// Package httpapi — REST API кредитного конвейера (/api/v1) и раздача веб-интерфейса.
package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"credit-conveyor/internal/auth"
	"credit-conveyor/internal/conveyor"
	"credit-conveyor/internal/domain"
	"credit-conveyor/internal/store"
)

type API struct {
	store          *store.Store
	svc            *conveyor.Service
	issuer         *auth.Issuer
	log            *slog.Logger
	web            fs.FS
	openAPI        []byte
	corsOrigins    map[string]bool
	maxUploadBytes int64
	loginLimiter   *limiter
}

type Options struct {
	Web            fs.FS
	OpenAPI        []byte
	CORSOrigins    []string
	MaxUploadBytes int64
}

func New(st *store.Store, svc *conveyor.Service, issuer *auth.Issuer, log *slog.Logger, o Options) *API {
	origins := map[string]bool{}
	for _, o := range o.CORSOrigins {
		if o = strings.TrimSpace(o); o != "" {
			origins[o] = true
		}
	}
	return &API{store: st, svc: svc, issuer: issuer, log: log, web: o.Web, openAPI: o.OpenAPI,
		corsOrigins: origins, maxUploadBytes: o.MaxUploadBytes, loginLimiter: newLimiter(10, time.Minute)}
}

func (a *API) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, logRequests(a.log), middleware.Recoverer, securityHeaders, a.cors)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	r.Get("/readyz", a.ready)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
			_, _ = w.Write(a.openAPI)
		})
		r.Post("/auth/login", a.login)

		r.Get("/catalog/products", a.products)
		r.Get("/catalog/purposes", a.purposes)
		r.Get("/catalog/recommendations", a.recommendations)
		r.Post("/catalog/calculate", a.calculate)
		r.Get("/catalog/statuses", a.statuses)
		r.Get("/catalog/document-types", a.docTypes)

		r.Group(func(r chi.Router) {
			r.Use(a.authenticate)
			r.Get("/me", a.me)
			r.Get("/companies/lookup", a.lookupCompany)
			r.With(a.requireRole(domain.RoleClient)).Post("/me/company", a.linkCompany)

			r.Get("/applications", a.listApplications)
			r.With(a.requireRole(domain.RoleClient)).Post("/applications", a.createApplication)
			r.Route("/applications/{id}", func(r chi.Router) {
				r.Get("/", a.getApplication)
				r.Get("/events", a.events)
				r.Get("/documents/{docId}", a.downloadDocument)
				r.Group(func(r chi.Router) {
					r.Use(a.requireRole(domain.RoleClient))
					r.Patch("/", a.updateApplication)
					r.Post("/submit", a.submit)
					r.Post("/cancel", a.cancel)
					r.Post("/offer/accept", a.acceptOffer)
					r.Post("/offer/decline", a.declineOffer)
					r.Post("/sign", a.sign)
					r.Post("/documents", a.uploadDocument)
				})
				r.Group(func(r chi.Router) {
					r.Use(a.requireRole(domain.RoleUnderwriter))
					r.Post("/assign", a.assign)
					r.Post("/decision", a.decide)
				})
			})
			r.With(a.requireRole(domain.RoleUnderwriter)).Get("/stats", a.stats)
		})
	})

	if a.web != nil {
		r.NotFound(a.spa)
	}
	return r
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.store.DB.PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unavailable"})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

// spa раздаёт статические файлы интерфейса; неизвестные пути — index.html (клиентский роутинг).
func (a *API) spa(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Метод не найден", nil)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := a.web.Open(path); err == nil {
		st, _ := f.Stat()
		f.Close()
		if !st.IsDir() {
			if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, a.web, path)
			return
		}
	}
	http.ServeFileFS(w, r, a.web, "index.html")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

// cors — только для явно разрешённых источников (например, Swagger UI на соседнем порту).
func (a *API) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && a.corsOrigins[origin] {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// --- аутентификация ---

type ctxKey struct{}

func actorFrom(ctx context.Context) conveyor.Actor {
	a, _ := ctx.Value(ctxKey{}).(conveyor.Actor)
	return a
}

// authenticate проверяет JWT и загружает пользователя из БД: роль и компания берутся из БД,
// а не из токена, поэтому изменения прав вступают в силу сразу.
func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || token == "" {
			writeError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Требуется вход в систему", nil)
			return
		}
		claims, err := a.issuer.Parse(token)
		if err != nil {
			writeError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Сессия истекла, войдите заново", nil)
			return
		}
		u, err := a.store.UserByID(r.Context(), claims.Subject)
		if err != nil {
			writeError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Пользователь не найден", nil)
			return
		}
		actor := conveyor.Actor{ID: u.ID, Name: u.FullName, Type: domain.ActorClient}
		if u.Role == domain.RoleUnderwriter {
			actor.Type = domain.ActorUnderwriter
		}
		if u.CompanyID != nil {
			actor.CompanyID = *u.CompanyID
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, actor)))
	})
}

func (a *API) requireRole(role domain.Role) func(http.Handler) http.Handler {
	want := domain.ActorClient
	if role == domain.RoleUnderwriter {
		want = domain.ActorUnderwriter
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if actorFrom(r.Context()).Type != want {
				writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Действие недоступно для вашей роли", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// limiter — простой ограничитель попыток входа по IP (защита от перебора паролей).
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}
