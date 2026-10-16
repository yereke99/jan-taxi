package audit

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Handler serves the admin audit log.
type Handler struct {
	pool *pgxpool.Pool
}

// NewHandler builds the audit log handler.
func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }

// MountAdmin registers GET /audit-logs on the admin router (audit.view).
func (h *Handler) MountAdmin(r chi.Router) {
	r.With(auth.Permit(auth.PermAuditView)).Get("/audit-logs", httpx.H(h.list))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	f, err := ParseFilter(r)
	if err != nil {
		return err
	}
	items, total, err := List(r.Context(), h.pool, f)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, httpx.Page[Record]{Items: items, Page: f.Page.Page, PageSize: f.Page.PageSize, Total: total})
}

// ParseFilter reads ?admin_id=&entity_type=&entity_id=&action=&from=&to=&page=&page_size=.
// from/to accept RFC 3339 timestamps or YYYY-MM-DD dates (UTC); a date in "to"
// includes that whole day.
func ParseFilter(r *http.Request) (Filter, error) {
	var f Filter
	var err error
	if f.Page, err = httpx.ParsePage(r); err != nil {
		return f, err
	}
	if f.AdminID, err = httpx.QueryUUID(r, "admin_id"); err != nil {
		return f, err
	}
	q := r.URL.Query()
	fields := map[string]string{}
	text := func(name string, max int) string {
		v := strings.TrimSpace(q.Get(name))
		if utf8.RuneCountInString(v) > max {
			fields[name] = "too_long"
			return ""
		}
		return v
	}
	f.EntityType = text("entity_type", 64)
	f.EntityID = text("entity_id", 128)
	f.Action = text("action", 64)
	if f.From, err = parseTime(q.Get("from"), false); err != nil {
		fields["from"] = "invalid"
	}
	if f.To, err = parseTime(q.Get("to"), true); err != nil {
		fields["to"] = "invalid"
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		fields["to"] = "before_from"
	}
	if len(fields) > 0 {
		return f, httpx.Validation(fields)
	}
	return f, nil
}

func parseTime(s string, endOfDay bool) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		t = t.UTC()
		return &t, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil, err
	}
	if endOfDay {
		t = t.AddDate(0, 0, 1)
	}
	return &t, nil
}
