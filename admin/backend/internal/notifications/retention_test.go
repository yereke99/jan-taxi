package notifications

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestPruneOutbox(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, _ := newUser(t, pool, "")

	// age sets the row's activity timestamps days in the past.
	age := func(id uuid.UUID, status string, createdDays, activityDays int, sent bool) {
		t.Helper()
		_, err := pool.Exec(ctx, `
			UPDATE notification_outbox
			SET status = $2,
			    created_at = now() - make_interval(days => $3),
			    next_attempt_at = now() - make_interval(days => $4),
			    sent_at = CASE WHEN $5 THEN now() - make_interval(days => $4) END
			WHERE id = $1`, id, status, createdDays, activityDays, sent)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO notification_attempts (outbox_id, attempt, started_at, outcome)
			VALUES ($1, 1, now(), 'sent')`, id); err != nil {
			t.Fatal(err)
		}
	}

	oldSent := enqueue(t, pool, user, "prune:old-sent")
	age(oldSent, StatusSent, 40, 40, true)
	oldDead := enqueue(t, pool, user, "prune:old-dead")
	age(oldDead, StatusDead, 35, 35, false)
	oldCancelled := enqueue(t, pool, user, "prune:old-cancelled")
	age(oldCancelled, StatusCancelled, 31, 31, false)
	recentSent := enqueue(t, pool, user, "prune:recent-sent")
	age(recentSent, StatusSent, 5, 5, true)
	// Old but re-queued by an administrator 2 days ago, then delivered.
	retried := enqueue(t, pool, user, "prune:retried")
	age(retried, StatusSent, 45, 2, true)
	// Unfinished rows are never deleted, however old.
	oldPending := enqueue(t, pool, user, "prune:old-pending")
	age(oldPending, StatusPending, 60, 60, false)
	oldSending := enqueue(t, pool, user, "prune:old-sending")
	age(oldSending, StatusSending, 60, 60, false)

	n, err := PruneOutbox(ctx, pool, OutboxRetention)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("deleted %d rows, want 3", n)
	}
	exists := func(id uuid.UUID) bool {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notification_outbox WHERE id = $1)`, id).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	for _, id := range []uuid.UUID{oldSent, oldDead, oldCancelled} {
		if exists(id) {
			t.Errorf("row %s should be deleted", id)
		}
	}
	for _, id := range []uuid.UUID{recentSent, retried, oldPending, oldSending} {
		if !exists(id) {
			t.Errorf("row %s should be kept", id)
		}
	}
	// Attempts of deleted rows went with them (ON DELETE CASCADE).
	var orphans, kept int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE o.id IS NULL), count(*) FILTER (WHERE o.id IS NOT NULL)
		FROM notification_attempts a LEFT JOIN notification_outbox o ON o.id = a.outbox_id`).Scan(&orphans, &kept); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 || kept != 4 {
		t.Fatalf("attempts: orphans=%d kept=%d", orphans, kept)
	}

	// A second run has nothing left to do; a non-positive retention is refused.
	if n, err := PruneOutbox(ctx, pool, OutboxRetention); err != nil || n != 0 {
		t.Fatalf("second run: %d %v", n, err)
	}
	if _, err := PruneOutbox(ctx, pool, 0); err == nil {
		t.Fatal("zero retention must be rejected")
	}
}
