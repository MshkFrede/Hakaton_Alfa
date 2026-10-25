// Package conveyor — прикладной слой кредитного конвейера: сценарии клиента, андеррайтера
// и системные шаги обработки. Здесь сходятся машина состояний, хранилище, интеграции,
// скоринг и кредитная политика. HTTP-слой и воркер вызывают только этот пакет.
package conveyor

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"credit-conveyor/internal/domain"
	"credit-conveyor/internal/integrations"
	"credit-conveyor/internal/store"
)

// Типы фоновых задач.
const (
	JobEnrich   = "ENRICH"
	JobScore    = "SCORE"
	JobDisburse = "DISBURSE"
)

var (
	ErrNotFound  = errors.New("не найдено")
	ErrForbidden = errors.New("нет доступа")
	ErrConflict  = errors.New("заявка изменилась — обновите страницу")
)

// ValidationError — ошибки по полям, показываются пользователю рядом с полями формы.
type ValidationError struct {
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string { return e.Message }

// StateError — действие недоступно в текущем статусе.
type StateError struct{ Msg string }

func (e *StateError) Error() string { return e.Msg }

// Actor — кто выполняет действие.
type Actor struct {
	Type      domain.ActorType
	ID        string
	Name      string
	CompanyID string
}

var SystemActor = Actor{Type: domain.ActorSystem, Name: "Кредитный конвейер"}

type Service struct {
	store     *store.Store
	sources   *integrations.Client
	stepDelay time.Duration
	log       *slog.Logger
	now       func() time.Time
}

func New(st *store.Store, sources *integrations.Client, stepDelay time.Duration, log *slog.Logger) *Service {
	return &Service{store: st, sources: sources, stepDelay: stepDelay, log: log, now: time.Now}
}

// transition — единая точка смены статуса: проверка по машине состояний, атомарное
// обновление с оптимистичной блокировкой и запись события в журнал (в одной транзакции).
type transitionOpts struct {
	version   int
	set       map[string]any
	eventType string
	message   string // видно клиенту
	internal  string // видно только банку
	payload   any
}

func (s *Service) transition(ctx context.Context, tx *sql.Tx, app domain.Application, to domain.Status, actor Actor, o transitionOpts) error {
	if err := domain.CanTransition(app.Status, to, actor.Type); err != nil {
		return &StateError{Msg: fmt.Sprintf("Действие недоступно: заявка в статусе «%s»", domain.StatusTitle[app.Status])}
	}
	err := s.store.ChangeStatus(ctx, tx, app.ID, store.StatusChange{From: app.Status, To: to, Version: o.version, Set: o.set})
	if errors.Is(err, store.ErrConflict) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	from := app.Status
	ev := domain.Event{
		ApplicationID: app.ID, FromStatus: &from, ToStatus: &to, Type: o.eventType,
		ActorType: actor.Type, ActorName: actor.Name, Message: o.message, InternalComment: o.internal, Visibility: "ALL",
	}
	if ev.Type == "" {
		ev.Type = "STATUS_CHANGED"
	}
	if actor.ID != "" {
		id := actor.ID
		ev.ActorID = &id
	}
	if o.payload != nil {
		b, _ := json.Marshal(o.payload)
		ev.Payload = b
	}
	return s.store.AddEvent(ctx, tx, ev)
}

func (s *Service) bankEvent(ctx context.Context, q store.Querier, appID, typ string, actor Actor, msg string, payload any) error {
	ev := domain.Event{ApplicationID: appID, Type: typ, ActorType: actor.Type, ActorName: actor.Name, Message: msg, Visibility: "BANK"}
	if actor.ID != "" {
		id := actor.ID
		ev.ActorID = &id
	}
	if payload != nil {
		b, _ := json.Marshal(payload)
		ev.Payload = b
	}
	return s.store.AddEvent(ctx, q, ev)
}

// loadForActor загружает заявку и проверяет право доступа (клиент видит только заявки своей компании).
func (s *Service) loadForActor(ctx context.Context, q store.Querier, id string, actor Actor) (domain.Application, error) {
	app, err := s.store.GetApplication(ctx, q, id)
	if errors.Is(err, store.ErrNotFound) {
		return app, ErrNotFound
	}
	if err != nil {
		return app, err
	}
	if actor.Type == domain.ActorClient && app.CompanyID != actor.CompanyID {
		return app, ErrNotFound // не раскрываем факт существования чужой заявки
	}
	return app, nil
}

func (s *Service) lockForActor(ctx context.Context, tx *sql.Tx, id string, actor Actor) (domain.Application, error) {
	if _, err := s.loadForActor(ctx, tx, id, actor); err != nil {
		return domain.Application{}, err
	}
	return s.store.GetApplicationForUpdate(ctx, tx, id)
}

// Get возвращает заявку с проверкой доступа.
func (s *Service) Get(ctx context.Context, id string, actor Actor) (domain.Application, error) {
	return s.loadForActor(ctx, s.store.DB, id, actor)
}

func randomDigits(n int) string {
	out := make([]byte, n)
	for i := range out {
		v, _ := rand.Int(rand.Reader, big.NewInt(10))
		out[i] = byte('0' + v.Int64())
	}
	return string(out)
}
