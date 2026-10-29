package notifications

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

var errReviewNoLongerCurrent = errors.New("application review notification is no longer current")

// lockReviewNotification keeps a delayed or retried OCR review message from
// contradicting a completed moderation decision or a newer application round.
// All decisions/resubmissions lock the profile before updating the application;
// this shared lock serializes the bounded send with those state transitions.
func (w *Worker) lockReviewNotification(ctx context.Context, m outboxRow) (pgx.Tx, error) {
	if m.template != TplModerationReviewRequired {
		return nil, nil
	}
	if m.userID == nil || m.bot != DriverBot {
		return nil, errReviewNoLongerCurrent
	}
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	var current bool
	err = tx.QueryRow(ctx, `
		SELECT true FROM driver_profiles d
		JOIN driver_applications a ON a.driver_id = d.id
		JOIN driver_verification_jobs j ON j.application_id = a.id
		JOIN notification_outbox n ON n.id = $2
		WHERE d.user_id = $1 AND d.status = 'pending' AND a.status = 'pending'
		  AND j.status = 'admin_review' AND j.driver_id = d.id
		  AND n.dedupe_key = 'moderation_review_required:' || a.id::text
		  AND n.status = 'sending' AND n.locked_by = $3 AND n.locked_until > clock_timestamp()
		  AND NOT EXISTS (SELECT 1 FROM driver_applications newer WHERE newer.driver_id = d.id AND newer.round > a.round)
		FOR SHARE OF d`, *m.userID, m.id, w.opt.InstanceID).Scan(&current)
	if err != nil {
		releaseContactLock(tx)
		if db.IsNoRows(err) {
			return nil, errReviewNoLongerCurrent
		}
		return nil, err
	}
	return tx, nil
}
