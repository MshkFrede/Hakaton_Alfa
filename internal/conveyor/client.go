package conveyor

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"credit-conveyor/internal/domain"
	"credit-conveyor/internal/integrations"
	"credit-conveyor/internal/store"
)

// DraftInput — данные анкеты, которые клиент вводит в мастере заявки.
type DraftInput struct {
	ProductCode             string `json:"product_code"`
	Purpose                 string `json:"purpose"`
	PurposeComment          string `json:"purpose_comment"`
	Amount                  int64  `json:"amount"`
	TermMonths              int    `json:"term_months"`
	AnnualRevenue           int64  `json:"annual_revenue"`
	ExistingMonthlyPayments int64  `json:"existing_monthly_payments"`
	HasCollateral           bool   `json:"has_collateral"`
	CollateralDescription   string `json:"collateral_description"`
	ContactPhone            string `json:"contact_phone"`
	BKIConsent              bool   `json:"bki_consent"`
}

func (in DraftInput) fields() store.DraftFields {
	return store.DraftFields{
		ProductCode: in.ProductCode, Purpose: in.Purpose, PurposeComment: strings.TrimSpace(in.PurposeComment),
		Amount: in.Amount, TermMonths: in.TermMonths, AnnualRevenue: in.AnnualRevenue,
		ExistingMonthlyPayments: in.ExistingMonthlyPayments, HasCollateral: in.HasCollateral,
		CollateralDescription: strings.TrimSpace(in.CollateralDescription), ContactPhone: strings.TrimSpace(in.ContactPhone),
		BKIConsent: in.BKIConsent,
	}
}

// validateDraft — базовые проверки, которые не дают сохранить заведомо некорректные данные.
func validateDraft(in DraftInput) *ValidationError {
	f := map[string]string{}
	if _, ok := domain.ProductByCode(in.ProductCode); !ok {
		f["product_code"] = "Выберите продукт"
	}
	if _, ok := domain.PurposeByCode(in.Purpose); !ok {
		f["purpose"] = "Выберите задачу"
	}
	if in.Amount < 0 || in.AnnualRevenue < 0 || in.ExistingMonthlyPayments < 0 || in.TermMonths < 0 {
		f["amount"] = "Значения не могут быть отрицательными"
	}
	if len(in.PurposeComment) > 1000 || len(in.CollateralDescription) > 1000 {
		f["purpose_comment"] = "Слишком длинный текст"
	}
	if len(f) > 0 {
		return &ValidationError{Message: "Проверьте данные заявки", Fields: f}
	}
	return nil
}

// ValidateForSubmit — полная проверка перед отправкой в банк.
func ValidateForSubmit(a domain.Application) *ValidationError {
	f := map[string]string{}
	p, ok := domain.ProductByCode(a.ProductCode)
	if !ok {
		f["product_code"] = "Выберите продукт"
	} else {
		if a.Amount < p.MinAmount || a.Amount > p.MaxAmount {
			f["amount"] = fmt.Sprintf("Сумма для продукта — от %s до %s", domain.FormatRub(p.MinAmount), domain.FormatRub(p.MaxAmount))
		}
		if a.TermMonths < p.MinTermMonths || a.TermMonths > p.MaxTermMonths {
			f["term_months"] = fmt.Sprintf("Срок для продукта — от %d до %d мес.", p.MinTermMonths, p.MaxTermMonths)
		}
	}
	if _, ok := domain.PurposeByCode(a.Purpose); !ok {
		f["purpose"] = "Выберите задачу"
	} else if a.Purpose == "OTHER" && strings.TrimSpace(a.PurposeComment) == "" {
		f["purpose_comment"] = "Опишите, на что нужны деньги"
	}
	if a.AnnualRevenue <= 0 {
		f["annual_revenue"] = "Укажите выручку за последний год"
	}
	digits := 0
	for _, r := range a.ContactPhone {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	if digits < 10 || digits > 15 {
		f["contact_phone"] = "Укажите телефон для связи"
	}
	if a.HasCollateral && strings.TrimSpace(a.CollateralDescription) == "" {
		f["collateral_description"] = "Опишите обеспечение"
	}
	if !a.BKIConsent {
		f["bki_consent"] = "Нужно согласие на запрос кредитной истории"
	}
	if len(f) > 0 {
		return &ValidationError{Message: "Заполните обязательные поля", Fields: f}
	}
	return nil
}

func (s *Service) CreateDraft(ctx context.Context, actor Actor, in DraftInput) (domain.Application, error) {
	if actor.CompanyID == "" {
		return domain.Application{}, &StateError{Msg: "Сначала укажите компанию"}
	}
	if v := validateDraft(in); v != nil {
		return domain.Application{}, v
	}
	id := uuid.NewString()
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.store.CreateApplication(ctx, tx, id, actor.CompanyID, actor.ID, in.fields()); err != nil {
			return err
		}
		to := domain.StatusDraft
		return s.store.AddEvent(ctx, tx, domain.Event{ApplicationID: id, ToStatus: &to, Type: "CREATED",
			ActorType: actor.Type, ActorID: &actor.ID, ActorName: actor.Name, Message: "Создан черновик заявки", Visibility: "ALL"})
	})
	if err != nil {
		return domain.Application{}, err
	}
	return s.store.GetApplication(ctx, s.store.DB, id)
}

func (s *Service) UpdateDraft(ctx context.Context, actor Actor, id string, version int, in DraftInput) (domain.Application, error) {
	if v := validateDraft(in); v != nil {
		return domain.Application{}, v
	}
	app, err := s.loadForActor(ctx, s.store.DB, id, actor)
	if err != nil {
		return app, err
	}
	if app.Status != domain.StatusDraft {
		return app, &StateError{Msg: "Изменять можно только черновик"}
	}
	if err := s.store.UpdateDraft(ctx, s.store.DB, id, version, in.fields()); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return app, ErrConflict
		}
		return app, err
	}
	return s.store.GetApplication(ctx, s.store.DB, id)
}

// Submit отправляет заявку в банк. Идемпотентно: повторный вызов для уже отправленной заявки
// возвращает её текущее состояние без ошибки.
func (s *Service) Submit(ctx context.Context, actor Actor, id string) (domain.Application, error) {
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		app, err := s.lockForActor(ctx, tx, id, actor)
		if err != nil {
			return err
		}
		if app.Status != domain.StatusDraft && app.SubmittedAt != nil {
			return nil
		}
		if v := ValidateForSubmit(app); v != nil {
			return v
		}
		if err := s.transition(ctx, tx, app, domain.StatusSubmitted, actor, transitionOpts{
			set:     map[string]any{"submitted_at": s.now()},
			message: "Заявка отправлена в банк",
		}); err != nil {
			return err
		}
		// Задача создаётся в той же транзакции, что и смена статуса (transactional outbox).
		return s.store.EnqueueJob(ctx, tx, id, JobEnrich, s.stepDelay)
	})
	if err != nil {
		return domain.Application{}, err
	}
	return s.store.GetApplication(ctx, s.store.DB, id)
}

func (s *Service) Cancel(ctx context.Context, actor Actor, id string) (domain.Application, error) {
	return s.clientTransition(ctx, actor, id, domain.StatusCancelled, "Вы отозвали заявку", nil)
}

func (s *Service) AcceptOffer(ctx context.Context, actor Actor, id string) (domain.Application, error) {
	return s.clientTransition(ctx, actor, id, domain.StatusSigning,
		"Вы приняли условия банка. Осталось подписать договор кодом подтверждения.",
		map[string]any{"sign_code": randomDigits(6)})
}

func (s *Service) DeclineOffer(ctx context.Context, actor Actor, id string) (domain.Application, error) {
	return s.clientTransition(ctx, actor, id, domain.StatusDeclinedByClient, "Вы отказались от предложения банка", nil)
}

func (s *Service) clientTransition(ctx context.Context, actor Actor, id string, to domain.Status, msg string, set map[string]any) (domain.Application, error) {
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		app, err := s.lockForActor(ctx, tx, id, actor)
		if err != nil {
			return err
		}
		return s.transition(ctx, tx, app, to, actor, transitionOpts{set: set, message: msg})
	})
	if err != nil {
		return domain.Application{}, err
	}
	return s.store.GetApplication(ctx, s.store.DB, id)
}

// Sign — простая электронная подпись (симуляция): клиент вводит код, показанный в интерфейсе.
func (s *Service) Sign(ctx context.Context, actor Actor, id, code string) (domain.Application, error) {
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		app, err := s.lockForActor(ctx, tx, id, actor)
		if err != nil {
			return err
		}
		if app.Status != domain.StatusSigning {
			return &StateError{Msg: "Подписание недоступно в текущем статусе"}
		}
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(code)), []byte(app.SignCode)) != 1 {
			return &ValidationError{Message: "Неверный код подтверждения", Fields: map[string]string{"code": "Неверный код"}}
		}
		if err := s.transition(ctx, tx, app, domain.StatusSigned, actor, transitionOpts{
			set:       map[string]any{"signed_at": s.now()},
			eventType: "SIGNED",
			message:   "Договор подписан простой электронной подписью. Готовим выдачу средств.",
		}); err != nil {
			return err
		}
		return s.store.EnqueueJob(ctx, tx, id, JobDisburse, s.stepDelay)
	})
	if err != nil {
		return domain.Application{}, err
	}
	return s.store.GetApplication(ctx, s.store.DB, id)
}

// UploadDocument прикрепляет файл к запросу документов. Когда закрыты все запросы,
// заявка автоматически возвращается андеррайтеру.
func (s *Service) UploadDocument(ctx context.Context, actor Actor, id, requestID, fileName, contentType string, content []byte) (domain.Application, error) {
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		app, err := s.lockForActor(ctx, tx, id, actor)
		if err != nil {
			return err
		}
		if app.Status != domain.StatusDocsRequested {
			return &StateError{Msg: "Банк сейчас не ожидает документов по этой заявке"}
		}
		reqs, err := s.store.ListDocRequests(ctx, tx, id)
		if err != nil {
			return err
		}
		var req *domain.DocumentRequest
		for i := range reqs {
			if reqs[i].ID == requestID {
				req = &reqs[i]
			}
		}
		if req == nil {
			return &ValidationError{Message: "Запрос документа не найден", Fields: map[string]string{"request_id": "Неизвестный запрос"}}
		}
		if err := s.store.SaveDocument(ctx, tx, uuid.NewString(), id, &requestID, fileName, contentType, content, actor.ID); err != nil {
			return err
		}
		if err := s.store.MarkRequestUploaded(ctx, tx, requestID); err != nil {
			return err
		}
		if err := s.store.AddEvent(ctx, tx, domain.Event{ApplicationID: id, Type: "DOCUMENT_UPLOADED", ActorType: actor.Type,
			ActorID: &actor.ID, ActorName: actor.Name, Message: "Загружен документ: " + req.Title, Visibility: "ALL"}); err != nil {
			return err
		}
		open, err := s.store.CountOpenRequests(ctx, tx, id)
		if err != nil {
			return err
		}
		if open > 0 {
			return nil
		}
		app, err = s.store.GetApplication(ctx, tx, id)
		if err != nil {
			return err
		}
		return s.transition(ctx, tx, app, domain.StatusManualReview, actor, transitionOpts{
			message: "Все документы получены — заявка снова у специалиста",
		})
	})
	if err != nil {
		return domain.Application{}, err
	}
	return s.store.GetApplication(ctx, s.store.DB, id)
}

// LookupCompany — данные компании из ЕГРЮЛ/ЕГРИП по ИНН (автозаполнение).
func (s *Service) LookupCompany(ctx context.Context, inn string) (integrations.EGRULCompany, error) {
	inn = strings.TrimSpace(inn)
	if !domain.ValidINN(inn) {
		return integrations.EGRULCompany{}, &ValidationError{Message: "Некорректный ИНН", Fields: map[string]string{"inn": "Проверьте ИНН: 10 цифр для организации, 12 — для ИП"}}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, _, err := s.sources.EGRUL(ctx, inn)
	if errors.Is(err, integrations.ErrNotFound) {
		return c, &ValidationError{Message: "Компания не найдена в ЕГРЮЛ/ЕГРИП", Fields: map[string]string{"inn": "Не найдено в реестре"}}
	}
	if err != nil {
		return c, &StateError{Msg: "Реестр временно недоступен, попробуйте позже"}
	}
	return c, nil
}

// LinkCompany привязывает клиента к компании по ИНН (онбординг): данные берутся из ЕГРЮЛ.
func (s *Service) LinkCompany(ctx context.Context, actor Actor, inn string) (domain.Company, error) {
	if actor.CompanyID != "" {
		return domain.Company{}, &StateError{Msg: "Компания уже указана"}
	}
	e, err := s.LookupCompany(ctx, inn)
	if err != nil {
		return domain.Company{}, err
	}
	if e.Status != "ACTIVE" {
		return domain.Company{}, &ValidationError{Message: "Компания не действует", Fields: map[string]string{"inn": "Статус в реестре: " + e.Status}}
	}
	c, err := s.store.UpsertCompany(ctx, s.store.DB, e)
	if err != nil {
		return c, err
	}
	return c, s.store.SetUserCompany(ctx, actor.ID, c.ID)
}
