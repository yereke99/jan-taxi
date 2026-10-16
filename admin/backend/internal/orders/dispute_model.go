package orders

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Dispute statuses and outcomes.
const (
	DisputeOpen     = "open"
	DisputeResolved = "resolved"
)

// AdminRef identifies an administrator (openapi AdminRef).
type AdminRef struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	FullName string    `json:"full_name"`
}

// Dispute is an order dispute as shown to operators (openapi Dispute).
type Dispute struct {
	ID             uuid.UUID  `json:"id"`
	OrderID        uuid.UUID  `json:"order_id"`
	OrderNumber    int64      `json:"order_number"`
	Status         string     `json:"status"`
	OpenedByType   string     `json:"opened_by_type"`
	OpenedByID     *uuid.UUID `json:"opened_by_id"`
	Reason         string     `json:"reason"`
	PreviousStatus string     `json:"previous_status"`
	Outcome        *string    `json:"outcome"`
	Resolution     *string    `json:"resolution"`
	ResolvedBy     *AdminRef  `json:"resolved_by"`
	CreatedAt      time.Time  `json:"created_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
}

// OrderDisputeInfo is the dispute as shown to the passenger (openapi OrderDisputeInfo).
type OrderDisputeInfo struct {
	Status     string     `json:"status"`
	Reason     string     `json:"reason"`
	Outcome    *string    `json:"outcome"`
	Resolution *string    `json:"resolution"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
}

func (d Dispute) info() *OrderDisputeInfo {
	return &OrderDisputeInfo{Status: d.Status, Reason: d.Reason, Outcome: d.Outcome, Resolution: d.Resolution,
		CreatedAt: d.CreatedAt, ResolvedAt: d.ResolvedAt}
}

const disputeColumns = `
	ds.id, ds.order_id, o.number, ds.status, ds.opened_by_type, ds.opened_by_id, ds.reason, ds.previous_status,
	ds.outcome, ds.resolution, a.id, a.email::text, a.full_name, ds.created_at, ds.resolved_at`

const disputeFrom = `
	FROM disputes ds
	JOIN orders o ON o.id = ds.order_id
	LEFT JOIN admin_users a ON a.id = ds.resolved_by`

func scanDispute(row scanner) (Dispute, error) {
	var d Dispute
	var aID *uuid.UUID
	var aEmail, aName *string
	err := row.Scan(&d.ID, &d.OrderID, &d.OrderNumber, &d.Status, &d.OpenedByType, &d.OpenedByID, &d.Reason,
		&d.PreviousStatus, &d.Outcome, &d.Resolution, &aID, &aEmail, &aName, &d.CreatedAt, &d.ResolvedAt)
	if err != nil {
		return Dispute{}, err
	}
	utc(&d.CreatedAt, d.ResolvedAt)
	if aID != nil {
		d.ResolvedBy = &AdminRef{ID: *aID, Email: str(aEmail), FullName: str(aName)}
	}
	return d, nil
}

// loadLatestDispute returns the order's most recent dispute, or nil.
func loadLatestDispute(ctx context.Context, q db.Querier, orderID uuid.UUID) (*Dispute, error) {
	d, err := scanDispute(q.QueryRow(ctx, `SELECT `+disputeColumns+disputeFrom+`
		WHERE ds.order_id = $1 ORDER BY ds.created_at DESC, ds.id DESC LIMIT 1`, orderID))
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: load dispute: %w", err)
	}
	return &d, nil
}

func loadDispute(ctx context.Context, q db.Querier, id uuid.UUID, lock bool) (Dispute, error) {
	sql := `SELECT ` + disputeColumns + disputeFrom + ` WHERE ds.id = $1`
	if lock {
		sql += ` FOR UPDATE OF ds`
	}
	d, err := scanDispute(q.QueryRow(ctx, sql, id))
	if db.IsNoRows(err) {
		return Dispute{}, httpx.NotFound()
	}
	if err != nil {
		return Dispute{}, fmt.Errorf("orders: load dispute: %w", err)
	}
	return d, nil
}

func listDisputes(ctx context.Context, q db.Querier, status string, page httpx.PageParams) ([]Dispute, int, error) {
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM disputes ds WHERE ($1 = '' OR ds.status = $1)`, status).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("orders: count disputes: %w", err)
	}
	rows, err := q.Query(ctx, `SELECT `+disputeColumns+disputeFrom+`
		WHERE ($1 = '' OR ds.status = $1)
		ORDER BY ds.created_at DESC, ds.id DESC
		LIMIT $2 OFFSET $3`, status, page.PageSize, page.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("orders: list disputes: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Dispute, error) { return scanDispute(row) })
	if err != nil {
		return nil, 0, fmt.Errorf("orders: list disputes: %w", err)
	}
	return items, total, nil
}

// hasOpenDispute reports whether the order has an open dispute.
func hasOpenDispute(ctx context.Context, q db.Querier, orderID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM disputes WHERE order_id = $1 AND status = 'open')`, orderID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("orders: open dispute: %w", err)
	}
	return ok, nil
}
