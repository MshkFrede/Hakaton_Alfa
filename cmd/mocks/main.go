// Сервис-симулятор внешних источников: ЕГРЮЛ/ЕГРИП, БКИ, ФНС «Прозрачный бизнес».
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"credit-conveyor/internal/config"
	"credit-conveyor/internal/mocks"
)

func main() {
	cfg := config.LoadMocks()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "mocks")
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mocks.NewServer(cfg.Latency, cfg.Hang, log).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("mocks listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("mocks stopped")
}
