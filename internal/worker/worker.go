// Package worker — фоновый обработчик очереди задач конвейера.
// Несколько горутин (и несколько реплик сервиса) безопасно разбирают очередь
// благодаря FOR UPDATE SKIP LOCKED.
package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"credit-conveyor/internal/conveyor"
	"credit-conveyor/internal/store"
)

type Worker struct {
	store       *store.Store
	svc         *conveyor.Service
	log         *slog.Logger
	poll        time.Duration
	concurrency int
}

func New(st *store.Store, svc *conveyor.Service, log *slog.Logger, poll time.Duration, concurrency int) *Worker {
	return &Worker{store: st, svc: svc, log: log, poll: poll, concurrency: max(1, concurrency)}
}

// Run блокируется до отмены ctx и дожидается завершения начатых задач (graceful shutdown).
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < w.concurrency; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			w.loop(ctx, n)
		}(i)
	}
	wg.Add(1)
	go func() { // восстановление задач, «зависших» после падения процесса
		defer wg.Done()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := w.store.RecoverStaleJobs(ctx, 2*time.Minute); err == nil && n > 0 {
					w.log.Warn("recovered stale jobs", "count", n)
				}
			}
		}
	}()
	wg.Wait()
}

func (w *Worker) loop(ctx context.Context, n int) {
	for {
		if ctx.Err() != nil {
			return
		}
		job, err := w.store.ClaimJob(ctx)
		if err != nil {
			if ctx.Err() == nil {
				w.log.Error("claim job", "err", err)
			}
			sleep(ctx, w.poll*4)
			continue
		}
		if job == nil {
			sleep(ctx, w.poll)
			continue
		}
		w.process(job)
	}
}

// process выполняет задачу с собственным таймаутом: остановка сервиса не обрывает начатый шаг.
func (w *Worker) process(job *store.Job) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	log := w.log.With("job_id", job.ID, "kind", job.Kind, "application_id", job.ApplicationID, "attempt", job.Attempts)
	if err := w.svc.HandleJob(ctx, job.ApplicationID, job.Kind); err != nil {
		log.Error("job failed", "err", err)
		if ferr := w.store.FailJob(ctx, job, err); ferr != nil {
			log.Error("mark job failed", "err", ferr)
		}
		return
	}
	if err := w.store.CompleteJob(ctx, job.ID); err != nil {
		log.Error("complete job", "err", err)
		return
	}
	log.Info("job done", "duration_ms", time.Since(start).Milliseconds())
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
