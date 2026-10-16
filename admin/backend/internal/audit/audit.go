// Package audit records administrator actions in the append-only admin_audit_logs
// table. Log is called inside the same transaction as the audited change.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Entry describes one audited action. Before/After are marshalled to JSON;
// keys that look like secrets are redacted.
type Entry struct {
	Action     string // e.g. "driver.approve", "topup.confirm"
	EntityType string // e.g. "driver", "topup", "order"
	EntityID   string
	Before     any
	After      any
	// AdminID overrides the admin taken from the request context (used by jobs and CLI).
	AdminID *uuid.UUID
}

// Log writes an audit row. r may be nil for non-HTTP callers.
func Log(ctx context.Context, q db.Querier, r *http.Request, e Entry) error {
	var adminID *uuid.UUID
	if e.AdminID != nil {
		adminID = e.AdminID
	} else if a, ok := auth.AdminFrom(ctx); ok {
		id := a.AdminID
		adminID = &id
	}
	before, err := marshal(e.Before)
	if err != nil {
		return err
	}
	after, err := marshal(e.After)
	if err != nil {
		return err
	}
	var ip, ua *string
	if r != nil {
		v := httpx.ClientIP(r)
		ip = &v
		u := r.UserAgent()
		if len(u) > 300 {
			u = u[:300]
		}
		ua = &u
	}
	var entityID *string
	if e.EntityID != "" {
		entityID = &e.EntityID
	}
	_, err = q.Exec(ctx, `
		INSERT INTO admin_audit_logs (admin_id, action, entity_type, entity_id, before, after, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7::inet, $8)`,
		adminID, e.Action, e.EntityType, entityID, before, after, ip, ua)
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	return nil
}

// secretKeys are matched against object keys lower-cased with "_" and "-"
// removed, so "init_data", "initData" and "Init-Data" are all caught.
var secretKeys = []string{"password", "token", "secret", "hash", "initdata", "authorization", "apikey", "privatekey", "cookie", "csrf"}

var keySeparators = strings.NewReplacer("_", "", "-", "")

func isSecretKey(k string) bool {
	norm := keySeparators.Replace(strings.ToLower(k))
	for _, s := range secretKeys {
		if strings.Contains(norm, s) {
			return true
		}
	}
	return false
}

func marshal(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("audit marshal: %w", err)
	}
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		return b, nil
	}
	return json.Marshal(redact(generic))
}

func redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSecretKey(k) {
				t[k] = "[redacted]"
			} else {
				t[k] = redact(val)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = redact(t[i])
		}
		return t
	default:
		return v
	}
}
