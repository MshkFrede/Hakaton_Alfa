package conveyor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"

	"credit-conveyor/internal/domain"
	"credit-conveyor/internal/store"
)

// DecisionInput — решение андеррайтера по заявке на ручном разборе.
type DecisionInput struct {
	Action        string         `json:"action"` // APPROVE | COUNTER_OFFER | REQUEST_DOCS | REJECT
	Version       int            `json:"version"`
	Amount        int64          `json:"amount"`
	TermMonths    int            `json:"term_months"`
	Rate          float64        `json:"rate"`
	ClientMessage string         `json:"client_message"`
	Comment       string         `json:"comment"` // внутренний комментарий (виден только банку)
	Documents     []DocRequestIn `json:"documents"`
}

type DocRequestIn struct {
	DocType string `json:"doc_type"`
	Comment string `json:"comment"`
}

// Assign — андеррайтер берёт заявку в работу.
func (s *Service) Assign(ctx context.Context, actor Actor, id string) (domain.Application, error) {
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		app, err := s.store.GetApplicationForUpdate(ctx, tx, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if app.Status != domain.StatusManualReview {
			return &StateError{Msg: "Взять в работу можно только заявку на ручном разборе"}
		}
		if err := s.store.AssignUnderwriter(ctx, tx, id, actor.ID); err != nil {
			return err
		}
		return s.bankEvent(ctx, tx, id, "ASSIGNED", actor, actor.Name+" взял(а) заявку в работу", nil)
	})
	if err != nil {
		return domain.Application{}, err
	}
	return s.store.GetApplication(ctx, s.store.DB, id)
}

// Decide применяет решение андеррайтера.
func (s *Service) Decide(ctx context.Context, actor Actor, id string, in DecisionInput) (domain.Application, error) {
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		app, err := s.store.GetApplicationForUpdate(ctx, tx, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if app.Status != domain.StatusManualReview {
			return &StateError{Msg: "Решение можно принять только по заявке на ручном разборе"}
		}
		if in.Version > 0 && in.Version != app.Version {
			return ErrConflict
		}
		product, _ := domain.ProductByCode(app.ProductCode)
		comment := strings.TrimSpace(in.Comment)
		set := map[string]any{"assigned_underwriter_id": actor.ID, "decision_reason": comment}
		payload := map[string]any{"action": in.Action, "comment": comment}

		switch in.Action {
		case "APPROVE", "COUNTER_OFFER":
			amount, term, rate := in.Amount, in.TermMonths, in.Rate
			if amount == 0 {
				amount = app.Amount
			}
			if term == 0 {
				term = app.TermMonths
			}
			f := map[string]string{}
			if amount < product.MinAmount || amount > product.MaxAmount {
				f["amount"] = fmt.Sprintf("От %s до %s", domain.FormatRub(product.MinAmount), domain.FormatRub(product.MaxAmount))
			}
			if term < product.MinTermMonths || term > product.MaxTermMonths {
				f["term_months"] = fmt.Sprintf("От %d до %d мес.", product.MinTermMonths, product.MaxTermMonths)
			}
			if rate < product.BaseRate || rate > product.BaseRate+15 {
				f["rate"] = fmt.Sprintf("Ставка от %.1f%% до %.1f%%", product.BaseRate, product.BaseRate+15)
			}
			if len(f) > 0 {
				return &ValidationError{Message: "Проверьте условия", Fields: f}
			}
			rate = math.Round(rate*100) / 100
			payment := domain.AnnuityPayment(amount, rate, term)
			set["offer_amount"], set["offer_term_months"], set["offer_rate"], set["offer_monthly_payment"] = amount, term, rate, payment
			to := domain.StatusApproved
			msg := "Заявка одобрена"
			if in.Action == "COUNTER_OFFER" || amount != app.Amount || term != app.TermMonths {
				to = domain.StatusCounterOffer
				msg = "Банк предлагает финансирование на других условиях"
			}
			cm := strings.TrimSpace(in.ClientMessage)
			if cm == "" {
				cm = msg + "."
			}
			set["client_message"] = cm
			payload["offer"] = map[string]any{"amount": amount, "term_months": term, "rate": rate, "monthly_payment": payment}
			return s.transition(ctx, tx, app, to, actor, transitionOpts{set: set, eventType: "DECISION", message: msg, internal: comment, payload: payload})

		case "REQUEST_DOCS":
			if len(in.Documents) == 0 {
				return &ValidationError{Message: "Укажите, какие документы нужны", Fields: map[string]string{"documents": "Выберите хотя бы один документ"}}
			}
			var titles []string
			for _, d := range in.Documents {
				title, ok := domain.DocTypes[d.DocType]
				if !ok {
					return &ValidationError{Message: "Неизвестный тип документа", Fields: map[string]string{"documents": d.DocType}}
				}
				if err := s.store.CreateDocRequest(ctx, tx, uuid.NewString(), id, d.DocType, title, strings.TrimSpace(d.Comment), actor.ID); err != nil {
					return err
				}
				titles = append(titles, title)
			}
			cm := strings.TrimSpace(in.ClientMessage)
			if cm == "" {
				cm = "Для принятия решения нужны дополнительные документы. Загрузите их на странице заявки."
			}
			set["client_message"] = cm
			payload["documents"] = in.Documents
			return s.transition(ctx, tx, app, domain.StatusDocsRequested, actor, transitionOpts{set: set, eventType: "DOCS_REQUESTED",
				message: "Банк запросил документы: " + strings.Join(titles, "; "), internal: comment, payload: payload})

		case "REJECT":
			if comment == "" {
				return &ValidationError{Message: "Укажите причину отказа", Fields: map[string]string{"comment": "Обязательно для отказа"}}
			}
			cm := strings.TrimSpace(in.ClientMessage)
			if cm == "" {
				cm = "К сожалению, сейчас банк не может предоставить финансирование."
			}
			set["client_message"] = cm
			return s.transition(ctx, tx, app, domain.StatusRejected, actor, transitionOpts{set: set, eventType: "DECISION",
				message: "Банк отказал в финансировании", internal: comment, payload: payload})
		}
		return &ValidationError{Message: "Неизвестное действие", Fields: map[string]string{"action": "APPROVE | COUNTER_OFFER | REQUEST_DOCS | REJECT"}}
	})
	if err != nil {
		return domain.Application{}, err
	}
	return s.store.GetApplication(ctx, s.store.DB, id)
}

// Dossier — всё, что банк знает о заявке: внешние данные, скоринг, рекомендация.
type Dossier struct {
	Enrichment []domain.EnrichmentResult `json:"enrichment"`
	Scoring    *domain.ScoringResult     `json:"scoring"`
}

func (s *Service) Dossier(ctx context.Context, id string) (Dossier, error) {
	enr, err := s.store.LatestEnrichment(ctx, id)
	if err != nil {
		return Dossier{}, err
	}
	sc, err := s.store.LatestScoring(ctx, id)
	return Dossier{Enrichment: enr, Scoring: sc}, err
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
