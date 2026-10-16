package payments

import (
	"net/http"
	"strings"
	"time"
	_ "time/tzdata" // business-day boundaries must not depend on the host's zoneinfo

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// BusinessTimezone is the zone of business days (Embi, Aktobe region; all of
// Kazakhstan is UTC+5 since March 2024).
const BusinessTimezone = "Asia/Aqtobe"

var businessLoc = func() *time.Location {
	loc, err := time.LoadLocation(BusinessTimezone)
	if err != nil {
		return time.FixedZone(BusinessTimezone, 5*60*60)
	}
	return loc
}()

// queryTime parses an optional RFC 3339 timestamp or a YYYY-MM-DD date in the
// business time zone; a date in an exclusive upper bound (endOfDay) covers the
// whole day.
func queryTime(r *http.Request, name string, endOfDay bool) (*time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t, nil
	}
	d, err := time.ParseInLocation(time.DateOnly, raw, businessLoc)
	if err != nil {
		return nil, httpx.Validation(map[string]string{name: "invalid"})
	}
	if endOfDay {
		d = d.AddDate(0, 0, 1)
	}
	return &d, nil
}

// queryRange parses ?from=&to= and rejects an empty or inverted range.
func queryRange(r *http.Request) (from, to *time.Time, err error) {
	if from, err = queryTime(r, "from", false); err != nil {
		return nil, nil, err
	}
	if to, err = queryTime(r, "to", true); err != nil {
		return nil, nil, err
	}
	if from != nil && to != nil && !to.After(*from) {
		return nil, nil, httpx.Validation(map[string]string{"to": "invalid"})
	}
	return from, to, nil
}

// normalizeTransactionID canonicalises a provider transaction / receipt number
// so the same payment cannot be recorded twice with different spacing or case.
func normalizeTransactionID(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), ""))
}

// likePattern escapes LIKE wildcards in s and wraps it in % for a substring match.
func likePattern(s string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s) + "%"
}
