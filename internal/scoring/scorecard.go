// Package scoring — балльная скоринговая модель (scorecard) оценки риска заёмщика.
//
// Скоринг оценивает именно заёмщика, а не конкретную заявку: сумма и срок в балл не входят.
// Превращение балла в решение и условия — задача кредитной политики (пакет policy).
// Поэтому одна и та же оценка риска может дать разные условия для разных продуктов.
package scoring

import (
	"fmt"
	"time"

	"credit-conveyor/internal/integrations"
)

const ModelVersion = "scorecard-v1"

// BaseScore — стартовый балл, к которому прибавляются вклады факторов.
const BaseScore = 500

// Input — признаки заёмщика, собранные из анкеты и внешних источников.
// Указатели на отчёты = nil, если источник был недоступен.
type Input struct {
	Now                     time.Time
	DeclaredAnnualRevenue   int64
	DeclaredMonthlyPayments int64
	HasCollateral           bool
	EGRUL                   *integrations.EGRULCompany
	BKI                     *integrations.BKIReport
	FNS                     *integrations.FNSInfo
}

// Factor — вклад одного признака в итоговый балл. Хранится для объяснимости решения.
type Factor struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Value      string `json:"value"`
	Points     int    `json:"points"`
	ClientHint string `json:"client_hint,omitempty"` // формулировка для клиента (без раскрытия модели)
}

// StopFactor — обстоятельство, при котором кредит не выдаётся независимо от балла.
type StopFactor struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

type Result struct {
	ModelVersion string       `json:"model_version"`
	Score        int          `json:"score"`
	Grade        string       `json:"grade"`
	PD           float64      `json:"pd"` // условная вероятность дефолта для рейтинга
	Factors      []Factor     `json:"factors"`
	StopFactors  []StopFactor `json:"stop_factors"`
	// MissingSources — источники, данные из которых не получены; оценка неполная.
	MissingSources []string `json:"missing_sources"`
	// Расчётные величины, которые нужны политике.
	ConfirmedAnnualRevenue  int64 `json:"confirmed_annual_revenue"`
	ExistingMonthlyPayments int64 `json:"existing_monthly_payments"`
}

// Grade-шкала. PD — иллюстративная калибровка для прототипа.
var gradePD = map[string]float64{"A": 0.01, "B": 0.03, "C": 0.07, "D": 0.15}

func GradeFor(score int) string {
	switch {
	case score >= 700:
		return "A"
	case score >= 600:
		return "B"
	case score >= 500:
		return "C"
	}
	return "D"
}

// Score рассчитывает балл. Функция чистая: одинаковый вход — одинаковый результат.
func Score(in Input) Result {
	res := Result{ModelVersion: ModelVersion, Factors: []Factor{}, StopFactors: []StopFactor{}, MissingSources: []string{}}
	add := func(code, name, value string, pts int, hint string) {
		res.Factors = append(res.Factors, Factor{Code: code, Name: name, Value: value, Points: pts, ClientHint: hint})
	}
	stop := func(code, desc string) { res.StopFactors = append(res.StopFactors, StopFactor{code, desc}) }

	// --- ЕГРЮЛ: статус и возраст бизнеса ---
	if in.EGRUL == nil {
		res.MissingSources = append(res.MissingSources, integrations.SourceEGRUL)
	} else {
		if in.EGRUL.Status != "ACTIVE" {
			stop("COMPANY_NOT_ACTIVE", "Компания не действует (статус в ЕГРЮЛ: "+in.EGRUL.Status+")")
		}
		if reg, err := time.Parse("2006-01-02", in.EGRUL.RegistrationDate); err == nil {
			months := monthsBetween(reg, in.Now)
			switch {
			case months < 3:
				stop("BUSINESS_TOO_YOUNG", "Бизнес зарегистрирован менее 3 месяцев назад")
				add("BUSINESS_AGE", "Возраст бизнеса", fmtMonths(months), -80, "Бизнес работает совсем недавно")
			case months < 12:
				add("BUSINESS_AGE", "Возраст бизнеса", fmtMonths(months), -60, "Бизнес работает меньше года")
			case months < 36:
				add("BUSINESS_AGE", "Возраст бизнеса", fmtMonths(months), 0, "")
			case months < 60:
				add("BUSINESS_AGE", "Возраст бизнеса", fmtMonths(months), 40, "Бизнес работает несколько лет")
			default:
				add("BUSINESS_AGE", "Возраст бизнеса", fmtMonths(months), 70, "Бизнес давно работает на рынке")
			}
		}
	}

	// --- БКИ: кредитная история и платёжная дисциплина ---
	if in.BKI == nil {
		res.MissingSources = append(res.MissingSources, integrations.SourceBKI)
		res.ExistingMonthlyPayments = in.DeclaredMonthlyPayments
	} else {
		b := in.BKI
		switch {
		case b.CreditHistoryMonths == 0:
			add("CREDIT_HISTORY", "Длина кредитной истории", "нет истории", -20, "Нет кредитной истории")
		case b.CreditHistoryMonths < 24:
			add("CREDIT_HISTORY", "Длина кредитной истории", fmtMonths(b.CreditHistoryMonths), 0, "")
		default:
			add("CREDIT_HISTORY", "Длина кредитной истории", fmtMonths(b.CreditHistoryMonths), 40, "Длительная кредитная история")
		}

		currentOverdue := 0
		var bkiPayments int64
		for _, l := range b.ActiveLoans {
			bkiPayments += l.MonthlyPayment
			if l.CurrentOverdueDays > currentOverdue {
				currentOverdue = l.CurrentOverdueDays
			}
		}
		// Берём максимум из заявленного клиентом и данных бюро — консервативно.
		res.ExistingMonthlyPayments = max(bkiPayments, in.DeclaredMonthlyPayments)

		switch {
		case b.MaxOverdueDays12M == 0:
			add("OVERDUE_12M", "Просрочки за 12 месяцев", "нет", 60, "Платежи по кредитам вносятся вовремя")
		case b.MaxOverdueDays12M <= 30:
			add("OVERDUE_12M", "Просрочки за 12 месяцев", fmt.Sprintf("до %d дн.", b.MaxOverdueDays12M), -40, "Были небольшие просрочки по кредитам")
		case b.MaxOverdueDays12M <= 90:
			add("OVERDUE_12M", "Просрочки за 12 месяцев", fmt.Sprintf("до %d дн.", b.MaxOverdueDays12M), -120, "Были существенные просрочки по кредитам")
		default:
			add("OVERDUE_12M", "Просрочки за 12 месяцев", fmt.Sprintf("до %d дн.", b.MaxOverdueDays12M), -200, "Были длительные просрочки по кредитам")
		}
		if currentOverdue > 0 {
			add("CURRENT_OVERDUE", "Текущая просрочка", fmt.Sprintf("%d дн.", currentOverdue), -100, "Есть непогашенная просрочка")
			if currentOverdue > 90 {
				stop("CURRENT_OVERDUE_90", fmt.Sprintf("Текущая просрочка %d дней (более 90)", currentOverdue))
			}
		}
		switch {
		case b.Inquiries30D >= 5:
			add("INQUIRIES", "Запросы кредитной истории за 30 дней", fmt.Sprint(b.Inquiries30D), -50, "Много недавних обращений за кредитами")
		case b.Inquiries30D >= 3:
			add("INQUIRIES", "Запросы кредитной истории за 30 дней", fmt.Sprint(b.Inquiries30D), -20, "")
		}
	}

	// --- ФНС: выручка, налоги, признаки недобросовестности ---
	res.ConfirmedAnnualRevenue = in.DeclaredAnnualRevenue
	if in.FNS == nil {
		res.MissingSources = append(res.MissingSources, integrations.SourceFNS)
	} else {
		f := in.FNS
		if f.ReportedRevenueLastYear > 0 {
			if in.DeclaredAnnualRevenue > f.ReportedRevenueLastYear*13/10 {
				add("REVENUE_CONSISTENCY", "Заявленная выручка vs отчётность ФНС",
					fmt.Sprintf("%s / %s", short(in.DeclaredAnnualRevenue), short(f.ReportedRevenueLastYear)), -60,
					"Заявленная выручка заметно выше отчётности")
			} else {
				add("REVENUE_CONSISTENCY", "Заявленная выручка vs отчётность ФНС",
					fmt.Sprintf("%s / %s", short(in.DeclaredAnnualRevenue), short(f.ReportedRevenueLastYear)), 20,
					"Выручка подтверждается отчётностью")
			}
			// Для расчёта нагрузки используем подтверждённую (меньшую) выручку.
			res.ConfirmedAnnualRevenue = min(in.DeclaredAnnualRevenue, f.ReportedRevenueLastYear)
		}
		if f.TaxArrears > 0 {
			add("TAX_ARREARS", "Налоговая задолженность", short(f.TaxArrears), -80, "Есть задолженность по налогам")
			if f.TaxArrears > 1_000_000 {
				stop("TAX_ARREARS_HIGH", "Налоговая задолженность более 1 млн ₽")
			}
		}
		if f.MassAddress {
			add("MASS_ADDRESS", "Адрес массовой регистрации", "да", -40, "")
		}
		if f.MassDirector {
			add("MASS_DIRECTOR", "Массовый руководитель", "да", -40, "")
		}
		if f.DisqualifiedDirector {
			stop("DISQUALIFIED_DIRECTOR", "Руководитель дисквалифицирован")
		}
	}

	// --- Текущая долговая нагрузка (без нового кредита) ---
	if res.ConfirmedAnnualRevenue > 0 {
		load := float64(res.ExistingMonthlyPayments) / (float64(res.ConfirmedAnnualRevenue) / 12)
		v := fmt.Sprintf("%.0f%% выручки", load*100)
		switch {
		case load < 0.10:
			add("DEBT_LOAD", "Текущие платежи по кредитам", v, 40, "Низкая текущая долговая нагрузка")
		case load < 0.25:
			add("DEBT_LOAD", "Текущие платежи по кредитам", v, 0, "")
		case load < 0.40:
			add("DEBT_LOAD", "Текущие платежи по кредитам", v, -60, "Высокая текущая долговая нагрузка")
		default:
			add("DEBT_LOAD", "Текущие платежи по кредитам", v, -120, "Очень высокая текущая долговая нагрузка")
		}
	}

	if in.HasCollateral {
		add("COLLATERAL", "Обеспечение (залог/поручительство)", "есть", 40, "Предложено обеспечение")
	}

	score := BaseScore
	for _, f := range res.Factors {
		score += f.Points
	}
	res.Score = max(0, min(1000, score))
	res.Grade = GradeFor(res.Score)
	res.PD = gradePD[res.Grade]
	return res
}

func monthsBetween(from, to time.Time) int {
	m := (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month())
	if to.Day() < from.Day() {
		m--
	}
	return max(0, m)
}

func fmtMonths(m int) string {
	if m < 12 {
		return fmt.Sprintf("%d мес.", m)
	}
	return fmt.Sprintf("%d г. %d мес.", m/12, m%12)
}

func short(v int64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1f млн ₽", float64(v)/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%d тыс. ₽", v/1_000)
	}
	return fmt.Sprintf("%d ₽", v)
}
