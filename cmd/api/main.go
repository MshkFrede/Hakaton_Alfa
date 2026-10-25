// Сервис кредитного конвейера: REST API, веб-интерфейс и фоновый обработчик заявок.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"credit-conveyor/api"
	"credit-conveyor/internal/auth"
	"credit-conveyor/internal/config"
	"credit-conveyor/internal/conveyor"
	"credit-conveyor/internal/httpapi"
	"credit-conveyor/internal/integrations"
	"credit-conveyor/internal/seed"
	"credit-conveyor/internal/store"
	"credit-conveyor/internal/worker"
	"credit-conveyor/web"
)

func main() {
	cfg := config.LoadAPI()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})).With("service", "credit-api")
	if cfg.JWTSecret == "dev-only-change-me" {
		log.Warn("JWT_SECRET is not set — using insecure development default")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := connect(ctx, cfg.DatabaseURL, log)
	if err != nil {
		log.Error("database unavailable", "err", err)
		os.Exit(1)
	}
	if err := st.Migrate(ctx); err != nil {
		log.Error("migrations failed", "err", err)
		os.Exit(1)
	}
	if cfg.SeedDemoData {
		if created, err := seed.Run(ctx, st); err != nil {
			log.Error("seed failed", "err", err)
			os.Exit(1)
		} else if created {
			log.Info("demo data created", "password", seed.DemoPassword)
		}
	}

	sources := integrations.NewClient(cfg.MocksBaseURL, cfg.IntegrationTimeout, cfg.IntegrationRetries)
	svc := conveyor.New(st, sources, cfg.StepDelay, log)
	apiHandler := httpapi.New(st, svc, auth.NewIssuer(cfg.JWTSecret, cfg.JWTTTL), log, httpapi.Options{
		Web:            web.FS(),
		OpenAPI:        api.OpenAPI,
		CORSOrigins:    strings.Split(os.Getenv("CORS_ORIGINS"), ","),
		MaxUploadBytes: cfg.MaxUploadBytes,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           apiHandler.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		worker.New(st, svc, log.With("component", "worker"), cfg.WorkerPollInterval, cfg.WorkerConcurrency).Run(ctx)
	}()
	go func() {
		log.Info("http listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	wg.Wait()
	_ = st.DB.Close()
	log.Info("stopped")
}

// connect ждёт готовности БД (в docker compose БД может стартовать позже сервиса).
func connect(ctx context.Context, dsn string, log *slog.Logger) (*store.Store, error) {
	var lastErr error
	for i := 0; i < 30; i++ {
		st, err := store.Open(ctx, dsn)
		if err == nil {
			return st, nil
		}
		lastErr = err
		log.Warn("waiting for database", "attempt", i+1, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, lastErr
}
