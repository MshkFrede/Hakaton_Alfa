package mocks

import (
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"credit-conveyor/internal/domain"
)

// Mode — режим работы источника, переключается через /admin/sources (для демо отказов).
type Mode string

const (
	ModeOK      Mode = "OK"
	ModeError   Mode = "ERROR"   // источник отвечает 503
	ModeTimeout Mode = "TIMEOUT" // источник «зависает»
)

type Server struct {
	latency time.Duration
	hang    time.Duration
	mu      sync.RWMutex
	modes   map[string]Mode
	log     *slog.Logger
}

func NewServer(latency, hang time.Duration, log *slog.Logger) *Server {
	return &Server{latency: latency, hang: hang, log: log,
		modes: map[string]Mode{"EGRUL": ModeOK, "BKI": ModeOK, "FNS": ModeOK}}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	r.Get("/egrul/v1/companies/{inn}", s.egrul)
	r.Get("/bki/v1/reports/{inn}", s.bki)
	r.Get("/fns/v1/transparent-business/{inn}", s.fns)
	r.Get("/admin/sources", s.getModes)
	r.Put("/admin/sources/{source}", s.setMode)
	return r
}

// simulate добавляет задержку и применяет режим источника. Возвращает false, если ответ уже записан.
func (s *Server) simulate(w http.ResponseWriter, r *http.Request, source string, forceTimeout bool) bool {
	s.mu.RLock()
	mode := s.modes[source]
	s.mu.RUnlock()
	if forceTimeout {
		mode = ModeTimeout
	}
	jitter := time.Duration(rand.IntN(200)) * time.Millisecond
	select {
	case <-time.After(s.latency + jitter):
	case <-r.Context().Done():
		return false
	}
	switch mode {
	case ModeError:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": source + " временно недоступен"})
		return false
	case ModeTimeout:
		select {
		case <-time.After(s.hang):
		case <-r.Context().Done():
		}
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": source + " не ответил"})
		return false
	}
	return true
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) (Profile, bool) {
	inn := chi.URLParam(r, "inn")
	if !domain.ValidINN(inn) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "некорректный ИНН"})
		return Profile{}, false
	}
	p, ok := LookupProfile(inn)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "субъект не найден"})
		return Profile{}, false
	}
	return p, true
}

func (s *Server) egrul(w http.ResponseWriter, r *http.Request) {
	if !s.simulate(w, r, "EGRUL", false) {
		return
	}
	if p, ok := s.profile(w, r); ok {
		writeJSON(w, 200, p.EGRUL)
	}
}

func (s *Server) bki(w http.ResponseWriter, r *http.Request) {
	p, found := LookupProfile(chi.URLParam(r, "inn"))
	if !s.simulate(w, r, "BKI", found && p.BKITimeout) {
		return
	}
	if p, ok := s.profile(w, r); ok {
		rep := p.BKI
		rep.ReportDate = time.Now().Format("2006-01-02")
		writeJSON(w, 200, rep)
	}
}

func (s *Server) fns(w http.ResponseWriter, r *http.Request) {
	if !s.simulate(w, r, "FNS", false) {
		return
	}
	if p, ok := s.profile(w, r); ok {
		writeJSON(w, 200, p.FNS)
	}
}

func (s *Server) getModes(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, 200, s.modes)
}

func (s *Server) setMode(w http.ResponseWriter, r *http.Request) {
	source := chi.URLParam(r, "source")
	var body struct {
		Mode Mode `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "ожидается {\"mode\": \"OK|ERROR|TIMEOUT\"}"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.modes[source]; !ok {
		writeJSON(w, 404, map[string]string{"error": "неизвестный источник"})
		return
	}
	switch body.Mode {
	case ModeOK, ModeError, ModeTimeout:
		s.modes[source] = body.Mode
		s.log.Info("source mode changed", "source", source, "mode", body.Mode)
		writeJSON(w, 200, s.modes)
	default:
		writeJSON(w, 400, map[string]string{"error": "mode: OK | ERROR | TIMEOUT"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
