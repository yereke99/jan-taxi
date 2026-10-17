package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Handler serves the admin settings endpoints.
type Handler struct {
	pool *pgxpool.Pool
}

// NewHandler builds the admin settings handler.
func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }

// MountAdmin registers GET/PATCH /settings on the admin router (config.manage).
func (h *Handler) MountAdmin(r chi.Router) {
	r.With(auth.Permit(auth.PermConfigManage)).Get("/settings", httpx.H(h.list))
	r.With(auth.Permit(auth.PermConfigManage)).Patch("/settings", httpx.H(h.patch))
}

// Item is one setting as shown to admins.
type Item struct {
	Key           string          `json:"key"`
	Type          Kind            `json:"type"`
	Value         json.RawMessage `json:"value"`
	Default       any             `json:"default"`
	Source        string          `json:"source"` // database | env | default
	HasEnvDefault bool            `json:"has_env_default"`
	OverridesEnv  bool            `json:"overrides_env"`
	Description   Localized       `json:"description"`
	Constraints   Constraints     `json:"constraints"`
	UpdatedAt     *time.Time      `json:"updated_at"`
	UpdatedBy     *uuid.UUID      `json:"updated_by"`
}

// Constraints lets the admin UI render matching inputs.
type Constraints struct {
	Min      *int64 `json:"min,omitempty"`
	Max      *int64 `json:"max,omitempty"`
	MinLen   int    `json:"min_length,omitempty"`
	MaxLen   int    `json:"max_length,omitempty"`
	MaxItems int    `json:"max_items,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type row struct {
	value     json.RawMessage
	updatedAt time.Time
	updatedBy *uuid.UUID
}

func loadRows(ctx context.Context, q db.Querier, lock bool, keys []string) (map[string]row, error) {
	sql := `SELECT key, value, updated_at, updated_by FROM app_settings`
	args := []any{}
	if keys != nil {
		sql += ` WHERE key = ANY($1)`
		args = append(args, keys)
	}
	if lock {
		sql += ` FOR UPDATE`
	}
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]row{}
	for rows.Next() {
		var k string
		var r row
		if err := rows.Scan(&k, &r.value, &r.updatedAt, &r.updatedBy); err != nil {
			return nil, err
		}
		out[k] = r
	}
	return out, rows.Err()
}

// List returns every registered setting with its effective value.
func List(ctx context.Context, q db.Querier) ([]Item, error) {
	rows, err := loadRows(ctx, q, false, nil)
	if err != nil {
		return nil, err
	}
	env := CurrentEnv()
	items := make([]Item, 0, len(registry))
	for _, d := range registry {
		def := d.DefaultValue(env)
		it := Item{
			Key: d.Key, Type: d.Kind, Default: def, Source: "default",
			HasEnvDefault: d.HasEnvDefault(), Description: d.Description, Constraints: d.constraints(),
		}
		if d.env != nil {
			if _, ok := d.env(env); ok {
				it.Source = "env"
			}
		}
		if r, ok := rows[d.Key]; ok {
			it.Value, it.Source = r.value, "database"
			it.OverridesEnv = d.HasEnvDefault()
			at := r.updatedAt
			it.UpdatedAt, it.UpdatedBy = &at, r.updatedBy
		} else {
			b, err := json.Marshal(def)
			if err != nil {
				return nil, err
			}
			it.Value = b
		}
		items = append(items, it)
	}
	return items, nil
}

func (d Def) constraints() Constraints {
	c := Constraints{MinLen: d.MinLen, MaxLen: d.MaxLen, MaxItems: d.MaxItems, Required: d.Required}
	if d.Kind == KindInt || d.Kind == KindIntList {
		lo, hi := d.Min, d.Max
		c.Min, c.Max = &lo, &hi
	}
	return c
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	items, err := List(r.Context(), h.pool)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, map[string]any{"items": items})
}

// patch applies {key: value|null, ...}; null restores the env/built-in default.
func (h *Handler) patch(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	var body map[string]json.RawMessage
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		return err
	}
	if len(body) == 0 {
		return httpx.BadRequest("no settings to update")
	}
	changes := map[string]json.RawMessage{} // nil value = reset
	fields := map[string]string{}
	for k, raw := range body {
		d, ok := Lookup(k)
		if !ok {
			fields[k] = ReasonUnknown
			continue
		}
		if string(raw) == "null" {
			changes[k] = nil
			continue
		}
		norm, reason := d.Normalize(raw)
		if reason != "" {
			fields[k] = reason
			continue
		}
		changes[k] = norm
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	ctx := r.Context()
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		current, err := loadRows(ctx, tx, true, keys)
		if err != nil {
			return httpx.Internal(err)
		}
		if err := checkConsistency(ctx, tx, changes); err != nil {
			return err
		}
		for _, k := range keys {
			var before any
			if c, ok := current[k]; ok {
				before = c.value
			}
			entry := audit.Entry{EntityType: "app_setting", EntityID: k, Before: map[string]any{"value": before}}
			if v := changes[k]; v == nil {
				if _, err := tx.Exec(ctx, `DELETE FROM app_settings WHERE key = $1`, k); err != nil {
					return httpx.Internal(err)
				}
				entry.Action = "settings.reset"
				entry.After = map[string]any{"value": nil}
			} else {
				if err := upsert(ctx, tx, k, v, admin.AdminID); err != nil {
					return err
				}
				entry.Action = "settings.update"
				entry.After = map[string]any{"value": v}
			}
			if err := audit.Log(ctx, tx, r, entry); err != nil {
				return httpx.Internal(err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	Invalidate(keys...)
	return h.list(w, r)
}

// checkConsistency rejects a patch whose related keys would contradict each
// other once applied: top-up min > max or presets outside [min, max], and a
// maximum dispatch radius below the base radius. Each rule is judged on the
// values in effect afterwards and only when the patch touches one of its keys.
func checkConsistency(ctx context.Context, tx pgx.Tx, changes map[string]json.RawMessage) error {
	fields := map[string]string{}
	if touches(changes, "topup.min_amount", "topup.max_amount", "topup.presets") {
		var lo, hi int64
		var presets []int64
		if err := resolveAfter(ctx, tx, changes, "topup.min_amount", &lo); err != nil {
			return httpx.Internal(err)
		}
		if err := resolveAfter(ctx, tx, changes, "topup.max_amount", &hi); err != nil {
			return httpx.Internal(err)
		}
		if err := resolveAfter(ctx, tx, changes, "topup.presets", &presets); err != nil {
			return httpx.Internal(err)
		}
		if lo > hi {
			fields["topup.min_amount"] = "greater_than_max"
		}
		for _, p := range presets {
			if p < lo || p > hi {
				fields["topup.presets"] = ReasonOutOfRange
				break
			}
		}
	}
	if touches(changes, "dispatch.radius_base_m", "dispatch.radius_max_m") {
		var base, maxR int64
		if err := resolveAfter(ctx, tx, changes, "dispatch.radius_base_m", &base); err != nil {
			return httpx.Internal(err)
		}
		if err := resolveAfter(ctx, tx, changes, "dispatch.radius_max_m", &maxR); err != nil {
			return httpx.Internal(err)
		}
		if maxR < base {
			fields["dispatch.radius_max_m"] = "less_than_base"
		}
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

func touches(changes map[string]json.RawMessage, keys ...string) bool {
	for _, k := range keys {
		if _, ok := changes[k]; ok {
			return true
		}
	}
	return false
}

// resolveAfter decodes the value key will have once changes are applied: the
// patched value, the default for a reset, or the current effective value.
func resolveAfter(ctx context.Context, tx pgx.Tx, changes map[string]json.RawMessage, key string, dst any) error {
	if v, ok := changes[key]; ok {
		if v != nil {
			return json.Unmarshal(v, dst)
		}
		d, _ := Lookup(key)
		b, err := json.Marshal(d.DefaultValue(CurrentEnv()))
		if err != nil {
			return err
		}
		return json.Unmarshal(b, dst)
	}
	return Effective(ctx, tx, key, dst)
}
