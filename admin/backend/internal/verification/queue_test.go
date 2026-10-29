package verification

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func enqueueFixture(t *testing.T, s *Service) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	d := testutil.CreateDriver(t, s.pool, testutil.DriverOptions{Status: "pending"})
	var app uuid.UUID
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.SaveInput(ctx, tx, d.DriverID, identityFixture().Input); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO driver_applications(driver_id,round,status,snapshot) VALUES($1,1,'pending','{}') RETURNING id`, d.DriverID).Scan(&app); err != nil {
			return err
		}
		return s.Enqueue(ctx, tx, d.DriverID, app, identityFixture().FirstName, identityFixture().LastName, []Document{})
	})
	if err != nil {
		t.Fatal(err)
	}
	return d.DriverID, app
}
func TestQueueLeaseFencingAdminFinalityAndIdentityOwnership(t *testing.T) {
	s := testService(t)
	s.pool = testutil.NewDB(t)
	driver, app := enqueueFixture(t, s)
	ctx := context.Background()
	first, err := s.claim(ctx)
	if err != nil || first == nil {
		t.Fatal("claim", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE driver_verification_jobs SET locked_until=now()-interval '1 second' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := s.claim(ctx)
	if err != nil || second == nil || first.Token == second.Token {
		t.Fatal("crash lease not recovered", err)
	}
	calls := 0
	approve := func(ctx context.Context, tx pgx.Tx, d, a uuid.UUID) error {
		calls++
		_, err := tx.Exec(ctx, `UPDATE driver_profiles SET status='approved' WHERE id=$1`, d)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE driver_applications SET status='approved' WHERE id=$1`, a)
		return err
	}
	result := Assess(highConfidenceResult(), identityFixture(), 98)
	if err = s.finish(ctx, *first, result, approve); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("stale lease approved")
	}
	if err = s.finish(ctx, *second, result, approve); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("approval missing")
	}
	if err = s.finish(ctx, *second, result, approve); err != nil || calls != 1 {
		t.Fatal("duplicate completion approved twice")
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error { return s.Claim(ctx, tx, driver, app, identityFixture().Input) })
	if err != nil {
		t.Fatal("same owner replay failed", err)
	}
	other, otherApp := enqueueFixture(t, s)
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error { return s.Claim(ctx, tx, other, otherApp, identityFixture().Input) })
	if !errors.Is(err, ErrConflict) {
		t.Fatal("another owner reused identity", err)
	}
	// An administrator's decision wins even if OCR finished successfully later.
	job, err := s.claim(ctx)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE driver_profiles SET status='rejected' WHERE id=$1`, other); err != nil {
		t.Fatal(err)
	}
	if err = s.finish(ctx, *job, result, approve); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = s.pool.QueryRow(ctx, `SELECT status FROM driver_verification_jobs WHERE id=$1`, job.ID).Scan(&status); err != nil || status != "superseded" {
		t.Fatal("admin decision overwritten", err)
	}
}

type failingEngine struct{}

func (failingEngine) Read(context.Context, Job) (Result, error) {
	return Result{}, errors.New("private raw output must never be stored")
}
func TestQueueRetryExhaustionAndBackpressure(t *testing.T) {
	s := testService(t)
	s.pool = testutil.NewDB(t)
	s.opt.QueueLimit = 1
	_, _ = enqueueFixture(t, s)
	_, app := enqueueFixture(t, s)
	ctx := context.Background()
	var status string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM driver_verification_jobs WHERE application_id=$1`, app).Scan(&status); err != nil || status != "admin_review" {
		t.Fatal("backpressure discarded application", err)
	}
	did, err := s.RunOnce(ctx, failingEngine{}, nil, WorkerOptions{Timeout: time.Second, MaxAttempts: 1})
	if err != nil || !did {
		t.Fatal(err)
	}
	var n int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM driver_verification_jobs WHERE status='admin_review' AND reasons::text NOT LIKE '%private raw%'`).Scan(&n); err != nil || n != 2 {
		t.Fatal("retry exhausted without durable manual review", err)
	}
}

type boundedEngine struct{ active, peak atomic.Int32 }

func (e *boundedEngine) Read(ctx context.Context, j Job) (Result, error) {
	n := e.active.Add(1)
	defer e.active.Add(-1)
	for old := e.peak.Load(); n > old && !e.peak.CompareAndSwap(old, n); old = e.peak.Load() {
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return highConfidenceResult(), nil
	}
}
func TestTwentyJobsAcrossConcurrentWorkersStayBounded(t *testing.T) {
	s := testService(t)
	s.pool = testutil.NewDB(t)
	s.opt.AutoApprove = false
	for range 20 {
		enqueueFixture(t, s)
	}
	engine := &boundedEngine{}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				did, err := s.RunOnce(ctx, engine, nil, WorkerOptions{})
				if err != nil {
					errs <- err
					return
				}
				if !did {
					var n int
					if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM driver_verification_jobs WHERE status IN ('pending','processing')`).Scan(&n); err != nil {
						errs <- err
						return
					}
					if n == 0 {
						return
					}
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if engine.peak.Load() != 1 {
		t.Fatalf("global concurrency %d", engine.peak.Load())
	}
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM driver_verification_jobs WHERE status='matched'`).Scan(&n); err != nil || n != 20 {
		t.Fatalf("lost jobs: %d %v", n, err)
	}
}
