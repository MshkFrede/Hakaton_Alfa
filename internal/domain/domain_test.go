package domain

import "testing"

func TestTransitions(t *testing.T) {
	ok := []struct {
		from, to Status
		actor    ActorType
	}{
		{StatusDraft, StatusSubmitted, ActorClient},
		{StatusScoring, StatusManualReview, ActorSystem},
		{StatusManualReview, StatusCounterOffer, ActorUnderwriter},
		{StatusDocsRequested, StatusManualReview, ActorClient},
		{StatusCounterOffer, StatusSigning, ActorClient},
		{StatusSigned, StatusIssued, ActorSystem},
	}
	for _, c := range ok {
		if err := CanTransition(c.from, c.to, c.actor); err != nil {
			t.Errorf("%s→%s by %s: %v", c.from, c.to, c.actor, err)
		}
	}
	bad := []struct {
		from, to Status
		actor    ActorType
	}{
		{StatusDraft, StatusApproved, ActorClient},        // нельзя одобрить самому
		{StatusManualReview, StatusApproved, ActorClient}, // клиент не принимает решений
		{StatusDraft, StatusSubmitted, ActorUnderwriter},  // банк не подаёт за клиента
		{StatusApproved, StatusSigning, ActorUnderwriter}, // банк не принимает оффер за клиента
		{StatusRejected, StatusSigning, ActorClient},      // из финального статуса — никуда
		{StatusIssued, StatusCancelled, ActorClient},
	}
	for _, c := range bad {
		if err := CanTransition(c.from, c.to, c.actor); err == nil {
			t.Errorf("%s→%s by %s must be rejected", c.from, c.to, c.actor)
		}
	}
	for _, s := range AllStatuses {
		if s.IsFinal() && len(transitions[s]) > 0 {
			t.Errorf("final status %s has outgoing transitions", s)
		}
		if StatusTitle[s] == "" {
			t.Errorf("status %s has no title", s)
		}
	}
}

func TestValidINN(t *testing.T) {
	for _, inn := range []string{"7709012344", "771405678946", "7707083893"} {
		if !ValidINN(inn) {
			t.Errorf("%s must be valid", inn)
		}
	}
	for _, inn := range []string{"7709012345", "123", "77090123a4", "771405678947", ""} {
		if ValidINN(inn) {
			t.Errorf("%s must be invalid", inn)
		}
	}
}

func TestAnnuity(t *testing.T) {
	p := AnnuityPayment(1_000_000, 12, 12)
	if p < 88_848 || p > 88_850 { // эталон: 88 848,79
		t.Errorf("payment = %d", p)
	}
	max := MaxAmountForPayment(p, 12, 12)
	if max < 999_990 || max > 1_000_010 {
		t.Errorf("inverse = %d", max)
	}
}

func TestRecommend(t *testing.T) {
	r := Recommend("EQUIPMENT", 20_000_000)
	if !r[0].Recommended || r[0].Product.Code != "INVESTMENT" {
		t.Errorf("first = %+v", r[0])
	}
	for _, x := range r {
		if x.Product.Code == "EXPRESS" && x.Fits {
			t.Error("EXPRESS must not fit 20M")
		}
	}
	r = Recommend("CASH_GAP", 1_000_000)
	if r[0].Product.Code != "EXPRESS" {
		t.Errorf("cash gap → %s", r[0].Product.Code)
	}
}

func TestValidRuPhone(t *testing.T) {
	for _, p := range []string{"+7 999 123-45-67", "+79991234567", "89991234567", "8 (495) 123-45-67", "+7 (812) 000-00-00"} {
		if !ValidRuPhone(p) {
			t.Errorf("%q must be valid", p)
		}
	}
	for _, p := range []string{"1234567890", "79991234567", "+8 999 123-45-67", "+1 202 555-01-23", "+7 999 123-45-6", "+7 999 123-45-678",
		"89991234567x", "+7 199 123-45-67", "8+9991234567", "+380 99 123 4567", ""} {
		if ValidRuPhone(p) {
			t.Errorf("%q must be invalid", p)
		}
	}
}
