package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"credit-conveyor/internal/domain"
)

// --- Очередь задач (transactional outbox) ---

type Job struct {
	ID            int64
	ApplicationID string
	Kind          string
	Attempts      int
	MaxAttempts   int
}

func (s *Store) EnqueueJob(ctx context.Context, q Querier, appID, kind string, delay time.Duration) error {
	_, err := q.ExecContext(ctx, `INSERT INTO jobs (application_id, kind, run_at) VALUES ($1, $2, now() + $3 * interval '1 millisecond')`,
		appID, kind, delay.Milliseconds())
	return err
}

// ClaimJob забирает одну готовую задачу. SKIP LOCKED позволяет запускать несколько воркеров/реплик
// без двойной обработки.
func (s *Store) ClaimJob(ctx context.Context) (*Job, error) {
	var j Job
	err := s.DB.QueryRowContext(ctx, `UPDATE jobs SET status='RUNNING', locked_at=now(), attempts=attempts+1, updated_at=now()
		WHERE id = (SELECT id FROM jobs WHERE status='PENDING' AND run_at <= now() ORDER BY run_at, id
		            FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, application_id, kind, attempts, max_attempts`).
		Scan(&j.ID, &j.ApplicationID, &j.Kind, &j.Attempts, &j.MaxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &j, err
}

func (s *Store) CompleteJob(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status='DONE', updated_at=now() WHERE id=$1`, id)
	return err
}

// FailJob — повтор с экспоненциальной задержкой или окончательная ошибка.
func (s *Store) FailJob(ctx context.Context, j *Job, cause error) error {
	if j.Attempts >= j.MaxAttempts {
		_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status='FAILED', last_error=$2, updated_at=now() WHERE id=$1`, j.ID, cause.Error())
		return err
	}
	backoff := time.Duration(1<<j.Attempts) * time.Second
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status='PENDING', last_error=$2, run_at=now() + $3 * interval '1 millisecond',
		updated_at=now() WHERE id=$1`, j.ID, cause.Error(), backoff.Milliseconds())
	return err
}

// RecoverStaleJobs возвращает в очередь задачи, «зависшие» в RUNNING (например, процесс упал).
func (s *Store) RecoverStaleJobs(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status='PENDING', updated_at=now()
		WHERE status='RUNNING' AND locked_at < now() - $1 * interval '1 millisecond'`, olderThan.Milliseconds())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- Документы ---

func (s *Store) CreateDocRequest(ctx context.Context, q Querier, id, appID, docType, title, comment, requestedBy string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO document_requests (id, application_id, doc_type, title, comment, requested_by)
		VALUES ($1,$2,$3,$4,$5,$6)`, id, appID, docType, title, comment, requestedBy)
	return err
}

func (s *Store) ListDocRequests(ctx context.Context, q Querier, appID string) ([]domain.DocumentRequest, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, doc_type, title, comment, status, created_at, fulfilled_at
		FROM document_requests WHERE application_id=$1 ORDER BY created_at`, appID)
	if err != nil {
		return nil, err
	}
	var out []domain.DocumentRequest
	byID := map[string]int{}
	for rows.Next() {
		var r domain.DocumentRequest
		var ful sql.NullTime
		if err := rows.Scan(&r.ID, &r.DocType, &r.Title, &r.Comment, &r.Status, &r.CreatedAt, &ful); err != nil {
			rows.Close()
			return nil, err
		}
		if ful.Valid {
			r.FulfilledAt = &ful.Time
		}
		r.Documents = []domain.Document{}
		byID[r.ID] = len(out)
		out = append(out, r)
	}
	rows.Close()
	docs, err := s.ListDocuments(ctx, q, appID)
	if err != nil {
		return nil, err
	}
	for _, d := range docs {
		if d.RequestID != nil {
			if i, ok := byID[*d.RequestID]; ok {
				out[i].Documents = append(out[i].Documents, d)
			}
		}
	}
	if out == nil {
		out = []domain.DocumentRequest{}
	}
	return out, nil
}

func (s *Store) ListDocuments(ctx context.Context, q Querier, appID string) ([]domain.Document, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, request_id, file_name, content_type, size_bytes, created_at
		FROM documents WHERE application_id=$1 ORDER BY created_at`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Document{}
	for rows.Next() {
		var d domain.Document
		var rid sql.NullString
		if err := rows.Scan(&d.ID, &rid, &d.FileName, &d.ContentType, &d.Size, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.RequestID = strPtr(rid)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) SaveDocument(ctx context.Context, q Querier, id, appID string, requestID *string, name, ctype string, content []byte, uploadedBy string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO documents (id, application_id, request_id, file_name, content_type, size_bytes, content, uploaded_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, id, appID, nullStr(requestID), name, ctype, len(content), content, uploadedBy)
	return err
}

func (s *Store) MarkRequestUploaded(ctx context.Context, q Querier, requestID string) error {
	_, err := q.ExecContext(ctx, `UPDATE document_requests SET status='UPLOADED', fulfilled_at=now() WHERE id=$1`, requestID)
	return err
}

func (s *Store) CountOpenRequests(ctx context.Context, q Querier, appID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM document_requests WHERE application_id=$1 AND status='REQUESTED'`, appID).Scan(&n)
	return n, err
}

func (s *Store) GetDocument(ctx context.Context, appID, docID string) (domain.Document, []byte, error) {
	var d domain.Document
	var content []byte
	err := s.DB.QueryRowContext(ctx, `SELECT id, file_name, content_type, size_bytes, created_at, content
		FROM documents WHERE id=$1 AND application_id=$2`, docID, appID).
		Scan(&d.ID, &d.FileName, &d.ContentType, &d.Size, &d.CreatedAt, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil, ErrNotFound
	}
	return d, content, err
}
