package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

type job struct {
	name  string
	every time.Duration
	fn    func(ctx context.Context) error
}

type worker struct {
	name string
	fn   func(ctx context.Context) error
}

// AddJob registers a periodic background job. Each run takes the
// PostgreSQL advisory lock for name (db.RunExclusive), so with several API
// replicas only one runs it at a time. Runs are spaced by every plus a small
// random jitter; a panicking or failing run is logged and the schedule
// continues. Jobs only run when JOBS_ENABLED is true. Must be called before Run.
func (a *App) AddJob(name string, every time.Duration, fn func(ctx context.Context) error) {
	if err := a.checkBackground(name, fn); err != nil {
		a.wireErrs = append(a.wireErrs, fmt.Errorf("AddJob %q: %w", name, err))
		return
	}
	if every <= 0 {
		a.wireErrs = append(a.wireErrs, fmt.Errorf("AddJob %q: interval must be positive", name))
		return
	}
	a.jobs = append(a.jobs, job{name: name, every: every, fn: fn})
}

// AddWorker registers a long-running background loop (e.g. the realtime
// LISTEN connection). fn should block until ctx is cancelled; if it returns
// early or panics it is restarted with exponential backoff (1s to 30s).
// Workers run regardless of JOBS_ENABLED. Must be called before Run.
func (a *App) AddWorker(name string, fn func(ctx context.Context) error) {
	if err := a.checkBackground(name, fn); err != nil {
		a.wireErrs = append(a.wireErrs, fmt.Errorf("AddWorker %q: %w", name, err))
		return
	}
	a.workers = append(a.workers, worker{name: name, fn: fn})
}

func (a *App) checkBackground(name string, fn func(context.Context) error) error {
	switch {
	case a.started.Load():
		return errors.New("registered after Run started")
	case name == "" || fn == nil:
		return errors.New("name and fn are required")
	}
	for _, j := range a.jobs {
		if j.name == name {
			return errors.New("duplicate name")
		}
	}
	for _, w := range a.workers {
		if w.name == name {
			return errors.New("duplicate name")
		}
	}
	return nil
}

func (a *App) runJob(ctx context.Context, j job) {
	log := a.Log.With(slog.String("job", j.name))
	timer := time.NewTimer(firstDelay(j.every))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		a.runJobOnce(ctx, j, log)
		timer.Reset(nextDelay(j.every))
	}
}

func (a *App) runJobOnce(ctx context.Context, j job, log *slog.Logger) {
	start := time.Now()
	defer func() {
		if p := recover(); p != nil {
			log.Error("job panicked", slog.Any("panic", p), slog.String("stack", string(debug.Stack())))
		}
	}()
	runCtx, cancel := context.WithTimeout(ctx, jobTimeout(j.every))
	defer cancel()
	ran, err := db.RunExclusive(runCtx, a.Pool, j.name, j.fn)
	dur := time.Since(start)
	switch {
	case err != nil && ctx.Err() == nil:
		log.Error("job failed", slog.Any("err", err), slog.Duration("dur", dur))
	case ran && dur > j.every && j.every >= time.Second:
		log.Warn("job took longer than its interval", slog.Duration("dur", dur), slog.Duration("every", j.every))
	}
}

func (a *App) runWorker(ctx context.Context, w worker) {
	log := a.Log.With(slog.String("worker", w.name))
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := runRecovered(ctx, w.fn)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		log.Error("worker stopped; restarting", slog.Any("err", err), slog.Duration("backoff", backoff))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func runRecovered(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
	}()
	if err := fn(ctx); err != nil {
		return err
	}
	return errors.New("returned before shutdown")
}

// firstDelay staggers job start-up so replicas and jobs do not fire together.
func firstDelay(every time.Duration) time.Duration {
	return time.Duration(rand.Int64N(int64(min(every, 5*time.Second)) + 1))
}

// nextDelay adds up to 10% jitter (at most one minute) to the interval.
func nextDelay(every time.Duration) time.Duration {
	j := min(every/10, time.Minute)
	if j <= 0 {
		return every
	}
	return every + time.Duration(rand.Int64N(int64(j)+1))
}

// jobTimeout bounds one run: ten intervals, between one and ten minutes.
func jobTimeout(every time.Duration) time.Duration {
	return min(max(every*10, time.Minute), 10*time.Minute)
}

// addPlatformJobs registers jobs owned by the platform layer.
func (a *App) addPlatformJobs() {
	a.AddJob("file_orphan_cleanup", 6*time.Hour, func(ctx context.Context) error {
		n, err := a.Store.RemoveOrphans(ctx, a.Pool, 24*time.Hour)
		if n > 0 {
			a.Log.Info("removed orphaned files", slog.Int("count", n))
		}
		return err
	})
}
