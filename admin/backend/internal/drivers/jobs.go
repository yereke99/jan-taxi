package drivers

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// LocationHistoryRetention is how long trip tracks are kept.
const LocationHistoryRetention = 30 * 24 * time.Hour

const idleSettingKey = "driver.online_idle_hours"

// IdleTimeout is how long an online driver may stay silent before being set
// offline: the admin setting driver.online_idle_hours when an admin has set
// it, otherwise envDefault (DRIVER_ONLINE_IDLE_TIMEOUT), otherwise the
// built-in default.
func IdleTimeout(ctx context.Context, q db.Querier, envDefault time.Duration) time.Duration {
	var hours int64
	if found, err := settings.JSON(ctx, q, idleSettingKey, &hours); err == nil && found && hours >= 1 && hours <= 72 {
		return time.Duration(hours) * time.Hour
	}
	if envDefault > 0 {
		return envDefault
	}
	return settings.DriverOnlineIdleTimeout(ctx, q)
}

// SetStaleDriversOffline switches off online drivers whose last heartbeat or
// location is older than timeout and publishes driver.status_changed for
// each. It returns the number of drivers switched off.
func SetStaleDriversOffline(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		return 0, fmt.Errorf("drivers: idle timeout must be positive")
	}
	n := 0
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE driver_profiles SET is_online = false, online_since = NULL
			WHERE is_online AND COALESCE(last_heartbeat_at, online_since, updated_at) < now() - $1::float8 * interval '1 second'
			RETURNING id, user_id, status`, timeout.Seconds())
		if err != nil {
			return fmt.Errorf("drivers: stale offline: %w", err)
		}
		type row struct {
			id, userID uuid.UUID
			status     string
		}
		var changed []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.userID, &r.status); err != nil {
				rows.Close()
				return fmt.Errorf("drivers: stale offline: %w", err)
			}
			changed = append(changed, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("drivers: stale offline: %w", err)
		}
		for _, r := range changed {
			if err := PublishStatus(ctx, tx, r.id, r.userID, r.status, false); err != nil {
				return err
			}
		}
		n = len(changed)
		return nil
	})
	return n, err
}

// PruneLocationHistory deletes trip-track points received before
// now - retention, in batches so no long lock is held. It returns the number
// of deleted rows.
func PruneLocationHistory(ctx context.Context, pool *pgxpool.Pool, retention time.Duration) (int64, error) {
	var total int64
	for {
		tag, err := pool.Exec(ctx, `
			DELETE FROM driver_location_history WHERE id IN (
				SELECT id FROM driver_location_history WHERE received_at < now() - $1::float8 * interval '1 second' LIMIT 5000)`,
			retention.Seconds())
		if err != nil {
			return total, fmt.Errorf("drivers: prune location history: %w", err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < 5000 || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}
