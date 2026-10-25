package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"credit-conveyor/internal/auth"
	"credit-conveyor/internal/conveyor"
	"credit-conveyor/internal/domain"
	"credit-conveyor/internal/policy"
	"credit-conveyor/internal/store"
)

// ---------- Аутентификация ----------

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if !a.loginLimiter.allow(r.RemoteAddr) {
		writeError(w, r, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "Слишком много попыток входа, подождите минуту", nil)
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := a.store.UserByEmail(r.Context(), strings.TrimSpace(in.Email))
	if err != nil || !auth.CheckPassword(u.PasswordHash, in.Password) {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			a.handleErr(w, r, err)
			return
		}
		writeError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Неверный email или пароль", nil)
		return
	}
	cid := ""
	if u.CompanyID != nil {
		cid = *u.CompanyID
	}
	tok, exp, err := a.issuer.Issue(u.ID, string(u.Role), u.FullName, cid)
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": tok, "token_type": "Bearer", "expires_at": exp, "user": u})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r.Context())
	u, err := a.store.UserByID(r.Context(), actor.ID)
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	resp := map[string]any{"user": u}
	if u.CompanyID != nil {
		c, err := a.store.CompanyByID(r.Context(), *u.CompanyID)
		if err != nil {
			a.handleErr(w, r, err)
			return
		}
		resp["company"] = c
	}
	writeJSON(w, 200, resp)
}

// ---------- Каталог и калькулятор ----------

func (a *API) products(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, domain.Products) }
func (a *API) purposes(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, domain.Purposes) }

func (a *API) docTypes(w http.ResponseWriter, _ *http.Request) {
	type dt struct {
		Code  string `json:"code"`
		Title string `json:"title"`
	}
	var out []dt
	for _, c := range []string{"BANK_STATEMENT", "FINANCIAL_REPORT", "TAX_DECLARATION", "CONTRACTS", "COLLATERAL_DOCS", "OTHER"} {
		out = append(out, dt{c, domain.DocTypes[c]})
	}
	writeJSON(w, 200, out)
}

func (a *API) recommendations(w http.ResponseWriter, r *http.Request) {
	amount, _ := strconv.ParseInt(r.URL.Query().Get("amount"), 10, 64)
	writeJSON(w, 200, domain.Recommend(r.URL.Query().Get("purpose"), amount))
}

func (a *API) statuses(w http.ResponseWriter, _ *http.Request) {
	type st struct {
		Code  domain.Status `json:"code"`
		Title string        `json:"title"`
		Final bool          `json:"final"`
	}
	var list []st
	for _, s := range domain.AllStatuses {
		list = append(list, st{s, domain.StatusTitle[s], s.IsFinal()})
	}
	writeJSON(w, 200, map[string]any{"statuses": list, "transitions": domain.TransitionTable()})
}

// calculate — предварительный расчёт платежа и нагрузки до подачи заявки.
func (a *API) calculate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProductCode             string `json:"product_code"`
		Amount                  int64  `json:"amount"`
		TermMonths              int    `json:"term_months"`
		AnnualRevenue           int64  `json:"annual_revenue"`
		ExistingMonthlyPayments int64  `json:"existing_monthly_payments"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, ok := domain.ProductByCode(in.ProductCode)
	if !ok {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Неизвестный продукт", map[string]string{"product_code": "Выберите продукт"})
		return
	}
	rateFrom, rateTo := p.BaseRate, p.BaseRate+policy.RateSpread["C"]
	resp := map[string]any{
		"rate_from": rateFrom, "rate_to": rateTo,
		"payment_from": domain.AnnuityPayment(in.Amount, rateFrom, in.TermMonths),
		"payment_to":   domain.AnnuityPayment(in.Amount, rateTo, in.TermMonths),
		"overpayment":  domain.AnnuityPayment(in.Amount, rateFrom, in.TermMonths)*int64(in.TermMonths) - in.Amount,
		"within_limits": in.Amount >= p.MinAmount && in.Amount <= p.MaxAmount &&
			in.TermMonths >= p.MinTermMonths && in.TermMonths <= p.MaxTermMonths,
	}
	if in.AnnualRevenue > 0 {
		monthly := in.AnnualRevenue / 12
		load := float64(in.ExistingMonthlyPayments+resp["payment_to"].(int64)) / float64(monthly)
		level, hint := "comfortable", "Платёж выглядит посильным для вашей выручки."
		switch {
		case load > policy.MaxDebtToRevenue:
			level, hint = "high", "Платёж велик для вашей выручки: банк может предложить меньшую сумму или больший срок."
		case load > policy.MaxDebtToRevenue*0.75:
			level, hint = "elevated", "Нагрузка близка к предельной — решение может потребовать проверки специалистом."
		}
		capacity := int64(float64(monthly)*policy.MaxDebtToRevenue) - in.ExistingMonthlyPayments
		resp["debt_to_revenue"] = load
		resp["load_level"] = level
		resp["hint"] = hint
		resp["max_amount_estimate"] = min(domain.MaxAmountForPayment(capacity, rateTo, in.TermMonths), p.MaxAmount)
	}
	writeJSON(w, 200, resp)
}

// ---------- Компании ----------

func (a *API) lookupCompany(w http.ResponseWriter, r *http.Request) {
	c, err := a.svc.LookupCompany(r.Context(), r.URL.Query().Get("inn"))
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	writeJSON(w, 200, c)
}

func (a *API) linkCompany(w http.ResponseWriter, r *http.Request) {
	var in struct {
		INN string `json:"inn"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := a.svc.LinkCompany(r.Context(), actorFrom(r.Context()), in.INN)
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	writeJSON(w, 200, c)
}

// ---------- Заявки ----------

func (a *API) listApplications(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r.Context())
	f := store.ListFilter{Search: r.URL.Query().Get("q")}
	if actor.Type == domain.ActorClient {
		if actor.CompanyID == "" {
			writeJSON(w, 200, []domain.Application{})
			return
		}
		f.CompanyID = actor.CompanyID
	} else {
		for _, s := range strings.Split(r.URL.Query().Get("status"), ",") {
			if st := domain.Status(strings.TrimSpace(s)); st.IsValid() {
				f.Statuses = append(f.Statuses, st)
			}
		}
	}
	list, err := a.store.ListApplications(r.Context(), f)
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	if actor.Type == domain.ActorClient {
		for i := range list { // клиенту внутренние оценки не показываем
			list[i].Score, list[i].Grade, list[i].DecisionReason = nil, "", ""
		}
	}
	writeJSON(w, 200, list)
}

func (a *API) createApplication(w http.ResponseWriter, r *http.Request) {
	var in conveyor.DraftInput
	if !decode(w, r, &in) {
		return
	}
	app, err := a.svc.CreateDraft(r.Context(), actorFrom(r.Context()), in)
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/applications/"+app.ID)
	a.writeView(w, r, http.StatusCreated, app)
}

func (a *API) getApplication(w http.ResponseWriter, r *http.Request) {
	app, err := a.svc.Get(r.Context(), chi.URLParam(r, "id"), actorFrom(r.Context()))
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	a.writeView(w, r, 200, app)
}

func (a *API) updateApplication(w http.ResponseWriter, r *http.Request) {
	var in struct {
		conveyor.DraftInput
		Version int `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	app, err := a.svc.UpdateDraft(r.Context(), actorFrom(r.Context()), chi.URLParam(r, "id"), in.Version, in.DraftInput)
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	a.writeView(w, r, 200, app)
}

type actionFn func(r *http.Request, actor conveyor.Actor, id string) (domain.Application, error)

func (a *API) action(fn actionFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := fn(r, actorFrom(r.Context()), chi.URLParam(r, "id"))
		if err != nil {
			a.handleErr(w, r, err)
			return
		}
		a.writeView(w, r, 200, app)
	}
}

func (a *API) submit(w http.ResponseWriter, r *http.Request) {
	a.action(func(r *http.Request, act conveyor.Actor, id string) (domain.Application, error) {
		return a.svc.Submit(r.Context(), act, id)
	})(w, r)
}

func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	a.action(func(r *http.Request, act conveyor.Actor, id string) (domain.Application, error) {
		return a.svc.Cancel(r.Context(), act, id)
	})(w, r)
}

func (a *API) acceptOffer(w http.ResponseWriter, r *http.Request) {
	a.action(func(r *http.Request, act conveyor.Actor, id string) (domain.Application, error) {
		return a.svc.AcceptOffer(r.Context(), act, id)
	})(w, r)
}

func (a *API) declineOffer(w http.ResponseWriter, r *http.Request) {
	a.action(func(r *http.Request, act conveyor.Actor, id string) (domain.Application, error) {
		return a.svc.DeclineOffer(r.Context(), act, id)
	})(w, r)
}

func (a *API) sign(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.action(func(r *http.Request, act conveyor.Actor, id string) (domain.Application, error) {
		return a.svc.Sign(r.Context(), act, id, in.Code)
	})(w, r)
}

func (a *API) assign(w http.ResponseWriter, r *http.Request) {
	a.action(func(r *http.Request, act conveyor.Actor, id string) (domain.Application, error) {
		return a.svc.Assign(r.Context(), act, id)
	})(w, r)
}

func (a *API) decide(w http.ResponseWriter, r *http.Request) {
	var in conveyor.DecisionInput
	if !decode(w, r, &in) {
		return
	}
	a.action(func(r *http.Request, act conveyor.Actor, id string) (domain.Application, error) {
		return a.svc.Decide(r.Context(), act, id, in)
	})(w, r)
}

var allowedExt = map[string]bool{".pdf": true, ".jpg": true, ".jpeg": true, ".png": true, ".xlsx": true, ".xls": true,
	".docx": true, ".doc": true, ".csv": true, ".txt": true, ".zip": true}

func (a *API) uploadDocument(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.maxUploadBytes+1<<20)
	if err := r.ParseMultipartForm(a.maxUploadBytes); err != nil {
		writeError(w, r, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE",
			fmt.Sprintf("Файл слишком большой (максимум %d МБ)", a.maxUploadBytes>>20), nil)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Прикрепите файл", map[string]string{"file": "Обязательно"})
		return
	}
	defer file.Close()
	name := filepath.Base(hdr.Filename)
	ext := strings.ToLower(filepath.Ext(name))
	if !allowedExt[ext] {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Недопустимый тип файла",
			map[string]string{"file": "Допустимы PDF, изображения, Office-документы, CSV, TXT, ZIP"})
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, a.maxUploadBytes+1))
	if err != nil || int64(len(content)) > a.maxUploadBytes || len(content) == 0 {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Не удалось прочитать файл", map[string]string{"file": "Пустой или слишком большой файл"})
		return
	}
	ctype := mime.TypeByExtension(ext)
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	app, err := a.svc.UploadDocument(r.Context(), actorFrom(r.Context()), chi.URLParam(r, "id"), r.FormValue("request_id"), name, ctype, content)
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	a.writeView(w, r, 200, app)
}

func (a *API) downloadDocument(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r.Context())
	id := chi.URLParam(r, "id")
	if _, err := a.svc.Get(r.Context(), id, actor); err != nil {
		a.handleErr(w, r, err)
		return
	}
	d, content, err := a.store.GetDocument(r.Context(), id, chi.URLParam(r, "docId"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = conveyor.ErrNotFound
		}
		a.handleErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", d.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.FileName}))
	_, _ = w.Write(content)
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r.Context())
	id := chi.URLParam(r, "id")
	if _, err := a.svc.Get(r.Context(), id, actor); err != nil {
		a.handleErr(w, r, err)
		return
	}
	ev, err := a.store.ListEvents(r.Context(), id, actor.Type == domain.ActorUnderwriter)
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	writeJSON(w, 200, ev)
}

func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	st, err := a.store.Stats(r.Context())
	if err != nil {
		a.handleErr(w, r, err)
		return
	}
	writeJSON(w, 200, st)
}

// ApplicationView — полное представление заявки для экрана: состав зависит от роли.
type ApplicationView struct {
	Application      domain.Application       `json:"application"`
	Product          *domain.Product          `json:"product,omitempty"`
	Company          *domain.Company          `json:"company,omitempty"`
	Events           []domain.Event           `json:"events"`
	DocumentRequests []domain.DocumentRequest `json:"document_requests"`
	MissingFields    map[string]string        `json:"missing_fields,omitempty"` // для черновика: чего не хватает до отправки
	SignCodeDemo     string                   `json:"sign_code_demo,omitempty"` // симуляция кода подписи (вместо SMS)
	Dossier          *conveyor.Dossier        `json:"dossier,omitempty"`        // только для банка
}

func (a *API) writeView(w http.ResponseWriter, r *http.Request, status int, app domain.Application) {
	ctx := r.Context()
	actor := actorFrom(ctx)
	isBank := actor.Type == domain.ActorUnderwriter
	v := ApplicationView{Application: app}
	if p, ok := domain.ProductByCode(app.ProductCode); ok {
		v.Product = &p
	}
	if c, err := a.store.CompanyByID(ctx, app.CompanyID); err == nil {
		v.Company = &c
	}
	var err error
	if v.Events, err = a.store.ListEvents(ctx, app.ID, isBank); err != nil {
		a.handleErr(w, r, err)
		return
	}
	if v.DocumentRequests, err = a.store.ListDocRequests(ctx, a.store.DB, app.ID); err != nil {
		a.handleErr(w, r, err)
		return
	}
	if app.Status == domain.StatusDraft {
		if ve := conveyor.ValidateForSubmit(app); ve != nil {
			v.MissingFields = ve.Fields
		}
	}
	if isBank {
		d, err := a.svc.Dossier(ctx, app.ID)
		if err != nil {
			a.handleErr(w, r, err)
			return
		}
		v.Dossier = &d
	} else {
		v.Application.Score, v.Application.Grade, v.Application.DecisionReason = nil, "", ""
		if app.Status == domain.StatusSigning {
			v.SignCodeDemo = app.SignCode
		}
	}
	writeJSON(w, status, v)
}
