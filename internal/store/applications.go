package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"credit-conveyor/internal/domain"
)

const appSelect = `SELECT a.id, a.number, a.company_id, a.applicant_id, a.product_code, a.purpose, a.purpose_comment,
	a.amount, a.term_months, a.annual_revenue, a.existing_monthly_payments, a.has_collateral, a.collateral_description,
	a.contact_phone, a.bki_consent, a.status, a.status_changed_at, a.version, a.assigned_underwriter_id,
	coalesce(u.full_name, ''), a.offer_amount, a.offer_term_months, a.offer_rate, a.offer_monthly_payment,
	a.decision_reason, a.client_message, a.sign_code, a.signed_at, a.issued_at, a.account_number, a.submitted_at,
	a.created_at, a.updated_at, c.name, c.inn, sr.score, coalesce(sr.grade, '')
FROM applications a
JOIN companies c ON c.id = a.company_id
LEFT JOIN users u ON u.id = a.assigned_underwriter_id
LEFT JOIN LATERAL (SELECT score, grade FROM scoring_results s WHERE s.application_id = a.id ORDER BY id DESC LIMIT 1) sr ON true`

func scanApp(row interface{ Scan(...any) error }) (domain.Application, error) {
	var a domain.Application
	var num int64
	var status string
	var assigned sql.NullString
	var offAmount, offPay sql.NullInt64
	var offTerm sql.NullInt32
	var offRate sql.NullFloat64
	var signedAt, issuedAt, submittedAt sql.NullTime
	var score sql.NullInt32
	err := row.Scan(&a.ID, &num, &a.CompanyID, &a.ApplicantID, &a.ProductCode, &a.Purpose, &a.PurposeComment,
		&a.Amount, &a.TermMonths, &a.AnnualRevenue, &a.ExistingMonthlyPayments, &a.HasCollateral, &a.CollateralDescription,
		&a.ContactPhone, &a.BKIConsent, &status, &a.StatusChangedAt, &a.Version, &assigned,
		&a.AssignedUnderwriterName, &offAmount, &offTerm, &offRate, &offPay,
		&a.DecisionReason, &a.ClientMessage, &a.SignCode, &signedAt, &issuedAt, &a.AccountNumber, &submittedAt,
		&a.CreatedAt, &a.UpdatedAt, &a.CompanyName, &a.CompanyINN, &score, &a.Grade)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	a.Number = fmt.Sprintf("КЗ-%06d", num)
	a.Status = domain.Status(status)
	a.StatusTitle = domain.StatusTitle[a.Status]
	a.AssignedUnderwriterID = strPtr(assigned)
	if offAmount.Valid {
		a.Offer = &domain.Offer{Amount: offAmount.Int64, TermMonths: int(offTerm.Int32), Rate: offRate.Float64, MonthlyPayment: offPay.Int64}
		a.Offer.Changed = a.Offer.Amount != a.Amount || a.Offer.TermMonths != a.TermMonths
	}
	if signedAt.Valid {
		a.SignedAt = &signedAt.Time
	}
	if issuedAt.Valid {
		a.IssuedAt = &issuedAt.Time
	}
	if submittedAt.Valid {
		a.SubmittedAt = &submittedAt.Time
	}
	if score.Valid {
		v := int(score.Int32)
		a.Score = &v
	}
	if p, ok := domain.ProductByCode(a.ProductCode); ok {
		a.ProductName = p.Name
	}
	return a, nil
}

func (s *Store) GetApplication(ctx context.Context, q Querier, id string) (domain.Application, error) {
	return scanApp(q.QueryRowContext(ctx, appSelect+` WHERE a.id=$1`, id))
}

// GetApplicationForUpdate блокирует строку заявки до конца транзакции.
func (s *Store) GetApplicationForUpdate(ctx context.Context, tx *sql.Tx, id string) (domain.Application, error) {
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM applications WHERE id=$1 FOR UPDATE`, id); err != nil {
		return domain.Application{}, err
	}
	return s.GetApplication(ctx, tx, id)
}

type ListFilter struct {
	CompanyID string
	Statuses  []domain.Status
	Search    string
	Limit     int
}

func (s *Store) ListApplications(ctx context.Context, f ListFilter) ([]domain.Application, error) {
	var where []string
	var args []any
	if f.CompanyID != "" {
		args = append(args, f.CompanyID)
		where = append(where, fmt.Sprintf("a.company_id=$%d", len(args)))
	}
	if len(f.Statuses) > 0 {
		ph := placeholders(len(args)+1, len(f.Statuses))
		for _, st := range f.Statuses {
			args = append(args, string(st))
		}
		where = append(where, "a.status IN ("+ph+")")
	}
	if f.Search != "" {
		args = append(args, "%"+strings.ToLower(f.Search)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf("(lower(c.name) LIKE $%d OR c.inn LIKE $%d OR a.number::text LIKE $%d)", n, n, n))
	}
	q := appSelect
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	q += fmt.Sprintf(" ORDER BY a.status_changed_at DESC LIMIT %d", limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Application{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DraftFields — поля, которые клиент заполняет в черновике.
type DraftFields struct {
	ProductCode             string
	Purpose                 string
	PurposeComment          string
	Amount                  int64
	TermMonths              int
	AnnualRevenue           int64
	ExistingMonthlyPayments int64
	HasCollateral           bool
	CollateralDescription   string
	ContactPhone            string
	BKIConsent              bool
}

func (s *Store) CreateApplication(ctx context.Context, q Querier, id, companyID, applicantID string, f DraftFields) error {
	_, err := q.ExecContext(ctx, `INSERT INTO applications (id, company_id, applicant_id, product_code, purpose, purpose_comment,
		amount, term_months, annual_revenue, existing_monthly_payments, has_collateral, collateral_description,
		contact_phone, bki_consent, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'DRAFT')`,
		id, companyID, applicantID, f.ProductCode, f.Purpose, f.PurposeComment, f.Amount, f.TermMonths, f.AnnualRevenue,
		f.ExistingMonthlyPayments, f.HasCollateral, f.CollateralDescription, f.ContactPhone, f.BKIConsent)
	return err
}

// UpdateDraft обновляет черновик с оптимистичной блокировкой по version.
func (s *Store) UpdateDraft(ctx context.Context, q Querier, id string, version int, f DraftFields) error {
	res, err := q.ExecContext(ctx, `UPDATE applications SET product_code=$3, purpose=$4, purpose_comment=$5, amount=$6,
		term_months=$7, annual_revenue=$8, existing_monthly_payments=$9, has_collateral=$10, collateral_description=$11,
		contact_phone=$12, bki_consent=$13, version=version+1, updated_at=now()
		WHERE id=$1 AND version=$2 AND status='DRAFT'`,
		id, version, f.ProductCode, f.Purpose, f.PurposeComment, f.Amount, f.TermMonths, f.AnnualRevenue,
		f.ExistingMonthlyPayments, f.HasCollateral, f.CollateralDescription, f.ContactPhone, f.BKIConsent)
	return affected(res, err)
}

// StatusChange — смена статуса и сопутствующие поля, записываемые атомарно.
type StatusChange struct {
	From, To domain.Status
	Version  int // ожидаемая версия; 0 — не проверять (системные шаги под FOR UPDATE)
	Set      map[string]any
}

// allowedSetCols — белый список колонок, которые можно менять вместе со статусом.
var allowedSetCols = map[string]bool{
	"offer_amount": true, "offer_term_months": true, "offer_rate": true, "offer_monthly_payment": true,
	"decision_reason": true, "client_message": true, "sign_code": true, "signed_at": true, "issued_at": true,
	"account_number": true, "submitted_at": true, "assigned_underwriter_id": true,
}

// ChangeStatus атомарно меняет статус, если заявка всё ещё в ожидаемом статусе (и версии).
func (s *Store) ChangeStatus(ctx context.Context, q Querier, id string, ch StatusChange) error {
	sets := []string{"status=$2", "status_changed_at=now()", "updated_at=now()", "version=version+1"}
	args := []any{id, string(ch.To), string(ch.From)}
	for col, v := range ch.Set {
		if !allowedSetCols[col] {
			return fmt.Errorf("column %s not allowed", col)
		}
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)))
	}
	where := "id=$1 AND status=$3"
	if ch.Version > 0 {
		args = append(args, ch.Version)
		where += fmt.Sprintf(" AND version=$%d", len(args))
	}
	res, err := q.ExecContext(ctx, "UPDATE applications SET "+strings.Join(sets, ", ")+" WHERE "+where, args...)
	return affected(res, err)
}

func (s *Store) AssignUnderwriter(ctx context.Context, q Querier, id, underwriterID string) error {
	res, err := q.ExecContext(ctx, `UPDATE applications SET assigned_underwriter_id=$2, updated_at=now(), version=version+1
		WHERE id=$1 AND status='MANUAL_REVIEW'`, id, underwriterID)
	return affected(res, err)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

// --- События ---

func (s *Store) AddEvent(ctx context.Context, q Querier, e domain.Event) error {
	var from, to any
	if e.FromStatus != nil {
		from = string(*e.FromStatus)
	}
	if e.ToStatus != nil {
		to = string(*e.ToStatus)
	}
	var payload any
	if len(e.Payload) > 0 {
		payload = []byte(e.Payload)
	}
	if e.Visibility == "" {
		e.Visibility = "ALL"
	}
	_, err := q.ExecContext(ctx, `INSERT INTO application_events (application_id, from_status, to_status, event_type,
		actor_type, actor_id, actor_name, message, internal_comment, visibility, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		e.ApplicationID, from, to, e.Type, string(e.ActorType), nullStr(e.ActorID), e.ActorName, e.Message,
		e.InternalComment, e.Visibility, payload)
	return err
}

func (s *Store) ListEvents(ctx context.Context, appID string, includeBank bool) ([]domain.Event, error) {
	q := `SELECT id, application_id, from_status, to_status, event_type, actor_type, actor_id, actor_name, message,
		internal_comment, visibility, payload, created_at FROM application_events WHERE application_id=$1`
	if !includeBank {
		q += ` AND visibility='ALL'`
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY id`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var e domain.Event
		var from, to, actorID sql.NullString
		var actorType string
		var payload []byte
		if err := rows.Scan(&e.ID, &e.ApplicationID, &from, &to, &e.Type, &actorType, &actorID, &e.ActorName,
			&e.Message, &e.InternalComment, &e.Visibility, &payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.ActorType = domain.ActorType(actorType)
		e.ActorID = strPtr(actorID)
		if from.Valid {
			st := domain.Status(from.String)
			e.FromStatus = &st
		}
		if to.Valid {
			st := domain.Status(to.String)
			e.ToStatus = &st
		}
		if len(payload) > 0 {
			e.Payload = json.RawMessage(payload)
		}
		if !includeBank {
			e.InternalComment = ""
			e.Payload = nil
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Обогащение и скоринг ---

func (s *Store) SaveEnrichment(ctx context.Context, q Querier, appID string, r domain.EnrichmentResult) error {
	var data any
	if len(r.Data) > 0 {
		data = []byte(r.Data)
	}
	_, err := q.ExecContext(ctx, `INSERT INTO enrichment_results (application_id, source, status, data, error, duration_ms, attempts)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, appID, r.Source, r.Status, data, r.Error, r.DurationMS, r.Attempts)
	return err
}

// LatestEnrichment — последний результат по каждому источнику.
func (s *Store) LatestEnrichment(ctx context.Context, appID string) ([]domain.EnrichmentResult, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT ON (source) source, status, data, error, duration_ms, attempts, created_at
		FROM enrichment_results WHERE application_id=$1 ORDER BY source, id DESC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.EnrichmentResult{}
	for rows.Next() {
		var r domain.EnrichmentResult
		var data []byte
		if err := rows.Scan(&r.Source, &r.Status, &data, &r.Error, &r.DurationMS, &r.Attempts, &r.CreatedAt); err != nil {
			return nil, err
		}
		if len(data) > 0 {
			r.Data = json.RawMessage(data)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SaveScoring(ctx context.Context, q Querier, appID string, r domain.ScoringResult) error {
	_, err := q.ExecContext(ctx, `INSERT INTO scoring_results (application_id, model_version, score, grade, pd, factors,
		stop_factors, recommendation) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		appID, r.ModelVersion, r.Score, r.Grade, r.PD, []byte(r.Factors), []byte(r.StopFactors), []byte(r.Recommendation))
	return err
}

func (s *Store) LatestScoring(ctx context.Context, appID string) (*domain.ScoringResult, error) {
	var r domain.ScoringResult
	var f, sf, rec []byte
	err := s.DB.QueryRowContext(ctx, `SELECT model_version, score, grade, pd, factors, stop_factors, recommendation, created_at
		FROM scoring_results WHERE application_id=$1 ORDER BY id DESC LIMIT 1`, appID).
		Scan(&r.ModelVersion, &r.Score, &r.Grade, &r.PD, &f, &sf, &rec, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Factors, r.StopFactors, r.Recommendation = f, sf, rec
	return &r, nil
}

// --- Статистика для рабочего места банка ---

type Stats struct {
	ByStatus      map[string]int `json:"by_status"`
	Total         int            `json:"total"`
	AutoDecisions int            `json:"auto_decisions"`
	Decisions     int            `json:"decisions"`
	AvgDecisionS  float64        `json:"avg_decision_seconds"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	st := Stats{ByStatus: map[string]int{}}
	rows, err := s.DB.QueryContext(ctx, `SELECT status, count(*) FROM applications GROUP BY status`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return st, err
		}
		st.ByStatus[k] = n
		st.Total += n
	}
	err = s.DB.QueryRowContext(ctx, `SELECT
			count(*) FILTER (WHERE actor_type='SYSTEM'),
			count(*),
			coalesce(avg(extract(epoch FROM e.created_at - a.submitted_at)), 0)
		FROM application_events e JOIN applications a ON a.id=e.application_id
		WHERE e.event_type='DECISION'`).Scan(&st.AutoDecisions, &st.Decisions, &st.AvgDecisionS)
	return st, err
}

// SinceSubmit — сколько прошло с момента подачи (для подписей в UI).
func SinceSubmit(a domain.Application) time.Duration {
	if a.SubmittedAt == nil {
		return 0
	}
	return time.Since(*a.SubmittedAt)
}
