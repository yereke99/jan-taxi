package marketing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// Segments (§23.7) are typed, server-validated filters over people who
// started a bot; they are evaluated in SQL with bound parameters only.

// Audiences.
const (
	AudiencePassengers = "passengers"
	AudienceDrivers    = "drivers"
)

// Filters narrows an audience. Every field is optional; passenger-only and
// driver-only fields are refused for the other audience.
type Filters struct {
	Languages            []string    `json:"languages,omitempty"`
	RegisteredWithinDays *int        `json:"registered_within_days,omitempty"`
	RegisteredBeforeDays *int        `json:"registered_before_days,omitempty"`
	CompletedRidesMin    *int        `json:"completed_rides_min,omitempty"`
	CompletedRidesMax    *int        `json:"completed_rides_max,omitempty"`
	LastRideWithinDays   *int        `json:"last_ride_within_days,omitempty"`
	NoRideForDays        *int        `json:"no_ride_for_days,omitempty"`
	HasActiveCoupon      *bool       `json:"has_active_coupon,omitempty"`
	DriverStatuses       []string    `json:"driver_statuses,omitempty"`
	OnlineWithinDays     *int        `json:"online_within_days,omitempty"`
	CityIDs              []uuid.UUID `json:"city_ids,omitempty"`
}

var (
	languages      = []string{"kk", "ru", "en"}
	driverStatuses = []string{"approved", "suspended", "pending", "changes_requested"}
)

// parseFilters decodes and validates raw filters for audience.
func parseFilters(audience string, raw json.RawMessage, fields map[string]string) Filters {
	var f Filters
	if len(bytes.TrimSpace(raw)) == 0 {
		return f
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		fields["filters"] = "invalid"
		return f
	}
	days := func(name string, v *int) {
		if v != nil && (*v < 0 || *v > 3650) {
			fields["filters."+name] = "out_of_range"
		}
	}
	for _, l := range f.Languages {
		if !slices.Contains(languages, l) {
			fields["filters.languages"] = "invalid"
		}
	}
	days("registered_within_days", f.RegisteredWithinDays)
	days("registered_before_days", f.RegisteredBeforeDays)
	days("last_ride_within_days", f.LastRideWithinDays)
	days("no_ride_for_days", f.NoRideForDays)
	days("online_within_days", f.OnlineWithinDays)
	for name, v := range map[string]*int{"completed_rides_min": f.CompletedRidesMin, "completed_rides_max": f.CompletedRidesMax} {
		if v != nil && (*v < 0 || *v > 100000) {
			fields["filters."+name] = "out_of_range"
		}
	}
	if f.CompletedRidesMin != nil && f.CompletedRidesMax != nil && *f.CompletedRidesMax < *f.CompletedRidesMin {
		fields["filters.completed_rides_max"] = "below_min"
	}
	switch audience {
	case AudiencePassengers:
		if len(f.DriverStatuses) > 0 || f.OnlineWithinDays != nil || len(f.CityIDs) > 0 {
			fields["filters"] = "driver_only_filter"
		}
	case AudienceDrivers:
		if f.HasActiveCoupon != nil || f.LastRideWithinDays != nil || f.NoRideForDays != nil {
			fields["filters"] = "passenger_only_filter"
		}
		for _, s := range f.DriverStatuses {
			if !slices.Contains(driverStatuses, s) {
				fields["filters.driver_statuses"] = "invalid"
			}
		}
		if len(f.CityIDs) > 50 {
			fields["filters.city_ids"] = "too_many"
		}
	default:
		fields["audience"] = "invalid"
	}
	return f
}

// audienceQuery builds "FROM ... WHERE ..." selecting u (users) and p
// (user_preferences) for the audience: everyone who started the audience's
// bot and matches f. suppressed is a SQL expression true for people who must
// not get marketing (opted out, blocked the bot, blocked by an operator).
func audienceQuery(audience string, f Filters, args *[]any) (from, suppressed string) {
	arg := func(v any) string {
		*args = append(*args, v)
		return "$" + strconv.Itoa(len(*args))
	}
	var where []string
	from = ` FROM users u LEFT JOIN user_preferences p ON p.user_id = u.id`
	if audience == AudienceDrivers {
		from += ` JOIN driver_profiles d ON d.user_id = u.id`
		where = append(where, "u.driver_bot_started_at IS NOT NULL")
		suppressed = "(p.marketing_opt_out_at IS NOT NULL OR u.driver_bot_blocked_at IS NOT NULL OR u.is_blocked)"
		if len(f.DriverStatuses) > 0 {
			where = append(where, "d.status = ANY("+arg(f.DriverStatuses)+")")
		} else {
			where = append(where, "d.status = 'approved'")
		}
		if f.OnlineWithinDays != nil {
			where = append(where, "d.last_heartbeat_at > now() - make_interval(days => "+arg(*f.OnlineWithinDays)+")")
		}
		if len(f.CityIDs) > 0 {
			where = append(where, "d.city_id = ANY("+arg(f.CityIDs)+")")
		}
		if f.CompletedRidesMin != nil {
			where = append(where, "d.completed_trips >= "+arg(*f.CompletedRidesMin))
		}
		if f.CompletedRidesMax != nil {
			where = append(where, "d.completed_trips <= "+arg(*f.CompletedRidesMax))
		}
	} else {
		where = append(where, "u.passenger_bot_started_at IS NOT NULL")
		suppressed = "(p.marketing_opt_out_at IS NOT NULL OR u.passenger_bot_blocked_at IS NOT NULL OR u.is_blocked)"
		rides := "(SELECT count(*) FROM orders o WHERE o.passenger_id = u.id AND o.status = 'completed')"
		if f.CompletedRidesMin != nil {
			where = append(where, rides+" >= "+arg(*f.CompletedRidesMin))
		}
		if f.CompletedRidesMax != nil {
			where = append(where, rides+" <= "+arg(*f.CompletedRidesMax))
		}
		if f.LastRideWithinDays != nil {
			where = append(where, "EXISTS (SELECT 1 FROM orders o WHERE o.passenger_id = u.id AND o.status = 'completed'"+
				" AND o.completed_at > now() - make_interval(days => "+arg(*f.LastRideWithinDays)+"))")
		}
		if f.NoRideForDays != nil {
			where = append(where, "NOT EXISTS (SELECT 1 FROM orders o WHERE o.passenger_id = u.id AND o.status = 'completed'"+
				" AND o.completed_at > now() - make_interval(days => "+arg(*f.NoRideForDays)+"))")
		}
		if f.HasActiveCoupon != nil {
			cond := "EXISTS (SELECT 1 FROM marketing_coupons mc WHERE mc.user_id = u.id AND mc.status = 'issued' AND mc.expires_at > now())"
			if !*f.HasActiveCoupon {
				cond = "NOT " + cond
			}
			where = append(where, cond)
		}
	}
	if len(f.Languages) > 0 {
		where = append(where, "COALESCE(p.language, 'kk') = ANY("+arg(f.Languages)+")")
	}
	if f.RegisteredWithinDays != nil {
		where = append(where, "u.created_at > now() - make_interval(days => "+arg(*f.RegisteredWithinDays)+")")
	}
	if f.RegisteredBeforeDays != nil {
		where = append(where, "u.created_at <= now() - make_interval(days => "+arg(*f.RegisteredBeforeDays)+")")
	}
	return from + " WHERE " + strings.Join(where, " AND "), suppressed
}

// AudienceCount is a segment estimate; no personal data leaves the server.
type AudienceCount struct {
	Matching   int64 `json:"matching"`
	Reachable  int64 `json:"reachable"`
	Suppressed int64 `json:"suppressed"`
}

func countAudience(ctx context.Context, q db.Querier, audience string, f Filters) (AudienceCount, error) {
	var args []any
	from, suppressed := audienceQuery(audience, f, &args)
	var c AudienceCount
	err := q.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE NOT `+suppressed+`)`+from, args...).
		Scan(&c.Matching, &c.Reachable)
	if err != nil {
		return AudienceCount{}, fmt.Errorf("marketing: count audience: %w", err)
	}
	c.Suppressed = c.Matching - c.Reachable
	return c, nil
}

// Segment is a saved audience (openapi MarketingSegment).
type Segment struct {
	ID         uuid.UUID       `json:"id"`
	Name       string          `json:"name"`
	Audience   string          `json:"audience"`
	Filters    json.RawMessage `json:"filters"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	ArchivedAt *time.Time      `json:"archived_at"`
	Count      *AudienceCount  `json:"count,omitempty"`
}

type segmentInput struct {
	Name     *string         `json:"name"`
	Audience *string         `json:"audience"`
	Filters  json.RawMessage `json:"filters"`
	Archived *bool           `json:"archived"`
}

func loadSegment(ctx context.Context, q db.Querier, id uuid.UUID) (Segment, error) {
	var s Segment
	err := q.QueryRow(ctx, `
		SELECT id, name, audience, filters, created_at, updated_at, archived_at FROM marketing_segments WHERE id = $1`, id).
		Scan(&s.ID, &s.Name, &s.Audience, &s.Filters, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt)
	if db.IsNoRows(err) {
		return Segment{}, httpx.NotFound()
	}
	if err != nil {
		return Segment{}, fmt.Errorf("marketing: segment: %w", err)
	}
	return s, nil
}

func listSegments(ctx context.Context, q db.Querier) ([]Segment, error) {
	rows, err := q.Query(ctx, `
		SELECT id, name, audience, filters, created_at, updated_at, archived_at FROM marketing_segments
		ORDER BY archived_at IS NOT NULL, created_at DESC LIMIT 200`)
	if err != nil {
		return nil, fmt.Errorf("marketing: segments: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Segment, error) {
		var s Segment
		err := row.Scan(&s.ID, &s.Name, &s.Audience, &s.Filters, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt)
		return s, err
	})
}

// saveSegment creates (id nil) or updates a segment.
func saveSegment(ctx context.Context, tx pgx.Tx, id *uuid.UUID, in segmentInput, adminID uuid.UUID) (uuid.UUID, error) {
	fields := map[string]string{}
	var cur Segment
	if id != nil {
		var err error
		if cur, err = loadSegment(ctx, tx, *id); err != nil {
			return uuid.Nil, err
		}
	}
	name := cur.Name
	if in.Name != nil {
		v, err := textutil.CleanText(*in.Name, 120)
		if err != nil {
			fields["name"] = "too_long"
		}
		name = v
	}
	if name == "" {
		fields["name"] = "required"
	}
	audience := cur.Audience
	if in.Audience != nil {
		if id != nil && *in.Audience != cur.Audience {
			fields["audience"] = "immutable"
		}
		audience = *in.Audience
	}
	raw := cur.Filters
	if in.Filters != nil {
		raw = in.Filters
	}
	f := parseFilters(audience, raw, fields)
	if len(fields) > 0 {
		return uuid.Nil, httpx.Validation(fields)
	}
	norm, _ := json.Marshal(f)
	if id == nil {
		var newID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO marketing_segments (name, audience, filters, created_by, updated_by) VALUES ($1, $2, $3, $4, $4)
			RETURNING id`, name, audience, norm, idPtr(adminID)).Scan(&newID)
		return newID, err
	}
	archived := cur.ArchivedAt != nil
	if in.Archived != nil {
		archived = *in.Archived
	}
	_, err := tx.Exec(ctx, `
		UPDATE marketing_segments SET name = $2, filters = $3, updated_by = $4,
		       archived_at = CASE WHEN $5 THEN COALESCE(archived_at, now()) ELSE NULL END
		WHERE id = $1`, *id, name, norm, idPtr(adminID), archived)
	return *id, err
}
