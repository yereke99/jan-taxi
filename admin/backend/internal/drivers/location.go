package drivers

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// Location sources (driver_locations.source).
const (
	SourceMiniApp   = "miniapp"
	SourceBotLive   = "bot_live"
	SourceBotStatic = "bot_static"
)

// Location limits.
const (
	maxAccuracyM     = 100000
	maxSpeedMps      = 100
	maxFutureSkew    = 60 * time.Second
	locationEventGap = 3 * time.Second
)

// LocationInput is one position fix. RecordedAt is the device time of the fix;
// the zero value means "now".
type LocationInput struct {
	Lat, Lon   float64
	AccuracyM  *float64
	Heading    *float64
	SpeedMps   *float64
	RecordedAt time.Time
}

func (in LocationInput) validate(now time.Time) error {
	fields := map[string]string{}
	p := geo.Point{Lat: in.Lat, Lon: in.Lon}
	if p.Valid() != nil {
		fields["lat"], fields["lon"] = "invalid", "invalid"
	}
	check := func(v *float64, name string, min, max float64) {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < min || *v > max) {
			fields[name] = "invalid"
		}
	}
	check(in.AccuracyM, "accuracy_m", 0, maxAccuracyM)
	check(in.Heading, "heading", 0, 360)
	check(in.SpeedMps, "speed_mps", 0, maxSpeedMps)
	if !in.RecordedAt.IsZero() && in.RecordedAt.After(now.Add(maxFutureSkew)) {
		fields["recorded_at"] = "invalid"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// RecordLocation stores a driver's position: the latest fix (older fixes than
// the stored one are ignored), the online heartbeat and, during an active
// order, the trip track. It publishes driver.location_updated to the order's
// passenger and the admin panel at most every 3 seconds per driver. The point
// must be inside Kazakhstan (422 outside_service_area); only approved drivers
// report locations (403 driver_not_approved). A free online driver whose
// previous fix was missing or too old to dispatch on is offered the orders
// already waiting nearby right after the commit (dispatch.OfferNearby).
func RecordLocation(ctx context.Context, pool *pgxpool.Pool, driverID uuid.UUID, in LocationInput, source string) error {
	switch source {
	case SourceMiniApp, SourceBotLive, SourceBotStatic:
	default:
		return fmt.Errorf("drivers: unknown location source %q", source)
	}
	now := time.Now()
	if err := in.validate(now); err != nil {
		return err
	}
	if in.RecordedAt.IsZero() || in.RecordedAt.After(now) {
		in.RecordedAt = now
	}
	pt := geo.Point{Lat: in.Lat, Lon: in.Lon}.Rounded()
	if err := geo.RequireInServiceArea(ctx, pool, "location", pt); err != nil {
		return err
	}
	becameFresh := false
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var (
			status string
			online bool
			prevAt *time.Time
		)
		err := tx.QueryRow(ctx, `
			UPDATE driver_profiles SET last_heartbeat_at = now() WHERE id = $1 RETURNING status, is_online`, driverID).
			Scan(&status, &online)
		if db.IsNoRows(err) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("drivers: heartbeat: %w", err)
		}
		if status != StatusApproved {
			return httpx.NewError(http.StatusForbidden, "driver_not_approved", "driver is not approved").
				WithDetails(map[string]any{"status": status})
		}
		if err := tx.QueryRow(ctx, `SELECT recorded_at FROM driver_locations WHERE driver_id = $1`, driverID).
			Scan(&prevAt); err != nil && !db.IsNoRows(err) {
			return fmt.Errorf("drivers: previous location: %w", err)
		}
		var stored bool
		err = tx.QueryRow(ctx, `
			INSERT INTO driver_locations (driver_id, lat, lon, accuracy_m, heading, speed_mps, source, recorded_at, received_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
			ON CONFLICT (driver_id) DO UPDATE SET
				lat = EXCLUDED.lat, lon = EXCLUDED.lon, accuracy_m = EXCLUDED.accuracy_m, heading = EXCLUDED.heading,
				speed_mps = EXCLUDED.speed_mps, source = EXCLUDED.source, recorded_at = EXCLUDED.recorded_at,
				received_at = EXCLUDED.received_at
			WHERE driver_locations.recorded_at <= EXCLUDED.recorded_at
			RETURNING true`, driverID, pt.Lat, pt.Lon, in.AccuracyM, in.Heading, in.SpeedMps, source, in.RecordedAt).Scan(&stored)
		if db.IsNoRows(err) {
			return nil // an older fix than the stored one
		}
		if err != nil {
			return fmt.Errorf("drivers: store location: %w", err)
		}
		order, active, err := FindActiveOrder(ctx, tx, driverID)
		if err != nil {
			return err
		}
		maxAge := time.Duration(settings.Dispatch(ctx, tx).LocationMaxAgeS) * time.Second
		becameFresh = online && !active && (prevAt == nil || now.Sub(*prevAt) > maxAge)
		if active {
			if _, err := tx.Exec(ctx, `
				INSERT INTO driver_location_history (driver_id, order_id, lat, lon, accuracy_m, source, recorded_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`, driverID, order.ID, pt.Lat, pt.Lon, in.AccuracyM, source, in.RecordedAt); err != nil {
				return fmt.Errorf("drivers: track: %w", err)
			}
		}
		if !(online || active) || !locationEvents.allow(driverID, now) {
			return nil
		}
		data := map[string]any{
			"driver_id": driverID, "lat": pt.Lat, "lon": pt.Lon, "accuracy_m": in.AccuracyM, "heading": in.Heading,
			"recorded_at": in.RecordedAt.UTC(),
		}
		scopes := []string{realtime.AdminScope}
		if active {
			data["order_id"] = order.ID
			scopes = append(scopes, realtime.UserScope(order.PassengerID))
			// The latest arrival estimate (§22.6) while the driver is on the way.
			var eta *int
			if err := tx.QueryRow(ctx, `SELECT driver_eta_s FROM orders WHERE id = $1 AND status = 'accepted'`, order.ID).
				Scan(&eta); err == nil && eta != nil {
				data["driver_eta_s"] = *eta
			}
		}
		return realtime.Publish(ctx, tx, realtime.Event{Type: realtime.DriverLocationUpdated, Scopes: scopes, Data: data})
	})
	if err == nil && becameFresh {
		offerNearby(ctx, pool, driverID)
	}
	return err
}

// offerNearby offers the orders waiting around a driver who just became
// dispatchable; a failure only delays the offers to the next dispatch_expand
// run, so it is logged, not returned.
func offerNearby(ctx context.Context, pool *pgxpool.Pool, driverID uuid.UUID) {
	if _, err := dispatch.OfferNearby(ctx, pool, driverID); err != nil {
		httpx.Logger(ctx).Warn("offering nearby orders failed", slog.String("driver_id", driverID.String()), slog.Any("err", err))
	}
}

// eventThrottle limits location events per driver within this process.
type eventThrottle struct {
	mu   sync.Mutex
	last map[uuid.UUID]time.Time
}

var locationEvents = &eventThrottle{last: map[uuid.UUID]time.Time{}}

func (t *eventThrottle) allow(id uuid.UUID, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.last[id]; ok && now.Sub(prev) < locationEventGap {
		return false
	}
	if len(t.last) > 100000 {
		for k, v := range t.last {
			if now.Sub(v) > time.Minute {
				delete(t.last, k)
			}
		}
	}
	t.last[id] = now
	return true
}

type locationRequest struct {
	Lat        *float64 `json:"lat"`
	Lon        *float64 `json:"lon"`
	AccuracyM  *float64 `json:"accuracy_m"`
	Heading    *float64 `json:"heading"`
	SpeedMps   *float64 `json:"speed_mps"`
	RecordedAt *string  `json:"recorded_at"`
}

func (h *Handler) postLocation(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	if !h.locationLimiter.Allow(d.ID.String()) {
		w.Header().Set("Retry-After", "2")
		return httpx.TooManyRequests()
	}
	var req locationRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	if req.Lat == nil {
		fields["lat"] = "required"
	}
	if req.Lon == nil {
		fields["lon"] = "required"
	}
	var recordedAt time.Time
	if req.RecordedAt != nil {
		t, err := time.Parse(time.RFC3339Nano, *req.RecordedAt)
		if err != nil {
			fields["recorded_at"] = "invalid"
		}
		recordedAt = t
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	in := LocationInput{
		Lat: *req.Lat, Lon: *req.Lon, AccuracyM: req.AccuracyM, Heading: req.Heading, SpeedMps: req.SpeedMps,
		RecordedAt: recordedAt,
	}
	if err := RecordLocation(r.Context(), h.pool, d.ID, in, SourceMiniApp); err != nil {
		return err
	}
	return httpx.NoContent(w)
}
