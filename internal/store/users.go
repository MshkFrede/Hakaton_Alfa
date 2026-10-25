package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"credit-conveyor/internal/domain"
	"credit-conveyor/internal/integrations"
)

const userCols = `id, email, password_hash, full_name, position, role, company_id`

func scanUser(row interface{ Scan(...any) error }) (domain.User, error) {
	var u domain.User
	var cid sql.NullString
	var role string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FullName, &u.Position, &role, &cid)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	u.Role = domain.Role(role)
	u.CompanyID = strPtr(cid)
	return u, err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE lower(email)=lower($1)`, email))
}

func (s *Store) UserByID(ctx context.Context, id string) (domain.User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

func (s *Store) CreateUser(ctx context.Context, q Querier, u domain.User) error {
	_, err := q.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, full_name, position, role, company_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (email) DO NOTHING`,
		u.ID, u.Email, u.PasswordHash, u.FullName, u.Position, string(u.Role), nullStr(u.CompanyID))
	return err
}

func (s *Store) SetUserCompany(ctx context.Context, userID, companyID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET company_id=$2 WHERE id=$1`, userID, companyID)
	return err
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

const companyCols = `id, inn, ogrn, name, full_name, legal_form, status, registration_date, director_name, okved_code, okved_name, address`

func scanCompany(row interface{ Scan(...any) error }) (domain.Company, error) {
	var c domain.Company
	var reg sql.NullTime
	err := row.Scan(&c.ID, &c.INN, &c.OGRN, &c.Name, &c.FullName, &c.LegalForm, &c.Status, &reg,
		&c.DirectorName, &c.OKVEDCode, &c.OKVEDName, &c.Address)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if reg.Valid {
		c.RegistrationDate = reg.Time
	}
	return c, err
}

func (s *Store) CompanyByID(ctx context.Context, id string) (domain.Company, error) {
	return scanCompany(s.DB.QueryRowContext(ctx, `SELECT `+companyCols+` FROM companies WHERE id=$1`, id))
}

func (s *Store) CompanyByINN(ctx context.Context, inn string) (domain.Company, error) {
	return scanCompany(s.DB.QueryRowContext(ctx, `SELECT `+companyCols+` FROM companies WHERE inn=$1`, inn))
}

// UpsertCompany создаёт или обновляет карточку компании по данным ЕГРЮЛ.
func (s *Store) UpsertCompany(ctx context.Context, q Querier, e integrations.EGRULCompany) (domain.Company, error) {
	var reg any
	if t, err := time.Parse("2006-01-02", e.RegistrationDate); err == nil {
		reg = t
	}
	row := q.QueryRowContext(ctx, `INSERT INTO companies (id, inn, ogrn, name, full_name, legal_form, status, registration_date,
			director_name, okved_code, okved_name, address)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (inn) DO UPDATE SET ogrn=EXCLUDED.ogrn, name=EXCLUDED.name, full_name=EXCLUDED.full_name,
			legal_form=EXCLUDED.legal_form, status=EXCLUDED.status, registration_date=EXCLUDED.registration_date,
			director_name=EXCLUDED.director_name, okved_code=EXCLUDED.okved_code, okved_name=EXCLUDED.okved_name,
			address=EXCLUDED.address
		RETURNING `+companyCols,
		uuid.NewString(), e.INN, e.OGRN, e.Name, e.FullName, e.LegalForm, e.Status, reg,
		e.Director.Name, e.OKVED.Code, e.OKVED.Name, e.Address)
	return scanCompany(row)
}
