// Package reports serves the admin dashboard metrics and the CSV exports of
// orders, ledger entries and top-ups.
package reports

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata" // business-day boundaries must not depend on the host's zoneinfo

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// DefaultTimezone is the business time zone when no city is selected.
const DefaultTimezone = "Asia/Aqtobe"

// Range limits.
const (
	MaxDashboardDays = 92
	MaxExportDays    = 366
)

// Period is the half-open interval [From, To) with the zone used for day boundaries.
type Period struct {
	From, To time.Time
	Loc      *time.Location
}

// Days returns the local calendar dates the period touches, in order.
func (p Period) Days() []string {
	var out []string
	first := startOfDay(p.From.In(p.Loc))
	last := p.To.Add(-time.Nanosecond).In(p.Loc)
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format(time.DateOnly))
	}
	return out
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// Today is the local calendar day containing now.
func Today(now time.Time, loc *time.Location) Period {
	from := startOfDay(now.In(loc))
	return Period{From: from, To: from.AddDate(0, 0, 1), Loc: loc}
}

// loadLocation resolves an IANA zone, falling back to the business zone.
func loadLocation(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	if loc, err := time.LoadLocation(DefaultTimezone); err == nil {
		return loc
	}
	return time.FixedZone(DefaultTimezone, 5*60*60)
}

// cityLocation returns the time zone of the selected city (422 for an unknown
// city) or the default business zone.
func cityLocation(ctx context.Context, q db.Querier, cityID *uuid.UUID) (*time.Location, error) {
	if cityID == nil {
		return loadLocation(DefaultTimezone), nil
	}
	var tz string
	err := q.QueryRow(ctx, `SELECT timezone FROM cities WHERE id = $1`, *cityID).Scan(&tz)
	if db.IsNoRows(err) {
		return nil, httpx.Validation(map[string]string{"city_id": "invalid"})
	}
	if err != nil {
		return nil, fmt.Errorf("reports: city time zone: %w", err)
	}
	return loadLocation(tz), nil
}

// parsePeriod reads ?from=&to= as RFC 3339 timestamps or YYYY-MM-DD dates in
// loc (a date in "to" includes that whole day). Missing bounds default to the
// defaultDays local days ending today. The period must be non-empty and at most
// maxDays long.
func parsePeriod(r *http.Request, loc *time.Location, now time.Time, defaultDays, maxDays int) (Period, error) {
	from, err := parseBound(r, "from", loc, false)
	if err != nil {
		return Period{}, err
	}
	to, err := parseBound(r, "to", loc, true)
	if err != nil {
		return Period{}, err
	}
	p := Period{Loc: loc}
	switch {
	case from != nil && to != nil:
		p.From, p.To = *from, *to
	case from != nil:
		p.From = *from
		p.To = Today(now, loc).To
	case to != nil:
		p.To = *to
		p.From = startOfDay(to.Add(-time.Nanosecond).In(loc)).AddDate(0, 0, 1-defaultDays)
	default:
		p.To = Today(now, loc).To
		p.From = p.To.AddDate(0, 0, -defaultDays)
	}
	if !p.To.After(p.From) {
		return Period{}, httpx.Validation(map[string]string{"to": "invalid"})
	}
	// Kazakhstan has no daylight saving time, so days are 24 hours long.
	if p.To.Sub(p.From) > time.Duration(maxDays)*24*time.Hour {
		return Period{}, httpx.Validation(map[string]string{"to": "too_large"}).
			WithDetails(map[string]any{"max_days": maxDays})
	}
	return p, nil
}

func parseBound(r *http.Request, name string, loc *time.Location, endOfDay bool) (*time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t, nil
	}
	d, err := time.ParseInLocation(time.DateOnly, raw, loc)
	if err != nil {
		return nil, httpx.Validation(map[string]string{name: "invalid"})
	}
	if endOfDay {
		d = d.AddDate(0, 0, 1)
	}
	return &d, nil
}
