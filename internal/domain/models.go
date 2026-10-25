package domain

import (
	"encoding/json"
	"strings"
	"time"
)

type Role string

const (
	RoleClient      Role = "CLIENT"
	RoleUnderwriter Role = "UNDERWRITER"
)

type User struct {
	ID           string  `json:"id"`
	Email        string  `json:"email"`
	PasswordHash string  `json:"-"`
	FullName     string  `json:"full_name"`
	Position     string  `json:"position"`
	Role         Role    `json:"role"`
	CompanyID    *string `json:"company_id,omitempty"`
}

type Company struct {
	ID               string    `json:"id"`
	INN              string    `json:"inn"`
	OGRN             string    `json:"ogrn"`
	Name             string    `json:"name"`
	FullName         string    `json:"full_name"`
	LegalForm        string    `json:"legal_form"`
	Status           string    `json:"status"`
	RegistrationDate time.Time `json:"registration_date"`
	DirectorName     string    `json:"director_name"`
	OKVEDCode        string    `json:"okved_code"`
	OKVEDName        string    `json:"okved_name"`
	Address          string    `json:"address"`
}

// Application — кредитная заявка. Денежные суммы — в целых рублях.
type Application struct {
	ID                      string     `json:"id"`
	Number                  string     `json:"number"`
	CompanyID               string     `json:"company_id"`
	ApplicantID             string     `json:"applicant_id"`
	ProductCode             string     `json:"product_code"`
	Purpose                 string     `json:"purpose"`
	PurposeComment          string     `json:"purpose_comment"`
	Amount                  int64      `json:"amount"`
	TermMonths              int        `json:"term_months"`
	AnnualRevenue           int64      `json:"annual_revenue"`
	ExistingMonthlyPayments int64      `json:"existing_monthly_payments"`
	HasCollateral           bool       `json:"has_collateral"`
	CollateralDescription   string     `json:"collateral_description"`
	ContactPhone            string     `json:"contact_phone"`
	BKIConsent              bool       `json:"bki_consent"`
	Status                  Status     `json:"status"`
	StatusTitle             string     `json:"status_title"`
	StatusChangedAt         time.Time  `json:"status_changed_at"`
	Version                 int        `json:"version"`
	AssignedUnderwriterID   *string    `json:"assigned_underwriter_id,omitempty"`
	AssignedUnderwriterName string     `json:"assigned_underwriter_name,omitempty"`
	Offer                   *Offer     `json:"offer,omitempty"`
	DecisionReason          string     `json:"decision_reason,omitempty"`
	ClientMessage           string     `json:"client_message,omitempty"`
	SignCode                string     `json:"-"`
	SignedAt                *time.Time `json:"signed_at,omitempty"`
	IssuedAt                *time.Time `json:"issued_at,omitempty"`
	AccountNumber           string     `json:"account_number,omitempty"`
	SubmittedAt             *time.Time `json:"submitted_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`

	// Денормализованные поля для списков.
	CompanyName string `json:"company_name,omitempty"`
	CompanyINN  string `json:"company_inn,omitempty"`
	ProductName string `json:"product_name,omitempty"`
	Score       *int   `json:"score,omitempty"`
	Grade       string `json:"grade,omitempty"`
}

// Offer — условия, которые банк готов предоставить.
type Offer struct {
	Amount         int64   `json:"amount"`
	TermMonths     int     `json:"term_months"`
	Rate           float64 `json:"rate"`
	MonthlyPayment int64   `json:"monthly_payment"`
	Changed        bool    `json:"changed"` // условия отличаются от запрошенных
}

type Event struct {
	ID              int64           `json:"id"`
	ApplicationID   string          `json:"application_id"`
	FromStatus      *Status         `json:"from_status,omitempty"`
	ToStatus        *Status         `json:"to_status,omitempty"`
	Type            string          `json:"type"`
	ActorType       ActorType       `json:"actor_type"`
	ActorID         *string         `json:"actor_id,omitempty"`
	ActorName       string          `json:"actor_name"`
	Message         string          `json:"message"`
	InternalComment string          `json:"internal_comment,omitempty"`
	Visibility      string          `json:"visibility"` // ALL | BANK
	Payload         json.RawMessage `json:"payload,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

type EnrichmentResult struct {
	Source     string          `json:"source"`
	Status     string          `json:"status"` // OK | ERROR
	Data       json.RawMessage `json:"data,omitempty"`
	Error      string          `json:"error,omitempty"`
	DurationMS int             `json:"duration_ms"`
	Attempts   int             `json:"attempts"`
	CreatedAt  time.Time       `json:"created_at"`
}

type ScoringResult struct {
	ModelVersion   string          `json:"model_version"`
	Score          int             `json:"score"`
	Grade          string          `json:"grade"`
	PD             float64         `json:"pd"`
	Factors        json.RawMessage `json:"factors"`
	StopFactors    json.RawMessage `json:"stop_factors"`
	Recommendation json.RawMessage `json:"recommendation"`
	CreatedAt      time.Time       `json:"created_at"`
}

type DocumentRequest struct {
	ID          string     `json:"id"`
	DocType     string     `json:"doc_type"`
	Title       string     `json:"title"`
	Comment     string     `json:"comment"`
	Status      string     `json:"status"` // REQUESTED | UPLOADED
	CreatedAt   time.Time  `json:"created_at"`
	FulfilledAt *time.Time `json:"fulfilled_at,omitempty"`
	Documents   []Document `json:"documents"`
}

type Document struct {
	ID          string    `json:"id"`
	RequestID   *string   `json:"request_id,omitempty"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"created_at"`
}

// DocTypes — типы документов, которые может запросить андеррайтер.
var DocTypes = map[string]string{
	"BANK_STATEMENT":   "Выписка по расчётному счёту в другом банке за 6 месяцев",
	"FINANCIAL_REPORT": "Бухгалтерская отчётность за последний год",
	"TAX_DECLARATION":  "Налоговая декларация за последний период",
	"CONTRACTS":        "Договоры с ключевыми покупателями",
	"COLLATERAL_DOCS":  "Документы на залоговое имущество",
	"OTHER":            "Другой документ",
}

// ValidINN проверяет контрольные разряды ИНН (10 цифр — юрлицо, 12 — ИП/физлицо).
func ValidINN(inn string) bool {
	d := make([]int, len(inn))
	for i, c := range inn {
		if c < '0' || c > '9' {
			return false
		}
		d[i] = int(c - '0')
	}
	check := func(coeffs []int) int {
		s := 0
		for i, c := range coeffs {
			s += c * d[i]
		}
		return s % 11 % 10
	}
	switch len(inn) {
	case 10:
		return check([]int{2, 4, 10, 3, 5, 9, 4, 6, 8}) == d[9]
	case 12:
		return check([]int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}) == d[10] &&
			check([]int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8}) == d[11]
	}
	return false
}

// ValidRuPhone проверяет российский номер: +7 или 8, затем 10 цифр, код начинается с 3, 4, 8 или 9.
// Допускаются пробелы, дефисы и скобки как разделители.
func ValidRuPhone(s string) bool {
	s = strings.TrimSpace(s)
	var digits []byte
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits = append(digits, byte(r))
		case r == '+' && i == 0:
		case r == ' ' || r == '-' || r == '(' || r == ')':
		default:
			return false
		}
	}
	plus := strings.HasPrefix(s, "+")
	if len(digits) != 11 {
		return false
	}
	switch {
	case plus && digits[0] == '7':
	case !plus && digits[0] == '8':
	default:
		return false
	}
	return strings.ContainsRune("3489", rune(digits[1]))
}
