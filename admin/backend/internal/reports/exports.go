package reports

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// exportDefaultDays is the period exported when no bounds are given.
const exportDefaultDays = 30

var (
	orderStatuses = []string{"draft", "searching", "accepted", "driver_arriving", "in_progress", "completed", "cancelled", "disputed"}
	ledgerTypes   = []string{"topup_credit", "topup_refund", "commission_reserve", "commission_release",
		"commission_charge", "commission_refund", "adjustment_credit", "adjustment_debit"}
	topUpStatuses = []string{"created", "pending_verification", "confirmed", "rejected", "expired", "refunded"}
)

// enumParam reads an optional query value that must be one of allowed.
func enumParam(r *http.Request, name string, allowed []string) (*string, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil, nil
	}
	for _, a := range allowed {
		if a == v {
			return &v, nil
		}
	}
	return nil, httpx.Validation(map[string]string{name: "invalid"})
}

func filename(kind string, p Period) string {
	return fmt.Sprintf("%s-%s-%s.csv", kind, p.From.In(p.Loc).Format(time.DateOnly), p.To.In(p.Loc).Format(time.DateOnly))
}

// export validates nothing itself: it audits the export, then streams rows
// produced by query. Errors after the header has been sent are only logged.
func (h *Handler) export(w http.ResponseWriter, r *http.Request, name string, p Period, filters map[string]any,
	header []string, query string, args []any, row func(pgx.Rows) ([]Cell, error)) error {
	ctx := r.Context()
	filters["from"], filters["to"] = p.From.UTC(), p.To.UTC()
	if err := audit.Log(ctx, h.pool, r, audit.Entry{
		Action: "report.export", EntityType: "report", EntityID: name, After: filters,
	}); err != nil {
		return err
	}
	rows, err := h.pool.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("reports: export %s: %w", name, err)
	}
	defer rows.Close()

	startCSV(w, filename(strings.TrimSuffix(name, ".csv"), p))
	cw, err := NewCSVWriter(w, header...)
	if err != nil {
		return logStreamError(ctx, name, err)
	}
	for rows.Next() {
		cells, err := row(rows)
		if err != nil {
			return logStreamError(ctx, name, err)
		}
		if err := cw.Row(cells...); err != nil {
			return logStreamError(ctx, name, err)
		}
	}
	if err := rows.Err(); err != nil {
		return logStreamError(ctx, name, err)
	}
	return logStreamError(ctx, name, cw.Close())
}

// logStreamError logs a failure after the response has started; the client
// sees a truncated file.
func logStreamError(ctx context.Context, name string, err error) error {
	if err != nil {
		httpx.Logger(ctx).Error("csv export interrupted", slog.String("report", name), slog.Any("err", err))
	}
	return nil
}

func (h *Handler) exportOrders(w http.ResponseWriter, r *http.Request) error {
	cityID, err := httpx.QueryUUID(r, "city_id")
	if err != nil {
		return err
	}
	status, err := enumParam(r, "status", orderStatuses)
	if err != nil {
		return err
	}
	loc, err := cityLocation(r.Context(), h.pool, cityID)
	if err != nil {
		return err
	}
	p, err := parsePeriod(r, loc, h.now(), exportDefaultDays, MaxExportDays)
	if err != nil {
		return err
	}
	header := []string{"number", "id", "created_at", "city", "locality", "category", "status", "fare", "commission", "currency",
		"payment_method", "distance_m", "duration_s", "stops", "pickup_address", "destination_address", "driver_code",
		"accepted_at", "started_at", "completed_at", "cancelled_at", "cancelled_by", "cancel_reason"}
	const query = `
		SELECT o.number, o.id, o.created_at, c.code, o.locality, o.category, o.status, o.fare, o.commission_amount, o.currency,
		       o.payment_method, o.distance_m, o.duration_s, o.stop_count,
		       (SELECT s.address FROM order_stops s WHERE s.order_id = o.id ORDER BY s.seq LIMIT 1),
		       (SELECT s.address FROM order_stops s WHERE s.order_id = o.id ORDER BY s.seq DESC LIMIT 1),
		       d.code, o.accepted_at, o.started_at, o.completed_at, o.cancelled_at, o.cancelled_by, o.cancel_reason
		FROM orders o JOIN cities c ON c.id = o.city_id LEFT JOIN driver_profiles d ON d.id = o.driver_id
		WHERE o.status <> 'draft' AND o.created_at >= $1 AND o.created_at < $2
		  AND ($3::uuid IS NULL OR o.city_id = $3) AND ($4::text IS NULL OR o.status = $4)
		ORDER BY o.created_at, o.number`
	filters := map[string]any{"city_id": cityID, "status": status}
	return h.export(w, r, "orders.csv", p, filters, header, query, []any{p.From, p.To, cityID, status},
		func(rows pgx.Rows) ([]Cell, error) {
			var (
				number, fare, commission, stops                  int64
				distance, duration                               *int64
				id                                               uuid.UUID
				created                                          time.Time
				city, category, st, currency, payment            string
				pickup, destination, driver, cancelledBy, reason *string
				locality                                         *string
				accepted, started, completed, cancelled          *time.Time
			)
			if err := rows.Scan(&number, &id, &created, &city, &locality, &category, &st, &fare, &commission, &currency, &payment,
				&distance, &duration, &stops, &pickup, &destination, &driver, &accepted, &started, &completed,
				&cancelled, &cancelledBy, &reason); err != nil {
				return nil, err
			}
			return []Cell{Int(number), Text(id.String()), Time(&created, loc), Text(city), TextPtr(locality), Text(category), Text(st),
				Int(fare), Int(commission), Text(currency), Text(payment), nullableInt(distance), nullableInt(duration), Int(stops),
				TextPtr(pickup), TextPtr(destination), TextPtr(driver), Time(accepted, loc), Time(started, loc),
				Time(completed, loc), Time(cancelled, loc), TextPtr(cancelledBy), TextPtr(reason)}, nil
		})
}

func (h *Handler) exportLedger(w http.ResponseWriter, r *http.Request) error {
	driverID, err := httpx.QueryUUID(r, "driver_id")
	if err != nil {
		return err
	}
	typ, err := enumParam(r, "type", ledgerTypes)
	if err != nil {
		return err
	}
	loc := loadLocation(DefaultTimezone)
	p, err := parsePeriod(r, loc, h.now(), exportDefaultDays, MaxExportDays)
	if err != nil {
		return err
	}
	header := []string{"id", "created_at", "driver_code", "entry_type", "balance_delta", "reserved_delta",
		"balance_after", "reserved_after", "currency", "order_number", "topup_reference", "admin_email",
		"idempotency_key", "description"}
	const query = `
		SELECT l.id, l.created_at, d.code, l.entry_type, l.balance_delta, l.reserved_delta, l.balance_after,
		       l.reserved_after, w.currency, o.number, t.reference, a.email::text, l.idempotency_key, l.description
		FROM wallet_ledger l
		JOIN wallets w ON w.id = l.wallet_id
		JOIN driver_profiles d ON d.id = w.driver_id
		LEFT JOIN orders o ON o.id = l.order_id
		LEFT JOIN top_up_requests t ON t.id = l.topup_id
		LEFT JOIN admin_users a ON a.id = l.admin_id
		WHERE l.created_at >= $1 AND l.created_at < $2
		  AND ($3::uuid IS NULL OR w.driver_id = $3) AND ($4::text IS NULL OR l.entry_type = $4)
		ORDER BY l.id`
	filters := map[string]any{"driver_id": driverID, "type": typ}
	return h.export(w, r, "ledger.csv", p, filters, header, query, []any{p.From, p.To, driverID, typ},
		func(rows pgx.Rows) ([]Cell, error) {
			var (
				id, bd, rd, ba, ra                 int64
				created                            time.Time
				code, entryType, currency, key     string
				orderNumber                        *int64
				reference, adminEmail, description *string
			)
			if err := rows.Scan(&id, &created, &code, &entryType, &bd, &rd, &ba, &ra, &currency, &orderNumber,
				&reference, &adminEmail, &key, &description); err != nil {
				return nil, err
			}
			return []Cell{Int(id), Time(&created, loc), Text(code), Text(entryType), Int(bd), Int(rd), Int(ba), Int(ra),
				Text(currency), IntPtr(orderNumber), TextPtr(reference), TextPtr(adminEmail), Text(key),
				TextPtr(description)}, nil
		})
}

func (h *Handler) exportTopUps(w http.ResponseWriter, r *http.Request) error {
	status, err := enumParam(r, "status", topUpStatuses)
	if err != nil {
		return err
	}
	loc := loadLocation(DefaultTimezone)
	p, err := parsePeriod(r, loc, h.now(), exportDefaultDays, MaxExportDays)
	if err != nil {
		return err
	}
	header := []string{"number", "reference", "created_at", "driver_code", "status", "provider", "requested_amount",
		"verified_amount", "currency", "provider_transaction_id", "paid_at", "driver_payment_reference",
		"driver_reported_paid_at", "confirmed_at", "confirmed_by", "rejected_at", "reject_reason", "refunded_at",
		"refund_reason", "expires_at"}
	const query = `
		SELECT t.number, t.reference, t.created_at, d.code, t.status, t.provider, t.requested_amount, t.verified_amount,
		       t.currency, t.provider_transaction_id, t.paid_at, t.driver_payment_reference, t.driver_reported_paid_at,
		       t.confirmed_at, ca.email::text, t.rejected_at, t.reject_reason, t.refunded_at, t.refund_reason, t.expires_at
		FROM top_up_requests t
		JOIN driver_profiles d ON d.id = t.driver_id
		LEFT JOIN admin_users ca ON ca.id = t.confirmed_by
		WHERE t.created_at >= $1 AND t.created_at < $2 AND ($3::text IS NULL OR t.status = $3)
		ORDER BY t.created_at, t.number`
	filters := map[string]any{"status": status}
	return h.export(w, r, "topups.csv", p, filters, header, query, []any{p.From, p.To, status},
		func(rows pgx.Rows) ([]Cell, error) {
			var (
				number, requested                                       int64
				verified                                                *int64
				reference, code, st, provider, currency                 string
				created, expires                                        time.Time
				txn, driverRef, confirmedBy, rejectReason, refundReason *string
				paid, reported, confirmed, rejected, refunded           *time.Time
			)
			if err := rows.Scan(&number, &reference, &created, &code, &st, &provider, &requested, &verified, &currency,
				&txn, &paid, &driverRef, &reported, &confirmed, &confirmedBy, &rejected, &rejectReason, &refunded,
				&refundReason, &expires); err != nil {
				return nil, err
			}
			return []Cell{Int(number), Text(reference), Time(&created, loc), Text(code), Text(st), Text(provider),
				Int(requested), IntPtr(verified), Text(currency), TextPtr(txn), Time(paid, loc), TextPtr(driverRef),
				Time(reported, loc), Time(confirmed, loc), TextPtr(confirmedBy), Time(rejected, loc),
				TextPtr(rejectReason), Time(refunded, loc), TextPtr(refundReason), Time(&expires, loc)}, nil
		})
}

func nullableInt(v *int64) Cell {
	if v == nil {
		return Text("")
	}
	return Int(*v)
}
