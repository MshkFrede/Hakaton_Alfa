// Package policy — кредитная политика: превращает оценку риска (scoring) и параметры заявки
// в решение и условия. Отделена от скоринга, чтобы правила банка можно было менять,
// не трогая модель риска, и наоборот.
package policy

import (
	"fmt"

	"credit-conveyor/internal/domain"
	"credit-conveyor/internal/scoring"
)

// Параметры политики (в реальном банке — настраиваемый справочник).
const (
	// MaxDebtToRevenue — предельная доля всех платежей по кредитам в месячной выручке.
	MaxDebtToRevenue = 0.30
	// MinScoreForManual — ниже этого балла отказ без ручного разбора.
	MinScoreForManual = 420
	// RoundTo — округление сумм контрпредложения.
	RoundTo = 10_000
)

// RateSpread — надбавка к базовой ставке продукта за рейтинг риска (п.п.).
var RateSpread = map[string]float64{"A": 0, "B": 1.5, "C": 3, "D": 5}

type Outcome string

const (
	OutcomeApprove      Outcome = "APPROVE"
	OutcomeCounterOffer Outcome = "COUNTER_OFFER"
	OutcomeManualReview Outcome = "MANUAL_REVIEW"
	OutcomeReject       Outcome = "REJECT"
)

// Decision — результат применения политики. Если Outcome = MANUAL_REVIEW,
// Suggested содержит рекомендацию системы для андеррайтера.
type Decision struct {
	Outcome       Outcome  `json:"outcome"`
	Suggested     Outcome  `json:"suggested"`
	Offer         *Offer   `json:"offer,omitempty"`
	Rate          float64  `json:"rate"`
	DebtToRevenue float64  `json:"debt_to_revenue"` // нагрузка с учётом нового платежа
	MaxAmount     int64    `json:"max_amount"`      // максимум, который выдерживает нагрузка
	Reasons       []string `json:"reasons"`         // для андеррайтера и аудита
	ClientMessage string   `json:"client_message"`  // для клиента, без внутренних деталей
	ManualReasons []string `json:"manual_reasons,omitempty"`
}

type Offer struct {
	Amount         int64   `json:"amount"`
	TermMonths     int     `json:"term_months"`
	Rate           float64 `json:"rate"`
	MonthlyPayment int64   `json:"monthly_payment"`
	Changed        bool    `json:"changed"`
}

// Request — параметры заявки, существенные для политики.
type Request struct {
	Product    domain.Product
	Amount     int64
	TermMonths int
}

// Decide применяет политику. Порядок правил важен и описан в docs/credit-policy.md.
func Decide(req Request, sc scoring.Result) Decision {
	d := Decision{Reasons: []string{}}
	rate := req.Product.BaseRate + RateSpread[sc.Grade]
	d.Rate = rate

	// 1. Стоп-факторы — отказ без вариантов.
	if len(sc.StopFactors) > 0 {
		d.Outcome, d.Suggested = OutcomeReject, OutcomeReject
		for _, s := range sc.StopFactors {
			d.Reasons = append(d.Reasons, "Стоп-фактор: "+s.Description)
		}
		d.ClientMessage = "К сожалению, сейчас банк не может предоставить финансирование. " +
			"Решение принято по результатам проверки данных о компании и кредитной истории."
		return d
	}

	// 2. Расчёт нагрузки и максимальной суммы.
	monthlyRevenue := sc.ConfirmedAnnualRevenue / 12
	payment := domain.AnnuityPayment(req.Amount, rate, req.TermMonths)
	if monthlyRevenue > 0 {
		d.DebtToRevenue = float64(sc.ExistingMonthlyPayments+payment) / float64(monthlyRevenue)
	} else {
		d.DebtToRevenue = 99
	}
	capacity := int64(float64(monthlyRevenue)*MaxDebtToRevenue) - sc.ExistingMonthlyPayments
	d.MaxAmount = min(domain.MaxAmountForPayment(capacity, rate, req.TermMonths), req.Product.MaxAmount)
	fits := d.DebtToRevenue <= MaxDebtToRevenue
	d.Reasons = append(d.Reasons,
		fmt.Sprintf("Рейтинг %s (балл %d), ставка %.1f%%", sc.Grade, sc.Score, rate),
		fmt.Sprintf("Нагрузка с новым платежом: %.0f%% выручки (лимит %.0f%%)", d.DebtToRevenue*100, MaxDebtToRevenue*100))

	// 3. Слишком низкий балл — отказ.
	if sc.Score < MinScoreForManual {
		d.Outcome, d.Suggested = OutcomeReject, OutcomeReject
		d.Reasons = append(d.Reasons, fmt.Sprintf("Балл ниже порога %d", MinScoreForManual))
		d.ClientMessage = "К сожалению, сейчас банк не может предоставить финансирование. " +
			"Основные причины: " + clientHints(sc) + "."
		return d
	}

	// 4. Что предложила бы система (используется и как авторешение, и как рекомендация).
	var offer *Offer
	switch {
	case fits:
		d.Suggested = OutcomeApprove
		offer = &Offer{Amount: req.Amount, TermMonths: req.TermMonths, Rate: rate, MonthlyPayment: payment}
	default:
		offer = counterOffer(req, rate, capacity)
		if offer != nil {
			d.Suggested = OutcomeCounterOffer
			d.Reasons = append(d.Reasons, fmt.Sprintf("Запрошенный платёж не укладывается в лимит нагрузки; предложено %s на %d мес.",
				domain.FormatRub(offer.Amount), offer.TermMonths))
		} else {
			d.Suggested = OutcomeReject
			d.Reasons = append(d.Reasons, "Выручки недостаточно даже для минимальной суммы продукта")
		}
	}
	d.Offer = offer

	// 5. Нужен ли человек.
	var manual []string
	if len(sc.MissingSources) > 0 {
		manual = append(manual, fmt.Sprintf("Нет данных из источников: %v — нужна ручная проверка", sc.MissingSources))
	}
	if sc.Grade != "A" && sc.Grade != "B" {
		manual = append(manual, "Рейтинг "+sc.Grade+" — автоматическое решение не принимается")
	}
	if req.Product.AutoApproveLimit == 0 {
		manual = append(manual, "Продукт «"+req.Product.Name+"» всегда рассматривается специалистом")
	} else if offer != nil && offer.Amount > req.Product.AutoApproveLimit {
		manual = append(manual, "Сумма выше лимита автоматического одобрения "+domain.FormatRub(req.Product.AutoApproveLimit))
	}
	if d.Suggested == OutcomeReject && len(sc.MissingSources) == 0 && sc.Grade == "D" {
		d.Outcome = OutcomeReject
		d.ClientMessage = "К сожалению, текущая выручка не позволяет обслуживать кредит такого размера."
		return d
	}
	if len(manual) > 0 || d.Suggested == OutcomeReject {
		if d.Suggested == OutcomeReject {
			manual = append(manual, "Система рекомендует отказ — требуется подтверждение специалиста")
		}
		d.Outcome = OutcomeManualReview
		d.ManualReasons = manual
		d.Reasons = append(d.Reasons, manual...)
		d.ClientMessage = "Заявку рассматривает кредитный специалист. Обычно это занимает до одного рабочего дня."
		return d
	}

	d.Outcome = d.Suggested
	if d.Outcome == OutcomeApprove {
		d.ClientMessage = "Заявка одобрена на запрошенных условиях."
	} else {
		d.ClientMessage = "Банк готов предоставить финансирование на других условиях: так платёж будет комфортным для вашего бизнеса."
	}
	return d
}

// counterOffer подбирает альтернативу: сначала увеличить срок при той же сумме,
// затем уменьшить сумму при максимальном сроке продукта.
func counterOffer(req Request, rate float64, capacity int64) *Offer {
	if capacity <= 0 {
		return nil
	}
	p := req.Product
	for term := req.TermMonths + 6; term <= p.MaxTermMonths; term += 6 {
		pay := domain.AnnuityPayment(req.Amount, rate, term)
		if pay <= capacity {
			return &Offer{Amount: req.Amount, TermMonths: term, Rate: rate, MonthlyPayment: pay, Changed: true}
		}
	}
	term := p.MaxTermMonths
	amount := domain.MaxAmountForPayment(capacity, rate, term) / RoundTo * RoundTo
	amount = min(amount, p.MaxAmount)
	if amount < p.MinAmount {
		return nil
	}
	return &Offer{Amount: amount, TermMonths: term, Rate: rate, MonthlyPayment: domain.AnnuityPayment(amount, rate, term), Changed: true}
}

func clientHints(sc scoring.Result) string {
	var hints []string
	for _, f := range sc.Factors {
		if f.Points < 0 && f.ClientHint != "" {
			hints = append(hints, f.ClientHint)
		}
	}
	if len(hints) == 0 {
		return "совокупная оценка рисков"
	}
	if len(hints) > 3 {
		hints = hints[:3]
	}
	out := ""
	for i, h := range hints {
		if i > 0 {
			out += "; "
		}
		out += h
	}
	return out
}
