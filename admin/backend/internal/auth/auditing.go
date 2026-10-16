package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// AuditEntry mirrors audit.Entry. Package audit imports auth (to read the admin
// from the context), so auth receives the audit writer as a function instead of
// importing it; the app wires it to audit.Log.
type AuditEntry struct {
	Action     string
	EntityType string
	EntityID   string
	Before     any
	After      any
	AdminID    *uuid.UUID // overrides the admin from the context (login, CLI)
}

// AuditFunc writes one audit row inside the caller's transaction.
type AuditFunc func(ctx context.Context, q db.Querier, r *http.Request, e AuditEntry) error
