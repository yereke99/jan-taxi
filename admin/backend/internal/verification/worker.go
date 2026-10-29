package verification

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"time"
)

type ApproveFunc func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error
type WorkerOptions struct {
	Timeout      time.Duration
	PollInterval time.Duration
	MaxAttempts  int
}

func (s *Service) Run(ctx context.Context, engine Engine, approve ApproveFunc, opt WorkerOptions) error {
	if !s.Enabled() {
		return errors.New("verification: worker is disabled")
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 60 * time.Second
	}
	if opt.Timeout > 90*time.Second {
		return errors.New("verification: timeout must be <= 90 seconds")
	}
	if opt.PollInterval <= 0 {
		opt.PollInterval = 2 * time.Second
	}
	if opt.MaxAttempts <= 0 {
		opt.MaxAttempts = 3
	}
	// A session lock bounds global worker concurrency, including accidental
	// duplicate deployments. Per-job lease tokens also fence stale completions.
	for ctx.Err() == nil {
		_, err := db.RunExclusive(ctx, s.pool, "driver_ocr_worker", func(lockCtx context.Context) error {
			s.opt.Log.Info("OCR worker lease acquired")
			nextReport := time.Time{}
			for lockCtx.Err() == nil {
				if time.Now().After(nextReport) {
					if stats, err := s.QueueStats(lockCtx); err == nil {
						s.opt.Log.Info("OCR queue", "pending", stats.Pending, "processing", stats.Processing, "admin_review", stats.Review, "oldest_pending_s", stats.OldestSeconds)
					}
					nextReport = time.Now().Add(time.Minute)
				}
				did, err := s.RunOnce(lockCtx, engine, approve, opt)
				if err != nil {
					return err
				}
				if !did {
					select {
					case <-lockCtx.Done():
						return nil
					case <-time.After(opt.PollInterval):
					}
				}
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(opt.PollInterval):
		}
	}
	return nil
}
func (s *Service) RunOnce(ctx context.Context, engine Engine, approve ApproveFunc, opt WorkerOptions) (bool, error) {
	j, err := s.claim(ctx)
	if j == nil {
		return false, err
	}
	started := time.Now()
	log := s.opt.Log.With("job_id", j.ID, "application_id", j.ApplicationID, "attempt", j.Attempts)
	log.Info("OCR job started", "document_slots", len(j.Documents))
	if opt.MaxAttempts <= 0 {
		opt.MaxAttempts = 3
	}
	if opt.Timeout <= 0 || opt.Timeout > 90*time.Second {
		opt.Timeout = 60 * time.Second
	}
	if err == nil && j.Attempts <= opt.MaxAttempts {
		work, cancel := context.WithTimeout(ctx, opt.Timeout)
		var r Result
		r, err = engine.Read(work, *j)
		cancel()
		if err == nil {
			r = Assess(r, j.Snapshot, s.opt.MinConfidence)
			err = s.finish(ctx, *j, r, approve)
			if err != nil {
				log.Error("OCR result could not be committed", "duration_ms", time.Since(started).Milliseconds())
			}
			return true, err
		}
	}
	// Errors deliberately contain no engine output, identity values or raw file
	// contents. A cancelled process releases its job for restart recovery.
	clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	status := "pending"
	reason := "processing_failed"
	delay := time.Duration(1<<min(j.Attempts, 8)) * 15 * time.Second
	if j.Attempts >= opt.MaxAttempts {
		status = "admin_review"
	}
	if ctx.Err() != nil {
		delay = 0
		reason = "processing_interrupted"
	}
	stage, code, exit := failureDetails(err)
	updated := false
	err = db.WithTx(clean, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(clean, `UPDATE driver_verification_jobs SET status=$3,reasons=jsonb_build_array($4::text),next_attempt_at=now()+$5*interval '1 second',locked_until=NULL,lease_token=NULL,finished_at=CASE WHEN $3='admin_review' THEN now() END,updated_at=now(),attempts=attempts-CASE WHEN $6 THEN 1 ELSE 0 END WHERE id=$1 AND lease_token=$2 AND status='processing'`, j.ID, j.Token, status, reason, delay.Seconds(), ctx.Err() != nil)
		updated = err == nil && tag.RowsAffected() > 0
		if updated && status == "admin_review" {
			return s.notifyReview(clean, tx, j.DriverID, j.ApplicationID)
		}
		return err
	})
	if err == nil && updated {
		log.Warn("OCR processing failed", "status", status, "reason", reason, "failure_stage", stage, "failure_code", code, "exit_code", exit, "retry_in_s", delay.Seconds(), "duration_ms", time.Since(started).Milliseconds())
	}
	return true, err
}
func (s *Service) finish(ctx context.Context, j Job, r Result, approve ApproveFunc) error {
	encrypted, err := s.seal(r)
	if err != nil {
		return err
	}
	outcome := ""
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		// Match moderation's lock order: driver, application, then job. Never let a
		// delayed OCR result replace a newer resubmission or an admin decision.
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM driver_profiles WHERE id=$1 FOR UPDATE`, j.DriverID).Scan(&status); err != nil {
			return err
		}
		var latest uuid.UUID
		var appStatus string
		if err := tx.QueryRow(ctx, `SELECT id,status FROM driver_applications WHERE driver_id=$1 ORDER BY round DESC LIMIT 1 FOR UPDATE`, j.DriverID).Scan(&latest, &appStatus); err != nil {
			return err
		}
		var token *uuid.UUID
		var jobStatus string
		if err := tx.QueryRow(ctx, `SELECT lease_token,status FROM driver_verification_jobs WHERE id=$1 FOR UPDATE`, j.ID).Scan(&token, &jobStatus); err != nil {
			return err
		}
		if token == nil || *token != j.Token || jobStatus != "processing" {
			return nil
		}
		var version int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT version FROM driver_identity_inputs WHERE driver_id=$1),0)`, j.DriverID).Scan(&version); err != nil {
			return err
		}
		next := "admin_review"
		if status != "pending" || appStatus != "pending" || latest != j.ApplicationID || version != j.InputVersion {
			next = "superseded"
		}
		if next != "superseded" && len(r.Reasons) == 0 {
			next = "matched"
			if s.opt.AutoApprove && approve != nil {
				// Claims and financial effects are atomic. A conflict is an explicit
				// review result and rolls back any first claim made in the savepoint.
				nested, err := tx.Begin(ctx)
				if err != nil {
					return err
				}
				err = s.Claim(ctx, nested, j.DriverID, j.ApplicationID, j.Snapshot.Input)
				if err == nil {
					err = approve(ctx, nested, j.DriverID, j.ApplicationID)
				}
				if err != nil {
					_ = nested.Rollback(ctx)
					next = "admin_review"
					r.Reasons = append(r.Reasons, "approval_requires_review")
					encrypted, err = s.seal(r)
					if err != nil {
						return err
					}
				} else {
					if err = nested.Commit(ctx); err != nil {
						return err
					}
					next = "approved"
				}
			}
		}
		_, err := tx.Exec(ctx, `UPDATE driver_verification_jobs SET status=$3,result_ciphertext=$4,confidence=$5,reasons=$6,lease_token=NULL,locked_until=NULL,finished_at=now(),updated_at=now() WHERE id=$1 AND lease_token=$2`, j.ID, j.Token, next, encrypted, r.Confidence, r.Reasons)
		if err != nil {
			return err
		}
		if next == "admin_review" {
			if err := s.notifyReview(ctx, tx, j.DriverID, j.ApplicationID); err != nil {
				return err
			}
		}
		if err = audit.Log(ctx, tx, nil, audit.Entry{Action: "driver.verification_completed", EntityType: "driver", EntityID: j.DriverID.String(), After: map[string]any{"application_id": j.ApplicationID, "status": next, "confidence": r.Confidence, "reasons": r.Reasons}}); err != nil {
			return err
		}
		outcome = next
		return realtime.Publish(ctx, tx, realtime.Event{Type: "driver.verification_updated", Scopes: []string{realtime.DriverScope(j.DriverID), realtime.AdminScope}, Data: map[string]any{"driver_id": j.DriverID, "application_id": j.ApplicationID, "status": next}})
	})
	if err == nil {
		if outcome == "" {
			s.opt.Log.Info("OCR result ignored", "job_id", j.ID, "reason", "lease_changed")
		} else {
			s.opt.Log.Info("OCR job completed", "job_id", j.ID, "application_id", j.ApplicationID, "status", outcome, "confidence", r.Confidence, "reasons", r.Reasons, "engine", r.Engine, assessmentLog(r))
		}
	}
	return err
}
