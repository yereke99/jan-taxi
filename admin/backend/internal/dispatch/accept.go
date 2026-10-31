package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	phonefmt "github.com/yereke99/jantaxi/admin/backend/internal/platform/phone"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// lockedDriver is a driver profile read FOR SHARE, so moderation cannot change
// its status while the acceptance transaction runs.
type lockedDriver struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Status      string
	IsOnline    bool
	UserBlocked bool
}

func lockDriver(ctx context.Context, tx pgx.Tx, driverID uuid.UUID) (lockedDriver, error) {
	var d lockedDriver
	err := tx.QueryRow(ctx, `
		SELECT d.id, d.user_id, d.status, d.is_online, u.is_blocked
		FROM driver_profiles d JOIN users u ON u.id = d.user_id
		WHERE d.id = $1
		FOR SHARE OF d`, driverID).
		Scan(&d.ID, &d.UserID, &d.Status, &d.IsOnline, &d.UserBlocked)
	if db.IsNoRows(err) {
		return lockedDriver{}, httpx.NotFound()
	}
	if err != nil {
		return lockedDriver{}, fmt.Errorf("dispatch: lock driver: %w", err)
	}
	return d, nil
}

func activeOrderOf(ctx context.Context, q db.Querier, driverID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT id FROM orders
		WHERE driver_id = $1 AND status IN ('accepted', 'driver_arriving', 'in_progress') LIMIT 1`, driverID).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("dispatch: active order: %w", err)
	}
	return id, nil
}

// currentVehicle returns the driver's current vehicle (claimed first), or nil.
func currentVehicle(ctx context.Context, q db.Querier, driverID uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT id FROM vehicles WHERE driver_id = $1 AND status <> 'archived'
		ORDER BY (status = 'claimed') DESC, created_at DESC LIMIT 1`, driverID).Scan(&id)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dispatch: vehicle: %w", err)
	}
	return &id, nil
}

// checkFunds locks the driver's wallet (FOR UPDATE, created when missing) and
// requires available >= commission.
func checkFunds(ctx context.Context, tx pgx.Tx, driverID uuid.UUID, commission int64) error {
	w, err := wallets.Lock(ctx, tx, driverID)
	if err != nil {
		return lifecycle.DBError(err)
	}
	if w.Available() < commission {
		return lifecycle.InsufficientBalance(commission, w.Available(), settings.TopUp(ctx, tx).Min)
	}
	return nil
}

// AcceptResult is the outcome of Accept.
type AcceptResult struct {
	Order lifecycle.Core
	// Replayed is true when the driver already holds this order (a repeated
	// request); nothing changed.
	Replayed bool
	// Charged is the commission taken from the driver's balance (§22.2).
	Charged int64
}

// AcceptOptions tune Accept for the channel the driver used.
type AcceptOptions struct {
	// ViaBot is set when the driver tapped «Тапсырысты алу» in the driver
	// bot (journaled as channel "bot"). The bot marks the tapped message at
	// once; the outbox still edits the driver's offer message (the tap may
	// have come from an /orders list) and sends the trip card with the
	// passenger's contact (retried, deduplicated and fenced against a later
	// cancellation or reassignment), whichever channel won.
	ViaBot bool
}

// Acceptance channels, logged with every attempt.
const (
	ChannelMiniApp = "miniapp"
	ChannelBot     = "bot"
)

// AcceptOrder is the one acceptance operation behind both the driver Mini App
// (POST /driver/orders/{id}/accept) and the driver bot's accept button (POST
// /internal/v1/driver/{tg_id}/orders/{id}/accept): it runs AcceptWith in its
// own transaction, so events and notifications leave only on commit, and
// logs the outcome with the request id. A repeated request of the winner
// (double tap, HTTP retry, redelivered callback) returns Replayed and
// charges nothing; any other driver gets 409 order_already_taken.
func AcceptOrder(ctx context.Context, pool *pgxpool.Pool, orderID, driverID uuid.UUID, opts AcceptOptions) (AcceptResult, error) {
	start := time.Now()
	var res AcceptResult
	err := lifecycle.DBError(db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		res, err = AcceptWith(ctx, tx, orderID, driverID, opts)
		return err
	}))
	channel := ChannelMiniApp
	if opts.ViaBot {
		channel = ChannelBot
	}
	attrs := []any{slog.String("order_id", orderID.String()), slog.String("driver_id", driverID.String()),
		slog.String("channel", channel), slog.Int64("duration_ms", time.Since(start).Milliseconds())}
	log := httpx.Logger(ctx)
	var he *httpx.Error
	switch {
	case err == nil && res.Replayed:
		log.Info("order accept replayed", attrs...)
	case err == nil:
		log.Info("order accepted", append(attrs, slog.Int64("charged", res.Charged))...)
	case errors.As(err, &he) && he.Status < http.StatusInternalServerError:
		log.Info("order accept refused", append(attrs, slog.String("code", he.Code))...)
	default:
		log.Error("order accept failed", append(attrs, slog.Any("err", err))...)
	}
	return res, err
}

// errLocation is 422 location_required for a driver without a fresh location
// fix; details.reason tells location_required (no fix) from location_stale.
func errLocation(reason string, p settings.DispatchSettings) *httpx.Error {
	return httpx.Unprocessable("location_required", "a fresh location is required to take orders").
		WithDetails(map[string]any{"reason": reason, "max_age_s": p.LocationMaxAgeS})
}

// Accept assigns a searching order to the driver, exactly-one-winner
// (ARCHITECTURE §5.1), in the caller's transaction:
//
//  0. the order row is locked FOR UPDATE first (then the driver profile, then
//     the wallet: the lock order of every order transition, so none of them
//     deadlocks with an acceptance), so concurrent acceptances of one order
//     run one after another: whoever comes second sees the result of the
//     first (its own earlier tap is a replay, anyone else is told the order
//     is taken) instead of racing it to the conditional UPDATE;
//  1. the driver is approved, online and not blocked, has no active order
//     and has a fresh location (422 location_required) within the order's
//     effective radius of the pickup, and the driver's vehicle fits the
//     order's class, passengers and cargo (404 otherwise: the order is not in
//     the driver's reach); the city plays no part;
//  2. the wallet row is locked FOR UPDATE and its available balance must
//     cover the commission (402 insufficient_balance; the order stays open
//     for others);
//  3. UPDATE … WHERE status='searching' AND driver_id IS NULL — 0 rows means
//     another driver won (409 order_already_taken);
//  4. the orders_one_active_per_driver index rejects a second active order
//     (409 driver_has_active_order);
//  5. the commission is charged (§22.2: commission_charge, never refunded
//     when the passenger cancels), the winner's offer accepted and the others
//     lost (their bot messages are marked taken), the transition journaled,
//     notifications and events queued.
//
// The Mini App and the driver bot (AcceptOptions.ViaBot) both reach it
// through AcceptOrder.
func Accept(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID) (AcceptResult, error) {
	return AcceptWith(ctx, tx, orderID, driverID, AcceptOptions{})
}

// AcceptWith is Accept with options.
func AcceptWith(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID, opts AcceptOptions) (AcceptResult, error) {
	o, err := lifecycle.LoadCore(ctx, tx, orderID, true)
	if err != nil {
		return AcceptResult{}, err
	}
	drv, err := lockDriver(ctx, tx, driverID)
	if err != nil {
		return AcceptResult{}, err
	}
	if drv.Status != "approved" || drv.UserBlocked {
		return AcceptResult{}, lifecycle.ErrDriverNotApproved()
	}
	// The driver's own passenger orders are invisible to them.
	if o.PassengerID == drv.UserID {
		return AcceptResult{}, httpx.NotFound()
	}
	// A repeated request of the winner answers like the first one did, even
	// when it arrives through the other channel.
	if o.HasDriver(driverID) && lifecycle.OccupiesDriver(o.Status) {
		return AcceptResult{Order: o, Replayed: true}, nil
	}
	if !drv.IsOnline {
		return AcceptResult{}, lifecycle.ErrDriverOffline()
	}
	if o.Status != lifecycle.StatusSearching {
		return AcceptResult{}, notAcceptable(o, driverID)
	}
	if active, err := activeOrderOf(ctx, tx, driverID); err != nil {
		return AcceptResult{}, err
	} else if active != uuid.Nil {
		return AcceptResult{}, lifecycle.ErrDriverHasActiveOrder(active)
	}
	p := settings.Dispatch(ctx, tx)
	rc, err := loadReach(ctx, tx, orderID, driverID, p)
	if err != nil {
		return AcceptResult{}, lifecycle.DBError(err)
	}
	switch reason := rc.problem(); reason {
	case "":
	case ReasonOutOfRadius:
		return AcceptResult{}, httpx.NotFound()
	default:
		return AcceptResult{}, errLocation(reason, p)
	}
	// An order the driver's vehicle cannot take is not offered to them (§21.4).
	if ok, err := fits(ctx, tx, orderID, driverID); err != nil {
		return AcceptResult{}, lifecycle.DBError(err)
	} else if !ok {
		return AcceptResult{}, httpx.NotFound()
	}
	if err := checkFunds(ctx, tx, driverID, o.CommissionAmount); err != nil {
		return AcceptResult{}, err
	}
	vehicleID, err := currentVehicle(ctx, tx, driverID)
	if err != nil {
		return AcceptResult{}, err
	}

	after, err := lifecycle.ScanCore(tx.QueryRow(ctx, `
		UPDATE orders o SET status = 'accepted', driver_id = $2, vehicle_id = $3, accepted_at = now(),
		       version = version + 1
		WHERE o.id = $1 AND o.status = 'searching' AND o.driver_id IS NULL
		RETURNING `+lifecycle.CoreColumns, orderID, driverID, vehicleID))
	switch {
	case db.IsNoRows(err):
		cur, lerr := lifecycle.LoadCore(ctx, tx, orderID, false)
		if lerr != nil {
			return AcceptResult{}, lerr
		}
		return AcceptResult{}, notAcceptable(cur, driverID)
	case db.IsUniqueViolation(err, lifecycle.IdxOneActivePerDriver):
		return AcceptResult{}, lifecycle.ErrDriverHasActiveOrder(uuid.Nil)
	case err != nil:
		return AcceptResult{}, lifecycle.DBError(fmt.Errorf("dispatch: accept: %w", err))
	}

	charged, err := assigned(ctx, tx, o, after, lifecycle.Driver(driverID), nil, opts)
	if err != nil {
		return AcceptResult{}, err
	}
	return AcceptResult{Order: after, Charged: charged}, nil
}

// notAcceptable explains why an order that is no longer searching cannot be
// accepted by driverID: taken by another driver, or closed — without a
// driver, or as driverID's own finished trip (a late tap on an old offer
// must not read "another driver took it").
func notAcceptable(o lifecycle.Core, driverID uuid.UUID) error {
	if o.DriverID != nil && !o.HasDriver(driverID) {
		return lifecycle.ErrOrderTaken()
	}
	return lifecycle.InvalidTransition(o.Status, "accept")
}

// lowBalanceZone is the business day used to send one low-balance message
// per driver and day (Kazakhstan, UTC+5).
var lowBalanceZone = time.FixedZone("Asia/Aqtobe", 5*60*60)

// lowBalanceThreshold is the balance a driver needs for the next offer: the
// fixed commission, or with percentage commissions the commission just paid.
func lowBalanceThreshold(ctx context.Context, q db.Querier, orderCommission int64) int64 {
	if p := settings.Commission(ctx, q); p.Mode == settings.CommissionFixed {
		return p.FixedAmount
	}
	return orderCommission
}

// warnLowBalance sends wallet_low_balance (once per driver and day) when the
// driver's available balance no longer covers the next commission, so they
// stop receiving offers (§22.2).
func warnLowBalance(ctx context.Context, tx pgx.Tx, driverID, userID uuid.UUID, orderCommission int64) error {
	need := lowBalanceThreshold(ctx, tx, orderCommission)
	if need <= 0 {
		return nil
	}
	w, err := wallets.Get(ctx, tx, driverID)
	if err != nil {
		return lifecycle.DBError(err)
	}
	if w.Available() >= need {
		return nil
	}
	return lifecycle.Notify(ctx, tx, notifications.DriverBot, userID, notifications.TplWalletLowBalance,
		map[string]any{"available": w.Available(), "required": need}, lowBalanceKey(driverID))
}

// lowBalanceKey is the dedupe key of driverID's low-balance message today:
// at most one per driver and business day, whichever path sends it.
func lowBalanceKey(driverID uuid.UUID) string {
	return "wallet_low_balance:" + driverID.String() + ":" + time.Now().In(lowBalanceZone).Format(time.DateOnly)
}

// assigned does everything after the order row points at its new driver:
// charge the commission, settle offers, journal, notify and publish. prev is
// the driver who held the order before a re-assignment (nil otherwise). It
// returns the commission charged.
func assigned(ctx context.Context, tx pgx.Tx, before, after lifecycle.Core, actor lifecycle.Actor, prev *uuid.UUID, opts AcceptOptions) (int64, error) {
	driverID := *after.DriverID
	if err := notifications.CancelOrderContacts(ctx, tx, after.ID); err != nil {
		return 0, err
	}
	charged, err := wallets.ChargeOnAccept(ctx, tx, driverID, after.ID, after.CommissionAmount, after.Version)
	if err != nil {
		if errors.Is(err, wallets.ErrInsufficientBalance) {
			w, _ := wallets.Get(ctx, tx, driverID)
			return 0, lifecycle.InsufficientBalance(after.CommissionAmount, w.Available(), settings.TopUp(ctx, tx).Min)
		}
		return 0, lifecycle.DBError(err)
	}
	lost, err := closeOffersOnAccept(ctx, tx, after.ID, driverID)
	if err != nil {
		return 0, err
	}
	meta := map[string]any{"driver_id": driverID.String(), "commission_charged": charged}
	if prev != nil {
		meta["previous_driver_id"] = prev.String()
	}
	if opts.ViaBot {
		meta["channel"] = "bot"
	}
	if err := lifecycle.InsertEvent(ctx, tx, lifecycle.Event{OrderID: after.ID, From: &before.Status,
		To: lifecycle.StatusAccepted, Actor: actor, Meta: meta}); err != nil {
		return 0, err
	}

	info, err := lifecycle.LoadDriverInfo(ctx, tx, driverID)
	if err != nil {
		return 0, err
	}
	// The straight-line arrival estimate now; the road estimate follows right
	// after the commit (ArrivalETA.Refine), which also releases the
	// passenger's message held for it (§22.6).
	eta, err := fallbackETA(ctx, tx, after.ID, driverID)
	if err != nil {
		return 0, err
	}
	if err := storeETA(ctx, tx, after.ID, eta); err != nil {
		return 0, err
	}
	suffix := after.ID.String() + ":" + driverID.String() + ":" + strconv.Itoa(after.Version)
	pushParams := map[string]any{
		"order_id": after.ID.String(), "order_number": after.Number,
		"driver_name": info.Name(), "vehicle": info.Vehicle(), "plate": info.PlateLabel(),
	}
	if r := info.Rating(); r != "" {
		pushParams["rating"] = r
	}
	if info.Phone != "" {
		pushParams["driver_phone"] = phonefmt.Format(info.Phone)
	}
	if eta != nil {
		pushParams["eta_min"] = ETAMinutes(*eta)
	}
	if info.AvatarFileID != nil {
		pushParams["photo"] = info.AvatarFileID.String()
	}
	if err := notifications.Enqueue(ctx, tx, notifications.Notification{
		Bot: notifications.PassengerBot, UserID: after.PassengerID, Template: notifications.TplOrderAcceptedPassenger,
		Params: pushParams, DedupeKey: "order_accepted_passenger:" + suffix, Delay: ETAHold,
	}); err != nil {
		return 0, fmt.Errorf("dispatch: notify passenger: %w", err)
	}
	params, err := TripCardParams(ctx, tx, after.ID)
	if err != nil {
		return 0, err
	}
	if err := lifecycle.Notify(ctx, tx, notifications.DriverBot, info.UserID, notifications.TplOrderAcceptedDriver,
		params, "order_accepted_driver:"+suffix); err != nil {
		return 0, err
	}
	if err := lifecycle.CloseOfferMessage(ctx, tx, after.ID, driverID, lifecycle.OfferMessageWon); err != nil {
		return 0, err
	}

	if err := lifecycle.Publish(ctx, tx, realtime.OrderAccepted, lifecycle.EventData(after, map[string]any{"driver_eta_s": eta}),
		lifecycle.PartyScopes(after)...); err != nil {
		return 0, err
	}
	if before.Status == lifecycle.StatusSearching {
		if err := lifecycle.PublishUnavailable(ctx, tx, after); err != nil {
			return 0, err
		}
	}
	if charged > 0 {
		if err := lifecycle.PublishWallet(ctx, tx, driverID, after.ID, wallets.EntryCommissionCharge); err != nil {
			return 0, err
		}
		if err := warnLowBalance(ctx, tx, driverID, info.UserID, after.CommissionAmount); err != nil {
			return 0, err
		}
	}
	if len(lost) > 0 {
		ids := make([]uuid.UUID, len(lost))
		for i, l := range lost {
			ids[i] = l.DriverID
		}
		if err := lifecycle.CloseOfferMessages(ctx, tx, after.ID, ids, lifecycle.OfferMessageTaken); err != nil {
			return 0, err
		}
		data := map[string]any{"order_id": after.ID.String(), "order_number": after.Number, "status": after.Status}
		if err := lifecycle.Publish(ctx, tx, realtime.OrderTaken, data, lifecycle.DriverScopes(ids)...); err != nil {
			return 0, err
		}
	}
	return charged, nil
}

// TripCardParams are the order_accepted_driver params of orderID: the trip
// (route in order, class, load, comment) and how to reach the passenger
// (§22.4). The commission is not part of it: drivers see the fare only.
func TripCardParams(ctx context.Context, q db.Querier, orderID uuid.UUID) (map[string]any, error) {
	var (
		number                    int64
		fare                      int64
		category, payment         string
		firstName                 string
		username, phone           *string
		tgID                      int64
		verified                  bool
		locality                  *string
		passengers                *int
		cargoSize, cargo, comment *string
		recipient                 *string
		classKK, classRU, classEN string
	)
	err := q.QueryRow(ctx, `
		SELECT o.number, o.fare, o.category, o.payment_method, u.first_name, u.username, u.telegram_user_id,
		       u.phone_e164, u.phone_verified_at IS NOT NULL, o.locality, o.passenger_count, o.cargo_size,
		       o.cargo_description, o.comment, o.recipient_phone, vc.name_kk, vc.name_ru, vc.name_en
		FROM orders o JOIN users u ON u.id = o.passenger_id JOIN vehicle_classes vc ON vc.code = o.vehicle_class
		WHERE o.id = $1`, orderID).Scan(&number, &fare, &category, &payment, &firstName, &username, &tgID, &phone,
		&verified, &locality, &passengers, &cargoSize, &cargo, &comment, &recipient, &classKK, &classRU, &classEN)
	if err != nil {
		return nil, fmt.Errorf("dispatch: trip card: %w", err)
	}
	route, err := lifecycle.LoadRoute(ctx, q, orderID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(firstName)
	if name == "" {
		name = "Telegram"
	}
	params := map[string]any{
		"order_id": orderID.String(), "order_number": number, "category": category,
		"vehicle_class": map[string]string{"kk": classKK, "ru": classRU, "en": classEN},
		"pickup":        route.Pickup, "destination": route.Destination, "fare": fare, "payment_method": payment,
		"passenger_name": name, "passenger_link": lifecycle.TelegramLink(username, tgID),
	}
	if via := route.ViaText("…"); via != "" {
		params["via"] = via
	}
	if route.PickupDetails != "" {
		params["pickup_details"] = route.PickupDetails
	}
	if locality != nil && *locality != "" {
		params["locality"] = *locality
	}
	if passengers != nil {
		params["passengers"] = *passengers
	}
	if cargoSize != nil {
		params["cargo_size"] = *cargoSize
	}
	if cargo != nil && *cargo != "" {
		params["cargo"] = *cargo
	}
	if comment != nil && *comment != "" {
		params["comment"] = *comment
	}
	if verified && phone != nil && *phone != "" {
		params["passenger_phone"] = phonefmt.Format(*phone)
	}
	if recipient != nil && *recipient != "" {
		params["recipient_phone"] = phonefmt.Format(*recipient)
	}
	return params, nil
}
