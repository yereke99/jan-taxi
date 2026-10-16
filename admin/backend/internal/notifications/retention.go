package notifications

import (
	"context"
	"fmt"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Outbox retention: finished rows (sent, dead, cancelled) are deleted once
// they have been inactive for OutboxRetention; their notification_attempts rows
// go with them (ON DELETE CASCADE). Pending and sending rows are never touched.
const (
	OutboxRetention         = 30 * 24 * time.Hour
	OutboxRetentionInterval = 24 * time.Hour
	outboxPruneBatch        = 5000
)

// PruneOutbox deletes finished outbox rows whose last activity (creation,
// last scheduled attempt or delivery) is older than olderThan, in batches so a
// large backlog never holds long locks. It returns the number of outbox rows
// deleted. A row an administrator re-queued recently is kept because its
// next_attempt_at is recent.
func PruneOutbox(ctx context.Context, q db.Querier, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		return 0, fmt.Errorf("notifications: prune outbox: retention must be positive")
	}
	var total int64
	for {
		tag, err := q.Exec(ctx, `
			DELETE FROM notification_outbox WHERE id IN (
				SELECT id FROM notification_outbox
				WHERE status IN ('sent', 'dead', 'cancelled')
				  AND created_at < now() - make_interval(secs => $1)
				  AND GREATEST(next_attempt_at, COALESCE(sent_at, created_at)) < now() - make_interval(secs => $1)
				LIMIT $2)`, olderThan.Seconds(), outboxPruneBatch)
		if err != nil {
			return total, fmt.Errorf("notifications: prune outbox: %w", err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < outboxPruneBatch {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}
