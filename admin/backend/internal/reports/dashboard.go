package reports

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Dashboard is GET /admin/dashboard (OpenAPI DashboardMetrics plus timezone,
// cities, today and extra counters). Period counters cover [From, To); fields
// ending in _now and the point-in-time sections ignore the period.
type Dashboard struct {
	From          time.Time          `json:"from"`
	To            time.Time          `json:"to"`
	CityID        *uuid.UUID         `json:"city_id"`
	Timezone      string             `json:"timezone"`
	Orders        OrderMetrics       `json:"orders"`
	Revenue       Revenue            `json:"revenue"`
	Drivers       DriverMetrics      `json:"drivers"`
	TopUps        TopUpMetrics       `json:"topups"`
	Wallets       WalletTotals       `json:"wallets"`
	Performance   Performance        `json:"performance"`
	Notifications NotificationCounts `json:"notifications"`
	Cities        CityCounts         `json:"cities"`
	Today         Summary            `json:"today"`
	Daily         []DailyRow         `json:"daily"`
}

// OrderMetrics counts orders. Created/by_category use created_at, completed
// uses completed_at, cancelled uses cancelled_at, disputed counts disputes
// opened in the period.
type OrderMetrics struct {
	Created              int           `json:"created"`
	Completed            int           `json:"completed"`
	Cancelled            int           `json:"cancelled"`
	CancelledByPassenger int           `json:"cancelled_by_passenger"`
	CancelledByDriver    int           `json:"cancelled_by_driver"`
	CancelledByAdmin     int           `json:"cancelled_by_admin"`
	NoDriverFound        int           `json:"no_driver_found"`
	Disputed             int           `json:"disputed"`
	SearchingNow         int           `json:"searching_now"`
	ActiveNow            int           `json:"active_now"`
	ByCategory           CategoryCount `json:"by_category"`
}

// CategoryCount splits created orders by category.
type CategoryCount struct {
	Taxi   int `json:"taxi"`
	Parcel int `json:"parcel"`
	Other  int `json:"other"`
}

// Revenue is the platform's money in the period: GMV of completed orders and
// commission charged minus commission refunded (from the ledger).
type Revenue struct {
	GMV                int64  `json:"gmv"`
	CommissionCharged  int64  `json:"commission_charged"`
	CommissionRefunded int64  `json:"commission_refunded"`
	CommissionNet      int64  `json:"commission_net"`
	Currency           string `json:"currency"`
}

// DriverMetrics are point-in-time driver counts plus registrations in the period.
type DriverMetrics struct {
	Total               int `json:"total"`
	Approved            int `json:"approved"`
	OnlineNow           int `json:"online_now"`
	OfflineNow          int `json:"offline_now"`
	PendingReview       int `json:"pending_review"`
	PendingApplications int `json:"pending_applications"`
	ChangesRequested    int `json:"changes_requested"`
	Suspended           int `json:"suspended"`
	RegisteredInPeriod  int `json:"registered_in_period"`
}

// TopUpMetrics: pending verification now; confirmed and refunded in the period.
type TopUpMetrics struct {
	PendingVerificationCount  int   `json:"pending_verification_count"`
	PendingVerificationAmount int64 `json:"pending_verification_amount"`
	ConfirmedCount            int   `json:"confirmed_count"`
	ConfirmedAmount           int64 `json:"confirmed_amount"`
	RefundedCount             int   `json:"refunded_count"`
	RefundedAmount            int64 `json:"refunded_amount"`
}

// WalletTotals sums driver wallets now.
type WalletTotals struct {
	TotalBalance  int64 `json:"total_balance"`
	TotalReserved int64 `json:"total_reserved"`
}

// Performance averages over the period; nil when there is no data.
type Performance struct {
	AvgAcceptTimeS    *int `json:"avg_accept_time_s"`
	AvgTripDurationS  *int `json:"avg_trip_duration_s"`
	CompletionRateBps *int `json:"completion_rate_bps"`
}

// NotificationCounts are outbox rows waiting to be sent and given up on.
type NotificationCounts struct {
	Pending int `json:"pending"`
	Dead    int `json:"dead"`
}

// CityCounts counts configured cities.
type CityCounts struct {
	Total  int `json:"total"`
	Active int `json:"active"`
}

// Summary is the compact set of period counters shown for today.
type Summary struct {
	From                  time.Time `json:"from"`
	To                    time.Time `json:"to"`
	OrdersCreated         int       `json:"orders_created"`
	OrdersCompleted       int       `json:"orders_completed"`
	OrdersCancelled       int       `json:"orders_cancelled"`
	OrdersDisputed        int       `json:"orders_disputed"`
	GMV                   int64     `json:"gmv"`
	CommissionCharged     int64     `json:"commission_charged"`
	CommissionRefunded    int64     `json:"commission_refunded"`
	CommissionNet         int64     `json:"commission_net"`
	TopUpsConfirmedCount  int       `json:"topups_confirmed_count"`
	TopUpsConfirmedAmount int64     `json:"topups_confirmed_amount"`
}

// DailyRow is one local day of the period.
type DailyRow struct {
	Date                  string `json:"date"`
	OrdersCreated         int    `json:"orders_created"`
	OrdersCompleted       int    `json:"orders_completed"`
	GMV                   int64  `json:"gmv"`
	Commission            int64  `json:"commission"`
	TopUpsConfirmedCount  int    `json:"topups_confirmed_count"`
	TopUpsConfirmedAmount int64  `json:"topups_confirmed_amount"`
}

// periodMetrics are the counters that depend on the period.
type periodMetrics struct {
	orders             OrderMetrics
	revenue            Revenue
	topups             TopUpMetrics
	createdCompleted   int
	avgAccept, avgTrip *float64
	registeredDrivers  int
}

// BuildDashboard computes the dashboard for p (and today, by now) in one
// consistent snapshot when q is a repeatable-read transaction.
func BuildDashboard(ctx context.Context, q db.Querier, p Period, cityID *uuid.UUID, now time.Time) (Dashboard, error) {
	d := Dashboard{From: p.From.UTC(), To: p.To.UTC(), CityID: cityID, Timezone: p.Loc.String()}
	pm, err := loadPeriod(ctx, q, p.From, p.To, cityID)
	if err != nil {
		return Dashboard{}, err
	}
	d.Orders, d.Revenue, d.TopUps = pm.orders, pm.revenue, pm.topups
	d.Drivers.RegisteredInPeriod = pm.registeredDrivers
	d.Performance = Performance{AvgAcceptTimeS: roundSeconds(pm.avgAccept), AvgTripDurationS: roundSeconds(pm.avgTrip)}
	if pm.orders.Created > 0 {
		bps := (pm.createdCompleted*10000 + pm.orders.Created/2) / pm.orders.Created
		d.Performance.CompletionRateBps = &bps
	}

	today := Today(now, p.Loc)
	tm, err := loadPeriod(ctx, q, today.From, today.To, cityID)
	if err != nil {
		return Dashboard{}, err
	}
	d.Today = Summary{
		From: today.From.UTC(), To: today.To.UTC(),
		OrdersCreated: tm.orders.Created, OrdersCompleted: tm.orders.Completed, OrdersCancelled: tm.orders.Cancelled,
		OrdersDisputed: tm.orders.Disputed, GMV: tm.revenue.GMV, CommissionCharged: tm.revenue.CommissionCharged,
		CommissionRefunded: tm.revenue.CommissionRefunded, CommissionNet: tm.revenue.CommissionNet,
		TopUpsConfirmedCount: tm.topups.ConfirmedCount, TopUpsConfirmedAmount: tm.topups.ConfirmedAmount,
	}
	if err := loadCurrent(ctx, q, cityID, &d); err != nil {
		return Dashboard{}, err
	}
	if d.Daily, err = loadDaily(ctx, q, p, cityID); err != nil {
		return Dashboard{}, err
	}
	return d, nil
}

func roundSeconds(v *float64) *int {
	if v == nil {
		return nil
	}
	n := int(math.Round(*v))
	return &n
}

// Shared SQL fragments; $1 from, $2 to, $3 city id (nullable).
const inPeriod = ` >= $1 AND %s < $2`

func period(col string) string { return col + fmt.Sprintf(inPeriod, col) }

func loadPeriod(ctx context.Context, q db.Querier, from, to time.Time, cityID *uuid.UUID) (periodMetrics, error) {
	var m periodMetrics
	created, completed, cancelled := period("created_at"), "status = 'completed' AND "+period("completed_at"),
		"status = 'cancelled' AND "+period("cancelled_at")
	err := q.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE `+created+`),
			count(*) FILTER (WHERE `+created+` AND category = 'taxi'),
			count(*) FILTER (WHERE `+created+` AND category = 'parcel'),
			count(*) FILTER (WHERE `+created+` AND category = 'other'),
			count(*) FILTER (WHERE `+created+` AND status = 'completed'),
			count(*) FILTER (WHERE `+completed+`),
			COALESCE(sum(fare) FILTER (WHERE `+completed+`), 0)::bigint,
			count(*) FILTER (WHERE `+cancelled+`),
			count(*) FILTER (WHERE `+cancelled+` AND cancelled_by = 'passenger'),
			count(*) FILTER (WHERE `+cancelled+` AND cancelled_by = 'driver'),
			count(*) FILTER (WHERE `+cancelled+` AND cancelled_by = 'admin'),
			count(*) FILTER (WHERE `+cancelled+` AND cancelled_by = 'system'),
			(avg(EXTRACT(EPOCH FROM accepted_at - created_at)) FILTER (WHERE `+period("accepted_at")+`))::float8,
			(avg(EXTRACT(EPOCH FROM completed_at - started_at)) FILTER (WHERE `+completed+` AND started_at IS NOT NULL))::float8
		FROM orders
		WHERE status <> 'draft' AND ($3::uuid IS NULL OR city_id = $3)
		  AND ((`+period("created_at")+`) OR (`+period("completed_at")+`)
		       OR (`+period("cancelled_at")+`) OR (`+period("accepted_at")+`))`,
		from, to, cityID).Scan(
		&m.orders.Created, &m.orders.ByCategory.Taxi, &m.orders.ByCategory.Parcel, &m.orders.ByCategory.Other,
		&m.createdCompleted, &m.orders.Completed, &m.revenue.GMV, &m.orders.Cancelled,
		&m.orders.CancelledByPassenger, &m.orders.CancelledByDriver, &m.orders.CancelledByAdmin, &m.orders.NoDriverFound,
		&m.avgAccept, &m.avgTrip)
	if err != nil {
		return m, fmt.Errorf("reports: order metrics: %w", err)
	}
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM disputes ds JOIN orders o ON o.id = ds.order_id
		WHERE ds.created_at >= $1 AND ds.created_at < $2 AND ($3::uuid IS NULL OR o.city_id = $3)`,
		from, to, cityID).Scan(&m.orders.Disputed); err != nil {
		return m, fmt.Errorf("reports: dispute metrics: %w", err)
	}
	if err := q.QueryRow(ctx, `
		SELECT COALESCE(sum(-l.balance_delta) FILTER (WHERE l.entry_type = 'commission_charge'), 0)::bigint,
		       COALESCE(sum(l.balance_delta) FILTER (WHERE l.entry_type = 'commission_refund'), 0)::bigint
		FROM wallet_ledger l LEFT JOIN orders o ON o.id = l.order_id
		WHERE l.entry_type IN ('commission_charge', 'commission_refund')
		  AND l.created_at >= $1 AND l.created_at < $2 AND ($3::uuid IS NULL OR o.city_id = $3)`,
		from, to, cityID).Scan(&m.revenue.CommissionCharged, &m.revenue.CommissionRefunded); err != nil {
		return m, fmt.Errorf("reports: commission metrics: %w", err)
	}
	m.revenue.CommissionNet = m.revenue.CommissionCharged - m.revenue.CommissionRefunded
	m.revenue.Currency = "KZT"
	confirmed := "t.status IN ('confirmed', 'refunded') AND " + period("t.confirmed_at")
	refunded := "t.status = 'refunded' AND " + period("t.refunded_at")
	if err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE `+confirmed+`),
		       COALESCE(sum(t.verified_amount) FILTER (WHERE `+confirmed+`), 0)::bigint,
		       count(*) FILTER (WHERE `+refunded+`),
		       COALESCE(sum(t.verified_amount) FILTER (WHERE `+refunded+`), 0)::bigint
		FROM top_up_requests t JOIN driver_profiles d ON d.id = t.driver_id
		WHERE ($3::uuid IS NULL OR d.city_id = $3) AND ((`+period("t.confirmed_at")+`) OR (`+period("t.refunded_at")+`))`,
		from, to, cityID).Scan(&m.topups.ConfirmedCount, &m.topups.ConfirmedAmount,
		&m.topups.RefundedCount, &m.topups.RefundedAmount); err != nil {
		return m, fmt.Errorf("reports: top-up metrics: %w", err)
	}
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM driver_profiles
		WHERE created_at >= $1 AND created_at < $2 AND ($3::uuid IS NULL OR city_id = $3)`,
		from, to, cityID).Scan(&m.registeredDrivers); err != nil {
		return m, fmt.Errorf("reports: registrations: %w", err)
	}
	return m, nil
}

// loadCurrent fills the point-in-time counters.
func loadCurrent(ctx context.Context, q db.Querier, cityID *uuid.UUID, d *Dashboard) error {
	if err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'searching'),
		       count(*) FILTER (WHERE status IN ('accepted', 'driver_arriving', 'in_progress'))
		FROM orders
		WHERE status IN ('searching', 'accepted', 'driver_arriving', 'in_progress') AND ($1::uuid IS NULL OR city_id = $1)`,
		cityID).Scan(&d.Orders.SearchingNow, &d.Orders.ActiveNow); err != nil {
		return fmt.Errorf("reports: active orders: %w", err)
	}
	if err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status <> 'draft'),
		       count(*) FILTER (WHERE status = 'approved'),
		       count(*) FILTER (WHERE status = 'approved' AND is_online),
		       count(*) FILTER (WHERE status = 'approved' AND NOT is_online),
		       count(*) FILTER (WHERE status = 'pending'),
		       count(*) FILTER (WHERE status = 'changes_requested'),
		       count(*) FILTER (WHERE status = 'suspended'),
		       (SELECT count(*) FROM driver_applications a JOIN driver_profiles ad ON ad.id = a.driver_id
		        WHERE a.status = 'pending' AND ($1::uuid IS NULL OR ad.city_id = $1))
		FROM driver_profiles WHERE ($1::uuid IS NULL OR city_id = $1)`,
		cityID).Scan(&d.Drivers.Total, &d.Drivers.Approved, &d.Drivers.OnlineNow, &d.Drivers.OfflineNow,
		&d.Drivers.PendingReview, &d.Drivers.ChangesRequested, &d.Drivers.Suspended, &d.Drivers.PendingApplications); err != nil {
		return fmt.Errorf("reports: driver metrics: %w", err)
	}
	if err := q.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(t.requested_amount), 0)::bigint
		FROM top_up_requests t JOIN driver_profiles d ON d.id = t.driver_id
		WHERE t.status = 'pending_verification' AND ($1::uuid IS NULL OR d.city_id = $1)`,
		cityID).Scan(&d.TopUps.PendingVerificationCount, &d.TopUps.PendingVerificationAmount); err != nil {
		return fmt.Errorf("reports: pending top-ups: %w", err)
	}
	if err := q.QueryRow(ctx, `
		SELECT COALESCE(sum(w.balance), 0)::bigint, COALESCE(sum(w.reserved), 0)::bigint
		FROM wallets w JOIN driver_profiles d ON d.id = w.driver_id
		WHERE ($1::uuid IS NULL OR d.city_id = $1)`,
		cityID).Scan(&d.Wallets.TotalBalance, &d.Wallets.TotalReserved); err != nil {
		return fmt.Errorf("reports: wallet totals: %w", err)
	}
	if err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status IN ('pending', 'sending')), count(*) FILTER (WHERE status = 'dead')
		FROM notification_outbox WHERE status IN ('pending', 'sending', 'dead')`).
		Scan(&d.Notifications.Pending, &d.Notifications.Dead); err != nil {
		return fmt.Errorf("reports: notification counts: %w", err)
	}
	if err := q.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE is_active) FROM cities`).
		Scan(&d.Cities.Total, &d.Cities.Active); err != nil {
		return fmt.Errorf("reports: city counts: %w", err)
	}
	return nil
}

// loadDaily builds one row per local day; $4 is the IANA zone of day boundaries.
func loadDaily(ctx context.Context, q db.Querier, p Period, cityID *uuid.UUID) ([]DailyRow, error) {
	days := p.Days()
	rows := make([]DailyRow, len(days))
	idx := make(map[string]int, len(days))
	for i, d := range days {
		rows[i].Date = d
		idx[d] = i
	}
	tz := p.Loc.String()
	day := func(col string) string { return `to_char(` + col + ` AT TIME ZONE $4, 'YYYY-MM-DD')` }
	queries := []struct {
		sql  string
		scan func(r *DailyRow, a, b int64)
	}{
		{`SELECT ` + day("created_at") + `, count(*), 0::bigint FROM orders
			WHERE status <> 'draft' AND ` + period("created_at") + ` AND ($3::uuid IS NULL OR city_id = $3) GROUP BY 1`,
			func(r *DailyRow, a, _ int64) { r.OrdersCreated = int(a) }},
		{`SELECT ` + day("completed_at") + `, count(*), COALESCE(sum(fare), 0)::bigint FROM orders
			WHERE status = 'completed' AND ` + period("completed_at") + ` AND ($3::uuid IS NULL OR city_id = $3) GROUP BY 1`,
			func(r *DailyRow, a, b int64) { r.OrdersCompleted, r.GMV = int(a), b }},
		// Charges have negative deltas and refunds positive ones, so the negated
		// sum is commission charged minus commission refunded.
		{`SELECT ` + day("l.created_at") + `, 0::bigint, COALESCE(sum(-l.balance_delta), 0)::bigint
			FROM wallet_ledger l LEFT JOIN orders o ON o.id = l.order_id
			WHERE l.entry_type IN ('commission_charge', 'commission_refund') AND ` + period("l.created_at") + `
			  AND ($3::uuid IS NULL OR o.city_id = $3) GROUP BY 1`,
			func(r *DailyRow, _, b int64) { r.Commission = b }},
		{`SELECT ` + day("t.confirmed_at") + `, count(*), COALESCE(sum(t.verified_amount), 0)::bigint
			FROM top_up_requests t JOIN driver_profiles d ON d.id = t.driver_id
			WHERE t.status IN ('confirmed', 'refunded') AND ` + period("t.confirmed_at") + `
			  AND ($3::uuid IS NULL OR d.city_id = $3) GROUP BY 1`,
			func(r *DailyRow, a, b int64) { r.TopUpsConfirmedCount, r.TopUpsConfirmedAmount = int(a), b }},
	}
	for _, qq := range queries {
		res, err := q.Query(ctx, qq.sql, p.From, p.To, cityID, tz)
		if err != nil {
			return nil, fmt.Errorf("reports: daily series: %w", err)
		}
		for res.Next() {
			var (
				date string
				a, b int64
			)
			if err := res.Scan(&date, &a, &b); err != nil {
				res.Close()
				return nil, fmt.Errorf("reports: scan daily series: %w", err)
			}
			if i, ok := idx[date]; ok {
				qq.scan(&rows[i], a, b)
			}
		}
		res.Close()
		if err := res.Err(); err != nil {
			return nil, fmt.Errorf("reports: daily series: %w", err)
		}
	}
	return rows, nil
}
