// Package config читает настройки из переменных окружения (12-factor).
package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

type API struct {
	HTTPAddr           string
	DatabaseURL        string
	JWTSecret          string
	JWTTTL             time.Duration
	MocksBaseURL       string
	IntegrationTimeout time.Duration
	IntegrationRetries int
	StepDelay          time.Duration // искусственная пауза между этапами обработки (для наглядности демо)
	WorkerPollInterval time.Duration
	WorkerConcurrency  int
	SeedDemoData       bool
	LogLevel           slog.Level
	MaxUploadBytes     int64
}

func LoadAPI() API {
	return API{
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		DatabaseURL:        env("DATABASE_URL", "postgres://credit:credit@localhost:5432/credit?sslmode=disable"),
		JWTSecret:          env("JWT_SECRET", "dev-only-change-me"),
		JWTTTL:             dur("JWT_TTL", 8*time.Hour),
		MocksBaseURL:       env("MOCKS_BASE_URL", "http://localhost:8090"),
		IntegrationTimeout: dur("INTEGRATION_TIMEOUT", 3*time.Second),
		IntegrationRetries: num("INTEGRATION_RETRIES", 2),
		StepDelay:          dur("PROCESSING_STEP_DELAY", 2*time.Second),
		WorkerPollInterval: dur("WORKER_POLL_INTERVAL", 500*time.Millisecond),
		WorkerConcurrency:  num("WORKER_CONCURRENCY", 4),
		SeedDemoData:       env("SEED_DEMO_DATA", "true") == "true",
		LogLevel:           level(env("LOG_LEVEL", "info")),
		MaxUploadBytes:     int64(num("MAX_UPLOAD_MB", 10)) << 20,
	}
}

type Mocks struct {
	HTTPAddr string
	Latency  time.Duration
	Hang     time.Duration
}

func LoadMocks() Mocks {
	return Mocks{
		HTTPAddr: env("HTTP_ADDR", ":8090"),
		Latency:  dur("MOCK_LATENCY", 400*time.Millisecond),
		Hang:     dur("MOCK_HANG", 15*time.Second),
	}
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func dur(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(env(k, "")); err == nil {
		return v
	}
	return def
}

func num(k string, def int) int {
	if v, err := strconv.Atoi(env(k, "")); err == nil {
		return v
	}
	return def
}

func level(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return l
}
