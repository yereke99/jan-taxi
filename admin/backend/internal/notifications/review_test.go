package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

type queuedReview struct {
	driver   testutil.DriverFixture
	appID    uuid.UUID
	jobID    uuid.UUID
	outboxID uuid.UUID
}

func enqueueReview(t *testing.T, pool *pgxpool.Pool) queuedReview {
	t.Helper()
	ctx := context.Background()
	q := queuedReview{driver: testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "pending"})}
	if err := pool.QueryRow(ctx, `INSERT INTO driver_applications(driver_id,round,status,snapshot)
		VALUES($1,1,'pending','{}') RETURNING id`, q.driver.DriverID).Scan(&q.appID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO driver_verification_jobs(driver_id,application_id,input_version,input_ciphertext,documents,status)
		VALUES($1,$2,1,$3,'[]','admin_review') RETURNING id`, q.driver.DriverID, q.appID, []byte{1}).Scan(&q.jobID); err != nil {
		t.Fatal(err)
	}
	key := "moderation_review_required:" + q.appID.String()
	if err := Enqueue(ctx, pool, Notification{Bot: DriverBot, UserID: q.driver.UserID, Template: TplModerationReviewRequired, DedupeKey: key}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM notification_outbox WHERE dedupe_key=$1`, key).Scan(&q.outboxID); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestReviewNotificationFollowsLatestPendingApplication(t *testing.T) {
	for _, state := range []string{"current", "approved", "rejected", "changes_requested", "suspended", "new_round", "job_matched", "wrong_recipient", "wrong_application"} {
		t.Run(state, func(t *testing.T) {
			pool := testutil.NewDB(t)
			ctx := context.Background()
			q := enqueueReview(t, pool)
			var err error
			switch state {
			case "approved", "rejected", "changes_requested":
				if _, err = pool.Exec(ctx, `UPDATE driver_applications SET status=$2 WHERE id=$1`, q.appID, state); err == nil {
					_, err = pool.Exec(ctx, `UPDATE driver_profiles SET status=$2 WHERE id=$1`, q.driver.DriverID, state)
				}
			case "suspended":
				_, err = pool.Exec(ctx, `UPDATE driver_profiles SET status='suspended' WHERE id=$1`, q.driver.DriverID)
			case "new_round":
				if _, err = pool.Exec(ctx, `UPDATE driver_applications SET status='changes_requested' WHERE id=$1`, q.appID); err == nil {
					_, err = pool.Exec(ctx, `INSERT INTO driver_applications(driver_id,round,status,snapshot) VALUES($1,2,'pending','{}')`, q.driver.DriverID)
				}
			case "job_matched":
				_, err = pool.Exec(ctx, `UPDATE driver_verification_jobs SET status='matched' WHERE id=$1`, q.jobID)
			case "wrong_recipient":
				other, tg := testutil.CreateUser(t, pool)
				_, err = pool.Exec(ctx, `UPDATE notification_outbox SET user_id=$2,chat_id=$3 WHERE id=$1`, q.outboxID, other, tg)
			case "wrong_application":
				_, err = pool.Exec(ctx, `UPDATE notification_outbox SET dedupe_key=$2 WHERE id=$1`, q.outboxID, "moderation_review_required:"+uuid.NewString())
			}
			if err != nil {
				t.Fatal(err)
			}
			fs := newFakeSender()
			if n, err := testWorker(pool, fs, "review-current").RunOnce(ctx); err != nil || n != 1 {
				t.Fatalf("RunOnce = %d, %v", n, err)
			}
			want := StatusCancelled
			if state == "current" {
				want = StatusSent
			}
			if got := readOutbox(t, pool, q.outboxID).status; got != want {
				t.Fatalf("outbox status = %s, want %s", got, want)
			}
			if sent := len(fs.sentTo(q.driver.TelegramID)); (sent == 1) != (state == "current") {
				t.Fatalf("delivered %d review notifications in state %s", sent, state)
			}
		})
	}
}

func TestReviewNotificationRetryDoesNotOutliveApproval(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	q := enqueueReview(t, pool)
	fs := newFakeSender()
	fs.fail(q.driver.TelegramID, apiErr(500, "upstream unavailable", 0))
	w := testWorker(pool, fs, "review-retry")
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if readOutbox(t, pool, q.outboxID).status != StatusPending {
		t.Fatal("transient failure did not remain retryable")
	}
	if _, err := pool.Exec(ctx, `UPDATE driver_profiles SET status='approved' WHERE id=$1`, q.driver.DriverID); err != nil {
		t.Fatal(err)
	}
	makeDue(t, pool, q.outboxID)
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if readOutbox(t, pool, q.outboxID).status != StatusCancelled || len(fs.sentTo(q.driver.TelegramID)) != 0 {
		t.Fatal("stale review message was delivered after approval")
	}
}

func TestReviewNotificationSendSerializesModerationDecision(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	q := enqueueReview(t, pool)
	sender := heldContactSender{entered: make(chan struct{}), release: make(chan struct{})}
	w := testWorker(pool, sender, "review-race")
	done := make(chan error, 1)
	go func() { _, err := w.RunOnce(ctx); done <- err }()
	select {
	case <-sender.entered:
	case <-time.After(time.Second):
		t.Fatal("review send did not begin")
	}
	bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := pool.Exec(bounded, `UPDATE driver_profiles SET status='approved' WHERE id=$1`, q.driver.DriverID)
	close(sender.release)
	if err == nil {
		t.Error("moderation decision committed between review eligibility and send")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("review send retained its profile lock")
	}
}
