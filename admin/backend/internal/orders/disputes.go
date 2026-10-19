package orders

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// Dispute outcomes (disputes.outcome).
const (
	OutcomeCompleted = "completed"
	OutcomeCancelled = "cancelled"
)

// maxCancelReason is the orders.cancel_reason limit.
const maxCancelReason = 200

// openDispute records a dispute for an in-progress or completed order (the
// caller has checked who may open it and when), moves the order to disputed
// keeping the previous status on the dispute, and tells the other parties.
func openDispute(ctx context.Context, tx pgx.Tx, o lifecycle.Core, actor lifecycle.Actor, reason string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO disputes (order_id, opened_by_type, opened_by_id, reason, previous_status)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, o.ID, actor.Type, actor.ID, reason, o.Status).Scan(&id)
	if db.IsUniqueViolation(err, lifecycle.IdxOneOpenDispute) {
		return uuid.Nil, lifecycle.ConflictReason("dispute_open", "order already has an open dispute")
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("orders: insert dispute: %w", err)
	}
	_, after, err := lifecycle.Apply(ctx, tx, lifecycle.Change{
		OrderID: o.ID, From: []string{lifecycle.StatusInProgress, lifecycle.StatusCompleted},
		To: lifecycle.StatusDisputed, Action: "dispute", Actor: actor, Reason: &reason,
		Meta: map[string]any{"dispute_id": id.String()},
	})
	if err != nil {
		return uuid.Nil, err
	}
	params := map[string]any{"order_id": after.ID.String(), "order_number": after.Number}
	key := "dispute_opened:" + id.String() + ":"
	if actor.Type != lifecycle.ActorPassenger {
		if err := lifecycle.Notify(ctx, tx, notifications.PassengerBot, after.PassengerID, notifications.TplDisputeOpened,
			params, key+string(notifications.PassengerBot)); err != nil {
			return uuid.Nil, err
		}
	}
	if after.DriverID != nil {
		info, err := lifecycle.LoadDriverInfo(ctx, tx, *after.DriverID)
		if err != nil {
			return uuid.Nil, err
		}
		if err := lifecycle.Notify(ctx, tx, notifications.DriverBot, info.UserID, notifications.TplDisputeOpened,
			params, key+string(notifications.DriverBot)); err != nil {
			return uuid.Nil, err
		}
	}
	if err := lifecycle.Publish(ctx, tx, realtime.OrderDisputed,
		lifecycle.EventData(after, map[string]any{"dispute_id": id.String()}), lifecycle.PartyScopes(after)...); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// resolveInput is an operator's decision on an open dispute.
type resolveInput struct {
	DisputeID        uuid.UUID
	AdminID          uuid.UUID
	Outcome          string
	Resolution       string
	RefundCommission bool
}

// resolveResult reports the money moved by a resolution.
type resolveResult struct {
	Before, After      Dispute
	Charged, Released  int64
	Refunded           int64
	OrderBefore, Order lifecycle.Core
}

// resolveDispute closes an open dispute and moves the order to the chosen
// outcome. Commission: it was charged when the driver took the order
// (§22.2) and is refunded once only when resolved cancelled with
// refund_commission. An order accepted under the earlier model that was
// disputed while in progress is settled when resolved completed and released
// when resolved cancelled.
func resolveDispute(ctx context.Context, tx pgx.Tx, in resolveInput) (resolveResult, error) {
	before, err := loadDispute(ctx, tx, in.DisputeID, true)
	if err != nil {
		return resolveResult{}, err
	}
	if before.Status != DisputeOpen {
		return resolveResult{}, lifecycle.InvalidTransition(before.Status, "resolve")
	}
	o, err := lifecycle.LoadCore(ctx, tx, before.OrderID, true)
	if err != nil {
		return resolveResult{}, err
	}
	if o.Status != lifecycle.StatusDisputed {
		return resolveResult{}, lifecycle.InvalidTransition(o.Status, "resolve")
	}
	res := resolveResult{Before: before, OrderBefore: o}
	actor := lifecycle.Admin(in.AdminID)
	wasCompleted := before.PreviousStatus == lifecycle.StatusCompleted
	meta := map[string]any{"dispute_id": before.ID.String(), "outcome": in.Outcome}

	switch in.Outcome {
	case OutcomeCompleted:
		_, after, err := lifecycle.Apply(ctx, tx, lifecycle.Change{
			OrderID: o.ID, From: []string{lifecycle.StatusDisputed}, To: lifecycle.StatusCompleted,
			Action: "resolve", Actor: actor, Reason: &in.Resolution, Meta: meta,
		})
		if err != nil {
			return resolveResult{}, err
		}
		res.Order = after
		if !wasCompleted && after.DriverID != nil {
			if res.Charged, err = finishCompleted(ctx, tx, after, false); err != nil {
				return resolveResult{}, err
			}
		} else if err := lifecycle.Publish(ctx, tx, realtime.OrderCompleted, lifecycle.EventData(after, nil),
			lifecycle.PartyScopes(after)...); err != nil {
			return resolveResult{}, err
		}
	case OutcomeCancelled:
		reason := truncateRunes(in.Resolution, maxCancelReason)
		cres, err := lifecycle.Cancel(ctx, tx, lifecycle.CancelInput{Change: lifecycle.Change{
			OrderID: o.ID, From: []string{lifecycle.StatusDisputed}, Action: "resolve", Actor: actor,
			Reason: &reason, Meta: meta,
		}})
		if err != nil {
			return resolveResult{}, err
		}
		res.Order, res.Released = cres.After, cres.Released
		if wasCompleted && cres.After.DriverID != nil {
			if _, err := tx.Exec(ctx, `
				UPDATE driver_profiles SET completed_trips = GREATEST(completed_trips - 1, 0) WHERE id = $1`,
				*cres.After.DriverID); err != nil {
				return resolveResult{}, err
			}
		}
		if in.RefundCommission && cres.After.DriverID != nil {
			res.Refunded, err = wallets.RefundCommission(ctx, tx, o.ID, *cres.After.DriverID, in.AdminID)
			if err != nil {
				return resolveResult{}, lifecycle.DBError(err)
			}
			if res.Refunded > 0 && cres.After.DriverID != nil {
				if err := lifecycle.PublishWallet(ctx, tx, *cres.After.DriverID, o.ID, "commission_refund"); err != nil {
					return resolveResult{}, err
				}
			}
		}
	default:
		return resolveResult{}, httpx.Validation(map[string]string{"outcome": "invalid"})
	}

	if _, err := tx.Exec(ctx, `
		UPDATE disputes SET status = 'resolved', outcome = $2, resolution = $3, resolved_by = $4, resolved_at = now()
		WHERE id = $1 AND status = 'open'`, before.ID, in.Outcome, in.Resolution, in.AdminID); err != nil {
		return resolveResult{}, fmt.Errorf("orders: resolve dispute: %w", err)
	}
	params := map[string]any{"order_id": res.Order.ID.String(), "order_number": res.Order.Number,
		"resolution": in.Resolution}
	key := "dispute_resolved:" + before.ID.String() + ":"
	if err := lifecycle.Notify(ctx, tx, notifications.PassengerBot, res.Order.PassengerID, notifications.TplDisputeResolved,
		params, key+string(notifications.PassengerBot)); err != nil {
		return resolveResult{}, err
	}
	if res.Order.DriverID != nil {
		info, err := lifecycle.LoadDriverInfo(ctx, tx, *res.Order.DriverID)
		if err != nil {
			return resolveResult{}, err
		}
		if err := lifecycle.Notify(ctx, tx, notifications.DriverBot, info.UserID, notifications.TplDisputeResolved,
			params, key+string(notifications.DriverBot)); err != nil {
			return resolveResult{}, err
		}
	}
	if res.After, err = loadDispute(ctx, tx, before.ID, false); err != nil {
		return resolveResult{}, err
	}
	return res, nil
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		r = r[:max]
	}
	return string(r)
}
