package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"credit-conveyor/internal/conveyor"
)

// ErrorBody — единый формат ошибки API.
type ErrorBody struct {
	Error struct {
		Code      string            `json:"code"`
		Message   string            `json:"message"`
		Fields    map[string]string `json:"fields,omitempty"`
		RequestID string            `json:"request_id,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string, fields map[string]string) {
	var b ErrorBody
	b.Error.Code, b.Error.Message, b.Error.Fields = code, msg, fields
	b.Error.RequestID = middleware.GetReqID(r.Context())
	writeJSON(w, status, b)
}

// handleErr переводит ошибки прикладного слоя в HTTP-ответы.
func (a *API) handleErr(w http.ResponseWriter, r *http.Request, err error) {
	var ve *conveyor.ValidationError
	var se *conveyor.StateError
	switch {
	case errors.As(err, &ve):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", ve.Message, ve.Fields)
	case errors.As(err, &se):
		writeError(w, r, http.StatusConflict, "INVALID_STATE", se.Msg, nil)
	case errors.Is(err, conveyor.ErrConflict):
		writeError(w, r, http.StatusConflict, "VERSION_CONFLICT", err.Error(), nil)
	case errors.Is(err, conveyor.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Не найдено", nil)
	case errors.Is(err, conveyor.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Нет доступа", nil)
	default:
		a.log.Error("internal error", "err", err, "path", r.URL.Path, "request_id", middleware.GetReqID(r.Context()))
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка. Попробуйте позже.", nil)
	}
}

// decode читает JSON-тело с ограничением размера и запретом неизвестных полей.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, r, http.StatusBadRequest, "BAD_REQUEST", "Некорректный JSON: "+err.Error(), nil)
		return false
	}
	return true
}

func logRequests(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				return
			}
			level := slog.LevelInfo
			if ww.Status() >= 500 {
				level = slog.LevelError
			}
			// Персональные данные и тела запросов в лог не пишем.
			log.Log(r.Context(), level, "http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(),
				"bytes", ww.BytesWritten(), "request_id", middleware.GetReqID(r.Context()))
		})
	}
}
