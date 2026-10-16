package orders

import (
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Mini App lists page by (created_at, id), newest first. The cursor is opaque
// to clients: base64url("<created_at RFC 3339>|<id>").

const (
	defaultListLimit = 20
	maxListLimit     = 50
)

type cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

func encodeCursor(createdAt time.Time, id uuid.UUID) *string {
	s := base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
	return &s
}

func decodeCursor(s string) (*cursor, error) {
	if s == "" {
		return nil, nil
	}
	invalid := httpx.Validation(map[string]string{"before": "invalid"})
	if len(s) > 200 {
		return nil, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, invalid
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return nil, invalid
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return nil, invalid
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, invalid
	}
	return &cursor{CreatedAt: t, ID: u}, nil
}

// listParams parses ?limit=&before= for Mini App lists.
func listParams(r *http.Request) (int, *cursor, error) {
	limit, err := httpx.QueryInt(r, "limit", defaultListLimit, 1, maxListLimit)
	if err != nil {
		return 0, nil, err
	}
	c, err := decodeCursor(strings.TrimSpace(r.URL.Query().Get("before")))
	if err != nil {
		return 0, nil, err
	}
	return limit, c, nil
}

// cursorArgs returns the SQL arguments for "(created_at, id) < (cursor)"
// (NULLs when there is no cursor).
func cursorArgs(c *cursor) (*time.Time, *uuid.UUID) {
	if c == nil {
		return nil, nil
	}
	return &c.CreatedAt, &c.ID
}
