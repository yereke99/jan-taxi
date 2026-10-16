package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Filter selects audit rows; zero fields are ignored. The time range is [From, To).
type Filter struct {
	AdminID    *uuid.UUID
	EntityType string
	EntityID   string
	Action     string
	From, To   *time.Time
	Page       httpx.PageParams
}

// Record is one audit row with the acting admin's identity joined in.
type Record struct {
	ID         int64           `json:"id"`
	AdminID    *uuid.UUID      `json:"admin_id"`
	AdminEmail *string         `json:"admin_email"`
	AdminName  *string         `json:"admin_name"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   *string         `json:"entity_id"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
	IP         *string         `json:"ip"`
	UserAgent  *string         `json:"user_agent"`
	CreatedAt  time.Time       `json:"created_at"`
}

// List returns one page of audit rows, newest first, and the total match count.
func List(ctx context.Context, q db.Querier, f Filter) ([]Record, int, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.AdminID != nil {
		add("l.admin_id = $%d", *f.AdminID)
	}
	if f.EntityType != "" {
		add("l.entity_type = $%d", f.EntityType)
	}
	if f.EntityID != "" {
		add("l.entity_id = $%d", f.EntityID)
	}
	if f.Action != "" {
		add("l.action = $%d", f.Action)
	}
	if f.From != nil {
		add("l.created_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("l.created_at < $%d", *f.To)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs l`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}
	if f.Page.PageSize <= 0 {
		f.Page = httpx.PageParams{Page: 1, PageSize: 25}
	}
	args = append(args, f.Page.PageSize, f.Page.Offset())
	rows, err := q.Query(ctx, `
		SELECT l.id, l.admin_id, a.email::text, a.full_name, l.action, l.entity_type, l.entity_id,
		       l.before, l.after, host(l.ip), l.user_agent, l.created_at
		FROM admin_audit_logs l
		LEFT JOIN admin_users a ON a.id = l.admin_id`+cond+fmt.Sprintf(`
		ORDER BY l.created_at DESC, l.id DESC
		LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var rec Record
		var before, after []byte
		if err := rows.Scan(&rec.ID, &rec.AdminID, &rec.AdminEmail, &rec.AdminName, &rec.Action, &rec.EntityType,
			&rec.EntityID, &before, &after, &rec.IP, &rec.UserAgent, &rec.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan audit log: %w", err)
		}
		if before != nil {
			rec.Before = before
		}
		if after != nil {
			rec.After = after
		}
		out = append(out, rec)
	}
	return out, total, rows.Err()
}
