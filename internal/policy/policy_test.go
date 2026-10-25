package policy_test

import (
	"testing"
	"time"

	"credit-conveyor/internal/domain"
	"credit-conveyor/internal/mocks"
	"credit-conveyor/internal/policy"
	"credit-conveyor/internal/scoring"
)

// Сквозная проверка «скоринг + политика» на демо-профилях: каждый ведёт к своему исходу.
func TestDemoProfilesOutcomes(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		inn      string
		product  string
		amount   int64
		term     int
		revenue  int64
		dropBKI  bool
		want     policy.Outcome
		wantSugg policy.Outcome
	}{
		{"Ромашка: автоодобрение", mocks.INNRomashka, "EXPRESS", 3_000_000, 24, 60_000_000, false, policy.OutcomeApprove, policy.OutcomeApprove},
		{"Вектор: контрпредложение", mocks.INNVector, "WORKING_CAPITAL", 10_000_000, 24, 12_000_000, false, policy.OutcomeCounterOffer, policy.OutcomeCounterOffer},
		{"Северов: ручной разбор", mocks.INNSeverov, "EXPRESS", 1_500_000, 24, 9_000_000, false, policy.OutcomeManualReview, policy.OutcomeApprove},
		{"Горизонт: отказ по стоп-фактору", mocks.INNGorizont, "WORKING_CAPITAL", 5_000_000, 24, 31_000_000, false, policy.OutcomeReject, policy.OutcomeReject},
		{"Туман: нет БКИ — ручной разбор", mocks.INNTuman, "EXPRESS", 2_000_000, 12, 24_000_000, true, policy.OutcomeManualReview, policy.OutcomeApprove},
		{"Ладога: инвесткредит всегда вручную", mocks.INNLadoga, "INVESTMENT", 8_000_000, 60, 42_000_000, false, policy.OutcomeManualReview, policy.OutcomeApprove},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, ok := mocks.LookupProfile(c.inn)
			if !ok {
				t.Fatal("profile not found")
			}
			in := scoring.Input{Now: now, DeclaredAnnualRevenue: c.revenue, EGRUL: &p.EGRUL, BKI: &p.BKI, FNS: &p.FNS}
			if c.dropBKI {
				in.BKI = nil
			}
			sc := scoring.Score(in)
			prod, _ := domain.ProductByCode(c.product)
			d := policy.Decide(policy.Request{Product: prod, Amount: c.amount, TermMonths: c.term}, sc)
			t.Logf("score=%d grade=%s outcome=%s suggested=%s dti=%.2f reasons=%v", sc.Score, sc.Grade, d.Outcome, d.Suggested, d.DebtToRevenue, d.Reasons)
			if d.Outcome != c.want {
				t.Errorf("outcome = %s, want %s", d.Outcome, c.want)
			}
			if d.Suggested != c.wantSugg {
				t.Errorf("suggested = %s, want %s", d.Suggested, c.wantSugg)
			}
			if (d.Outcome == policy.OutcomeApprove || d.Outcome == policy.OutcomeCounterOffer) && d.Offer == nil {
				t.Error("offer expected")
			}
		})
	}
}

func TestCounterOfferFitsLimit(t *testing.T) {
	prod, _ := domain.ProductByCode("WORKING_CAPITAL")
	sc := scoring.Result{Score: 720, Grade: "A", ConfirmedAnnualRevenue: 12_000_000, ExistingMonthlyPayments: 50_000,
		Factors: []scoring.Factor{}, StopFactors: []scoring.StopFactor{}}
	d := policy.Decide(policy.Request{Product: prod, Amount: 10_000_000, TermMonths: 24}, sc)
	if d.Offer == nil {
		t.Fatal("expected counter offer")
	}
	load := float64(d.Offer.MonthlyPayment+50_000) / 1_000_000
	if load > policy.MaxDebtToRevenue+1e-9 {
		t.Errorf("offer load %.3f exceeds limit", load)
	}
	if d.Offer.Amount < prod.MinAmount || d.Offer.TermMonths > prod.MaxTermMonths {
		t.Errorf("offer outside product limits: %+v", d.Offer)
	}
}
