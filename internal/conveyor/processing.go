package conveyor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"credit-conveyor/internal/domain"
	"credit-conveyor/internal/integrations"
	"credit-conveyor/internal/policy"
	"credit-conveyor/internal/scoring"
)

// HandleJob — точка входа воркера. Все шаги идемпотентны: при повторе задачи
// (после сбоя) шаг либо продолжает с места остановки, либо ничего не делает.
func (s *Service) HandleJob(ctx context.Context, appID, kind string) error {
	switch kind {
	case JobEnrich:
		return s.stepEnrich(ctx, appID)
	case JobScore:
		return s.stepScore(ctx, appID)
	case JobDisburse:
		return s.stepDisburse(ctx, appID)
	}
	return fmt.Errorf("unknown job kind %q", kind)
}

var sourceTitle = map[string]string{
	integrations.SourceEGRUL: "ЕГРЮЛ/ЕГРИП",
	integrations.SourceBKI:   "Бюро кредитных историй",
	integrations.SourceFNS:   "ФНС «Прозрачный бизнес»",
}

// stepEnrich — запросы во внешние источники параллельно, с таймаутами и повторами.
// Недоступность источника не останавливает конвейер: заявка уйдёт на ручной разбор.
func (s *Service) stepEnrich(ctx context.Context, appID string) error {
	app, err := s.store.GetApplication(ctx, s.store.DB, appID)
	if err != nil {
		return err
	}
	switch app.Status {
	case domain.StatusSubmitted:
		err := s.store.Tx(ctx, func(tx *sql.Tx) error {
			return s.transition(ctx, tx, app, domain.StatusEnrichment, SystemActor, transitionOpts{
				message: "Проверяем данные о компании: реестр, кредитная история, налоговые сведения",
			})
		})
		if err != nil {
			return err
		}
	case domain.StatusEnrichment:
		// повтор после сбоя — продолжаем
	default:
		return nil // шаг уже пройден
	}

	inn := app.CompanyINN
	type res struct {
		r     domain.EnrichmentResult
		egrul *integrations.EGRULCompany
	}
	results := make([]res, 3)
	var wg sync.WaitGroup
	call := func(i int, source string, fn func(context.Context) (any, integrations.CallResult, error)) {
		defer wg.Done()
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		data, meta, err := fn(cctx)
		r := domain.EnrichmentResult{Source: source, DurationMS: meta.DurationMS, Attempts: meta.Attempts}
		if err != nil {
			r.Status = "ERROR"
			r.Error = err.Error()
			if errors.Is(err, integrations.ErrNotFound) {
				r.Error = "субъект не найден"
			}
		} else {
			r.Status = "OK"
			r.Data, _ = json.Marshal(data)
			if e, ok := data.(integrations.EGRULCompany); ok {
				results[i].egrul = &e
			}
		}
		results[i].r = r
	}
	wg.Add(3)
	go call(0, integrations.SourceEGRUL, func(c context.Context) (any, integrations.CallResult, error) { return s.sources.EGRUL(c, inn) })
	go call(1, integrations.SourceBKI, func(c context.Context) (any, integrations.CallResult, error) { return s.sources.BKI(c, inn) })
	go call(2, integrations.SourceFNS, func(c context.Context) (any, integrations.CallResult, error) { return s.sources.FNS(c, inn) })
	wg.Wait()

	var failed []string
	err = s.store.Tx(ctx, func(tx *sql.Tx) error {
		app, err := s.store.GetApplicationForUpdate(ctx, tx, appID)
		if err != nil {
			return err
		}
		if app.Status != domain.StatusEnrichment {
			return nil
		}
		for _, r := range results {
			if err := s.store.SaveEnrichment(ctx, tx, appID, r.r); err != nil {
				return err
			}
			msg := fmt.Sprintf("%s: данные получены за %d мс", sourceTitle[r.r.Source], r.r.DurationMS)
			if r.r.Status != "OK" {
				msg = fmt.Sprintf("%s: ошибка после %d попыток — %s", sourceTitle[r.r.Source], r.r.Attempts, r.r.Error)
				failed = append(failed, sourceTitle[r.r.Source])
			}
			if err := s.bankEvent(ctx, tx, appID, "ENRICHMENT_"+r.r.Source, SystemActor, msg, nil); err != nil {
				return err
			}
			// Обогащение карточки компании актуальными данными реестра.
			if r.egrul != nil {
				if _, err := s.store.UpsertCompany(ctx, tx, *r.egrul); err != nil {
					return err
				}
			}
		}
		internal := ""
		if len(failed) > 0 {
			internal = "Недоступны источники: " + strings.Join(failed, ", ")
		}
		if err := s.transition(ctx, tx, app, domain.StatusScoring, SystemActor, transitionOpts{
			message:  "Данные проверены. Оцениваем заявку",
			internal: internal,
		}); err != nil {
			return err
		}
		return s.store.EnqueueJob(ctx, tx, appID, JobScore, s.stepDelay)
	})
	return err
}

// stepScore — скоринг и кредитная политика, затем перевод заявки в итоговый статус
// (автоматическое решение) либо на ручной разбор.
func (s *Service) stepScore(ctx context.Context, appID string) error {
	enr, err := s.store.LatestEnrichment(ctx, appID)
	if err != nil {
		return err
	}
	return s.store.Tx(ctx, func(tx *sql.Tx) error {
		app, err := s.store.GetApplicationForUpdate(ctx, tx, appID)
		if err != nil {
			return err
		}
		if app.Status != domain.StatusScoring {
			return nil
		}
		in := scoring.Input{
			Now:                     s.now(),
			DeclaredAnnualRevenue:   app.AnnualRevenue,
			DeclaredMonthlyPayments: app.ExistingMonthlyPayments,
			HasCollateral:           app.HasCollateral,
		}
		for _, r := range enr {
			if r.Status != "OK" {
				continue
			}
			switch r.Source {
			case integrations.SourceEGRUL:
				var v integrations.EGRULCompany
				if json.Unmarshal(r.Data, &v) == nil {
					in.EGRUL = &v
				}
			case integrations.SourceBKI:
				var v integrations.BKIReport
				if json.Unmarshal(r.Data, &v) == nil {
					in.BKI = &v
				}
			case integrations.SourceFNS:
				var v integrations.FNSInfo
				if json.Unmarshal(r.Data, &v) == nil {
					in.FNS = &v
				}
			}
		}
		sc := scoring.Score(in)
		product, _ := domain.ProductByCode(app.ProductCode)
		dec := policy.Decide(policy.Request{Product: product, Amount: app.Amount, TermMonths: app.TermMonths}, sc)

		if err := s.store.SaveScoring(ctx, tx, appID, domain.ScoringResult{
			ModelVersion: sc.ModelVersion, Score: sc.Score, Grade: sc.Grade, PD: sc.PD,
			Factors: mustJSON(sc.Factors), StopFactors: mustJSON(sc.StopFactors), Recommendation: mustJSON(dec),
		}); err != nil {
			return err
		}
		if err := s.bankEvent(ctx, tx, appID, "SCORED", SystemActor,
			fmt.Sprintf("Скоринг %s: балл %d, рейтинг %s; политика: %s (рекомендация: %s)", sc.ModelVersion, sc.Score, sc.Grade, dec.Outcome, dec.Suggested),
			map[string]any{"score": sc.Score, "grade": sc.Grade, "outcome": dec.Outcome, "suggested": dec.Suggested}); err != nil {
			return err
		}

		set := map[string]any{"decision_reason": strings.Join(dec.Reasons, "\n"), "client_message": dec.ClientMessage}
		if dec.Offer != nil && (dec.Outcome == policy.OutcomeApprove || dec.Outcome == policy.OutcomeCounterOffer) {
			set["offer_amount"], set["offer_term_months"] = dec.Offer.Amount, dec.Offer.TermMonths
			set["offer_rate"], set["offer_monthly_payment"] = dec.Offer.Rate, dec.Offer.MonthlyPayment
		}
		var to domain.Status
		var msg, evType string
		switch dec.Outcome {
		case policy.OutcomeApprove:
			to, msg, evType = domain.StatusApproved, "Заявка одобрена автоматически", "DECISION"
		case policy.OutcomeCounterOffer:
			to, msg, evType = domain.StatusCounterOffer, "Банк предлагает финансирование на других условиях", "DECISION"
		case policy.OutcomeReject:
			to, msg, evType = domain.StatusRejected, "Банк отказал в финансировании", "DECISION"
		default:
			to, msg, evType = domain.StatusManualReview, "Заявка передана кредитному специалисту", "ROUTED_TO_REVIEW"
		}
		return s.transition(ctx, tx, app, to, SystemActor, transitionOpts{set: set, eventType: evType, message: msg,
			internal: strings.Join(dec.Reasons, "; "), payload: map[string]any{"outcome": dec.Outcome, "suggested": dec.Suggested}})
	})
}

// stepDisburse — симуляция выдачи: открытие ссудного счёта и зачисление на расчётный счёт.
func (s *Service) stepDisburse(ctx context.Context, appID string) error {
	return s.store.Tx(ctx, func(tx *sql.Tx) error {
		app, err := s.store.GetApplicationForUpdate(ctx, tx, appID)
		if err != nil {
			return err
		}
		if app.Status != domain.StatusSigned {
			return nil
		}
		account := "40702810" + randomDigits(12)
		amount := app.Amount
		if app.Offer != nil {
			amount = app.Offer.Amount
		}
		return s.transition(ctx, tx, app, domain.StatusIssued, SystemActor, transitionOpts{
			set:       map[string]any{"issued_at": s.now(), "account_number": account},
			eventType: "ISSUED",
			message:   fmt.Sprintf("%s зачислены на расчётный счёт %s", domain.FormatRub(amount), maskAccount(account)),
		})
	})
}

func maskAccount(a string) string {
	if len(a) < 8 {
		return a
	}
	return a[:5] + "•••" + a[len(a)-4:]
}
