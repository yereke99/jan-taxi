package payments

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
)

// expiryBatch bounds one expiry transaction.
const expiryBatch = 200

// ExpireTopUps closes created top-ups whose expires_at has passed (job
// "topup_expiry"). Top-ups waiting for verification never expire: the driver
// may already have paid. It returns how many top-ups expired.
func (s *Service) ExpireTopUps(ctx context.Context) (int, error) {
	total := 0
	for {
		n, err := s.expireBatch(ctx)
		total += n
		if err != nil {
			return total, err
		}
		if n < expiryBatch {
			if total > 0 {
				s.log.Info("top-ups expired", slog.Int("count", total))
			}
			return total, nil
		}
	}
}

func (s *Service) expireBatch(ctx context.Context) (int, error) {
	n := 0
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE top_up_requests t SET status = 'expired'
			FROM driver_profiles d
			WHERE t.id IN (
				SELECT id FROM top_up_requests
				WHERE status = 'created' AND expires_at <= now()
				ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED)
			  AND d.id = t.driver_id
			RETURNING t.id, t.reference, t.driver_id, d.user_id`, expiryBatch)
		if err != nil {
			return fmt.Errorf("payments: expire top-ups: %w", err)
		}
		var expired []TopUp
		for rows.Next() {
			var t TopUp
			if err := rows.Scan(&t.ID, &t.Reference, &t.DriverID, &t.DriverUserID); err != nil {
				rows.Close()
				return fmt.Errorf("payments: scan expired top-up: %w", err)
			}
			expired = append(expired, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("payments: expire top-ups: %w", err)
		}
		for _, t := range expired {
			if err := addEvent(ctx, tx, t.ID, StatusCreated, StatusExpired, ActorSystem, (*uuid.UUID)(nil), NoteExpired, nil); err != nil {
				return err
			}
			if err := publishTopUp(ctx, tx, t, realtime.TopUpUpdated, StatusExpired, false); err != nil {
				return err
			}
		}
		n = len(expired)
		return nil
	})
	return n, err
}
