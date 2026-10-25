// Package integrations — упрощённые контракты внешних источников данных и HTTP-клиенты к ним.
// Реальные сервисы (ЕГРЮЛ/ЕГРИП, бюро кредитных историй, «Прозрачный бизнес» ФНС)
// в прототипе заменены локальными моками (cmd/mocks), контракты сохраняют смысл интеграции.
package integrations

// EGRULCompany — выписка из ЕГРЮЛ/ЕГРИП (упрощённо).
type EGRULCompany struct {
	INN               string    `json:"inn"`
	OGRN              string    `json:"ogrn"`
	KPP               string    `json:"kpp,omitempty"`
	Name              string    `json:"name"`
	FullName          string    `json:"full_name"`
	LegalForm         string    `json:"legal_form"` // ООО | ИП | АО
	Status            string    `json:"status"`     // ACTIVE | LIQUIDATING | LIQUIDATED | BANKRUPTCY
	RegistrationDate  string    `json:"registration_date"`
	Director          Person    `json:"director"`
	Founders          []Founder `json:"founders"`
	OKVED             OKVED     `json:"okved"`
	Address           string    `json:"address"`
	AuthorizedCapital int64     `json:"authorized_capital"`
}

type Person struct {
	Name     string `json:"name"`
	Position string `json:"position"`
	INN      string `json:"inn"`
}

type Founder struct {
	Name  string  `json:"name"`
	Share float64 `json:"share"` // доля, %
}

type OKVED struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// BKIReport — кредитный отчёт из бюро кредитных историй (упрощённо).
type BKIReport struct {
	INN                  string    `json:"inn"`
	ReportDate           string    `json:"report_date"`
	CreditHistoryMonths  int       `json:"credit_history_months"`
	ActiveLoans          []BKILoan `json:"active_loans"`
	ClosedLoansCount     int       `json:"closed_loans_count"`
	MaxOverdueDays12M    int       `json:"max_overdue_days_12m"`
	CurrentOverdueAmount int64     `json:"current_overdue_amount"`
	Inquiries30D         int       `json:"inquiries_30d"`
}

type BKILoan struct {
	Creditor           string `json:"creditor"`
	Type               string `json:"type"`
	OpenedAt           string `json:"opened_at"`
	Amount             int64  `json:"amount"`
	Balance            int64  `json:"balance"`
	MonthlyPayment     int64  `json:"monthly_payment"`
	CurrentOverdueDays int    `json:"current_overdue_days"`
}

// FNSInfo — сведения сервиса «Прозрачный бизнес» ФНС (упрощённо).
type FNSInfo struct {
	INN                     string `json:"inn"`
	ReportingYear           int    `json:"reporting_year"`
	ReportedRevenueLastYear int64  `json:"reported_revenue_last_year"`
	EmployeesCount          int    `json:"employees_count"`
	TaxRegime               string `json:"tax_regime"`
	TaxArrears              int64  `json:"tax_arrears"`
	MassAddress             bool   `json:"mass_address"`
	MassDirector            bool   `json:"mass_director"`
	DisqualifiedDirector    bool   `json:"disqualified_director"`
}

// Источники данных.
const (
	SourceEGRUL = "EGRUL"
	SourceBKI   = "BKI"
	SourceFNS   = "FNS"
)
