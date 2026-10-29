package verification

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"time"
)

type Summary struct {
	ID         uuid.UUID  `json:"id"`
	Status     string     `json:"status"`
	Confidence *float64   `json:"confidence"`
	Reasons    []string   `json:"reasons"`
	Attempts   int        `json:"attempts"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Result     *Result    `json:"result,omitempty"`
}

func (s *Service) Summary(ctx context.Context, q db.Querier, driverID uuid.UUID, sensitive bool) (*Summary, error) {
	if !s.Enabled() {
		return nil, nil
	}
	var v Summary
	var reasons, encrypted []byte
	err := q.QueryRow(ctx, `SELECT id,status,confidence,reasons,attempts,created_at,finished_at,result_ciphertext FROM driver_verification_jobs WHERE driver_id=$1 ORDER BY created_at DESC LIMIT 1`, driverID).Scan(&v.ID, &v.Status, &v.Confidence, &reasons, &v.Attempts, &v.CreatedAt, &v.FinishedAt, &encrypted)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.Reasons = []string{}
	_ = json.Unmarshal(reasons, &v.Reasons)
	if sensitive && len(encrypted) > 0 {
		var result Result
		if err := s.open(encrypted, &result); err != nil {
			return nil, err
		}
		v.Result = &result
	}
	return &v, nil
}

// Enqueue snapshots only protected identity data. A full queue still records the
// received application and a durable explicit manual-review outcome.
func (s *Service) Enqueue(ctx context.Context, tx pgx.Tx, driverID, appID uuid.UUID, first, last string, docs []Document) error {
	if !s.Enabled() {
		return nil
	}
	in, version, err := s.LoadInput(ctx, tx, driverID)
	if err != nil {
		return err
	}
	if in == nil {
		in = &Input{}
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, db.LockKey("driver_ocr_admission")); err != nil {
		return err
	}
	b, err := s.seal(Snapshot{Input: *in, FirstName: first, LastName: last})
	if err != nil {
		return err
	}
	var n int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM driver_verification_jobs WHERE status IN ('pending','processing')`).Scan(&n); err != nil {
		return err
	}
	status := "pending"
	reasons := []string{}
	if n >= s.opt.QueueLimit {
		status = "admin_review"
		reasons = append(reasons, "queue_capacity")
	}
	if !in.Complete() {
		status = "admin_review"
		reasons = append(reasons, "identity_fields_missing")
	}
	_, err = tx.Exec(ctx, `INSERT INTO driver_verification_jobs(driver_id,application_id,input_version,input_ciphertext,documents,status,reasons,finished_at) VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $6='admin_review' THEN now() END) ON CONFLICT(application_id) DO NOTHING`, driverID, appID, version, b, docs, status, reasons)
	return err
}

type Job struct {
	ID, DriverID, ApplicationID, Token uuid.UUID
	Snapshot                           Snapshot
	InputVersion                       int64
	Documents                          []Document
	Attempts                           int
}

func (s *Service) claim(ctx context.Context) (*Job, error) {
	var j Job
	var b, docs []byte
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, db.LockKey("driver_ocr_claim")); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `WITH due AS (SELECT id FROM driver_verification_jobs WHERE ((status='pending' AND next_attempt_at<=now()) OR (status='processing' AND locked_until<now())) AND NOT EXISTS(SELECT 1 FROM driver_verification_jobs running WHERE running.status='processing' AND running.locked_until>=now()) ORDER BY next_attempt_at,created_at LIMIT 1 FOR UPDATE SKIP LOCKED) UPDATE driver_verification_jobs j SET status='processing',lease_token=gen_random_uuid(),locked_until=now()+interval '120 seconds',attempts=attempts+1,updated_at=now() FROM due WHERE j.id=due.id RETURNING j.id,j.driver_id,j.application_id,j.lease_token,j.input_version,j.input_ciphertext,j.documents,j.attempts`).Scan(&j.ID, &j.DriverID, &j.ApplicationID, &j.Token, &j.InputVersion, &b, &docs, &j.Attempts)
	})
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("verification: claim: %w", err)
	}
	if err = s.open(b, &j.Snapshot); err != nil {
		return &j, err
	}
	if err = json.Unmarshal(docs, &j.Documents); err != nil {
		return &j, fmt.Errorf("verification: invalid document references")
	}
	return &j, nil
}

type QueueStats struct {
	Pending       int     `json:"pending"`
	Processing    int     `json:"processing"`
	Review        int     `json:"admin_review"`
	OldestSeconds float64 `json:"oldest_pending_s"`
}

func (s *Service) QueueStats(ctx context.Context) (QueueStats, error) {
	var v QueueStats
	if !s.Enabled() {
		return v, nil
	}
	err := s.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='processing'),count(*) FILTER(WHERE status='admin_review'),COALESCE(extract(epoch FROM now()-min(created_at) FILTER(WHERE status='pending')),0) FROM driver_verification_jobs`).Scan(&v.Pending, &v.Processing, &v.Review, &v.OldestSeconds)
	return v, err
}

// ApplicationInput is the submitted immutable identity, not a newer draft.
func (s *Service) ApplicationInput(ctx context.Context, q db.Querier, appID uuid.UUID) (*Input, error) {
	if !s.Enabled() {
		return nil, nil
	}
	var b []byte
	err := q.QueryRow(ctx, `SELECT input_ciphertext FROM driver_verification_jobs WHERE application_id=$1`, appID).Scan(&b)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v Snapshot
	if err = s.open(b, &v); err != nil {
		return nil, err
	}
	return &v.Input, nil
}

func (s *Service) notifyReview(ctx context.Context, q db.Querier, driverID, appID uuid.UUID) error {
	var user uuid.UUID
	if err := q.QueryRow(ctx, `SELECT user_id FROM driver_profiles WHERE id=$1`, driverID).Scan(&user); err != nil {
		return err
	}
	return notifications.Enqueue(ctx, q, notifications.Notification{Bot: notifications.DriverBot, UserID: user, Template: notifications.TplModerationReviewRequired, DedupeKey: "moderation_review_required:" + appID.String()})
}
func (s *Service) History(ctx context.Context, q db.Querier, driverID uuid.UUID) ([]Summary, error) {
	rows, err := q.Query(ctx, `SELECT id,status,confidence,reasons,attempts,created_at,finished_at FROM driver_verification_jobs WHERE driver_id=$1 ORDER BY created_at DESC LIMIT 20`, driverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var v Summary
		var reasons []byte
		if err := rows.Scan(&v.ID, &v.Status, &v.Confidence, &reasons, &v.Attempts, &v.CreatedAt, &v.FinishedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(reasons, &v.Reasons)
		out = append(out, v)
	}
	return out, rows.Err()
}

// NotifyReviewIfNeeded is called after the submission acknowledgment is queued.
func (s *Service) NotifyReviewIfNeeded(ctx context.Context, q db.Querier, driverID, appID uuid.UUID) error {
	if !s.Enabled() {
		return nil
	}
	var review bool
	if err := q.QueryRow(ctx, `SELECT status='admin_review' FROM driver_verification_jobs WHERE application_id=$1`, appID).Scan(&review); err != nil {
		return err
	}
	if review {
		return s.notifyReview(ctx, q, driverID, appID)
	}
	return nil
}

// AdminDecision invalidates pending work in the moderation transaction. An OCR
// process already running may finish, but its cleared lease cannot apply.
func (s *Service) AdminDecision(ctx context.Context, tx pgx.Tx, driverID uuid.UUID) error {
	if !s.Enabled() {
		return nil
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM driver_profiles WHERE id=$1`, driverID).Scan(&status); err != nil {
		return err
	}
	if status == "pending" {
		return nil
	}
	next := "superseded"
	if status == "approved" {
		next = "approved"
	}
	_, err := tx.Exec(ctx, `UPDATE driver_verification_jobs SET status=$2,lease_token=NULL,locked_until=NULL,finished_at=COALESCE(finished_at,now()),updated_at=now() WHERE driver_id=$1 AND application_id=(SELECT id FROM driver_applications WHERE driver_id=$1 ORDER BY round DESC LIMIT 1) AND status IN ('pending','processing','matched','admin_review')`, driverID, next)
	return err
}
