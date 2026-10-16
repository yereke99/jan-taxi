package botapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Lease TTL bounds accepted from the bots (OpenAPI BotLeaseRequest).
const (
	minLeaseTTL       = 10
	maxLeaseTTL       = 120
	maxInstanceIDSize = 128
)

type leaseRequest struct {
	InstanceID string `json:"instance_id"`
	TTLSeconds int    `json:"ttl_s"`
}

// Lease is the polling lease state returned to a bot instance.
type Lease struct {
	Acquired       bool       `json:"acquired"`
	LastUpdateID   int64      `json:"last_update_id"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at"`
}

func validInstanceID(id string) bool {
	return id != "" && len(id) <= maxInstanceIDSize && strings.TrimSpace(id) == id
}

// acquireLease grants or renews the single getUpdates lease of a bot. The
// conditional UPDATE is atomic, so two instances can never both hold it.
func (h *Handler) acquireLease(w http.ResponseWriter, r *http.Request) error {
	var req leaseRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	if !validInstanceID(req.InstanceID) {
		fields["instance_id"] = "invalid"
	}
	if req.TTLSeconds < minLeaseTTL || req.TTLSeconds > maxLeaseTTL {
		fields["ttl_s"] = "invalid"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	res, err := AcquireLease(r.Context(), h.pool, chi.URLParam(r, "bot"), req.InstanceID, time.Duration(req.TTLSeconds)*time.Second)
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}

// AcquireLease takes the lease for holder when it is free, expired or already
// held by holder, and reports the stored offset either way.
func AcquireLease(ctx context.Context, q db.Querier, bot, holder string, ttl time.Duration) (Lease, error) {
	var res Lease
	var expires time.Time
	err := q.QueryRow(ctx, `
		UPDATE bot_states
		SET lease_holder = $2,
		    lease_expires_at = now() + make_interval(secs => $3),
		    updated_at = now()
		WHERE bot = $1
		  AND (lease_holder IS NULL OR lease_holder = $2 OR lease_expires_at IS NULL OR lease_expires_at < now())
		RETURNING last_update_id, lease_expires_at`, bot, holder, ttl.Seconds()).Scan(&res.LastUpdateID, &expires)
	switch {
	case err == nil:
		res.Acquired = true
		res.LeaseExpiresAt = &expires
		return res, nil
	case !db.IsNoRows(err):
		return res, fmt.Errorf("acquire bot lease: %w", err)
	}
	err = q.QueryRow(ctx, `SELECT last_update_id, lease_expires_at FROM bot_states WHERE bot = $1`, bot).
		Scan(&res.LastUpdateID, &res.LeaseExpiresAt)
	if db.IsNoRows(err) {
		return res, httpx.NotFound()
	}
	if err != nil {
		return res, fmt.Errorf("read bot state: %w", err)
	}
	return res, nil
}

type offsetRequest struct {
	InstanceID   string `json:"instance_id"`
	LastUpdateID int64  `json:"last_update_id"`
}

type offsetResponse struct {
	LastUpdateID int64 `json:"last_update_id"`
}

// saveOffset stores the last processed update id. Only the lease holder may
// write and the value never moves backwards.
func (h *Handler) saveOffset(w http.ResponseWriter, r *http.Request) error {
	var req offsetRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	if !validInstanceID(req.InstanceID) {
		fields["instance_id"] = "invalid"
	}
	if req.LastUpdateID < 0 {
		fields["last_update_id"] = "invalid"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	var res offsetResponse
	err := h.pool.QueryRow(r.Context(), `
		UPDATE bot_states
		SET last_update_id = GREATEST(last_update_id, $3), last_poll_at = now(), updated_at = now()
		WHERE bot = $1 AND lease_holder = $2
		RETURNING last_update_id`, chi.URLParam(r, "bot"), req.InstanceID, req.LastUpdateID).Scan(&res.LastUpdateID)
	if db.IsNoRows(err) {
		return httpx.Conflict("conflict", "instance does not hold the polling lease").
			WithDetails(map[string]any{"reason": "not_lease_holder"})
	}
	if err != nil {
		return fmt.Errorf("save bot offset: %w", err)
	}
	return httpx.OK(w, res)
}

type claimResponse struct {
	Claimed bool `json:"claimed"`
}

// claimUpdate records an update id before the bot acts on it; only the first
// claim succeeds, so a restarted bot never processes an update twice.
func (h *Handler) claimUpdate(w http.ResponseWriter, r *http.Request) error {
	updateID, err := int64Param(r, "update_id", 0)
	if err != nil {
		return err
	}
	claimed, err := ClaimUpdate(r.Context(), h.pool, chi.URLParam(r, "bot"), updateID)
	if err != nil {
		return err
	}
	return httpx.OK(w, claimResponse{Claimed: claimed})
}

// ClaimUpdate inserts the claim row and reports whether this call created it.
func ClaimUpdate(ctx context.Context, q db.Querier, bot string, updateID int64) (bool, error) {
	tag, err := q.Exec(ctx, `
		INSERT INTO bot_processed_updates (bot, update_id) VALUES ($1, $2)
		ON CONFLICT (bot, update_id) DO NOTHING`, bot, updateID)
	if err != nil {
		return false, fmt.Errorf("claim bot update: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CleanupProcessedUpdates deletes update claims older than olderThan and
// returns how many were removed. Telegram keeps undelivered updates for at
// most 24 hours, so claims older than that can never be needed again.
func CleanupProcessedUpdates(ctx context.Context, q db.Querier, olderThan time.Duration) (int64, error) {
	tag, err := q.Exec(ctx, `DELETE FROM bot_processed_updates WHERE processed_at < now() - make_interval(secs => $1)`,
		olderThan.Seconds())
	if err != nil {
		return 0, fmt.Errorf("delete processed bot updates: %w", err)
	}
	return tag.RowsAffected(), nil
}
