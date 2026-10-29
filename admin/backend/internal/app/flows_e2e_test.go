package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/app"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/verification"
)

// TestBusinessFlowsNotifyAndPublish drives every money and state transition
// that sends a Telegram message through the real application wiring (HTTP
// handlers, internal bot API and the search-expiry job function) and checks
// that:
//
//   - every registered notification template is enqueued at least once, with
//     params Enqueue accepted, to the right recipient and bot, and renders in
//     kk, ru and en;
//   - every §9 realtime event type is published (pg_notify after commit) with
//     the expected scopes;
//   - wallets move exactly as §6 says and reconcile at the end.
func TestBusinessFlowsNotifyAndPublish(t *testing.T) {
	e := newFlowEnv(t)
	ctx := context.Background()

	// Driver A registers through the Mini App, is sent back for corrections,
	// resubmits and is approved.
	a := e.registerDriver("Ерлан", "+77471234501", "123 abc 02")
	e.admin(http.MethodPost, "/api/v1/admin/drivers/"+a.driverID+"/request-corrections", map[string]any{
		"items":   []map[string]any{{"field": "vehicle_color", "reason": "missing_info", "comment": "Түсін нақтылаңыз"}},
		"comment": "Көлік түсін тексеріңіз",
	}, http.StatusOK)
	e.must(http.StatusOK, e.call(e.h, http.MethodPut, "/api/v1/driver/registration", map[string]any{
		"first_name": "Ерлан", "last_name": "Бекенов",
		"vehicle": map[string]any{"make": "Toyota", "model": "Camry", "color": "Күміс", "plate": "123 abc 02"},
	}, a.auth), "fix flagged field")
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/register", map[string]any{}, a.auth), "resubmit")
	e.admin(http.MethodPost, "/api/v1/admin/drivers/"+a.driverID+"/approve", map[string]any{"comment": "Құжаттар дұрыс"}, http.StatusOK)

	// Driver C registers and is rejected.
	c := e.registerDriver("Нұрлан", "+77471234502", "456 def 02")
	// An uncertain OCR result leaves the application pending for the existing
	// moderator, and queues a localized additional-review acknowledgment.
	identity, err := verification.NewService(e.pool, verification.Options{
		Enabled: true, Key: base64.StdEncoding.EncodeToString(make([]byte, 32)), MinConfidence: 98,
	})
	if err != nil {
		t.Fatal(err)
	}
	var identityApp uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM driver_applications WHERE driver_id=$1 ORDER BY round DESC LIMIT 1`, c.driverID).Scan(&identityApp); err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(ctx, e.pool, func(tx pgx.Tx) error {
		if err := identity.SaveInput(ctx, tx, uuid.MustParse(c.driverID), verification.Input{
			DocumentType: "id_card", IIN: strings.Repeat("1", 11) + "0", DocumentNumber: "123456789",
		}); err != nil {
			return err
		}
		return identity.Enqueue(ctx, tx, uuid.MustParse(c.driverID), identityApp, "Нұрлан", "Бекенов", []verification.Document{})
	}); err != nil {
		t.Fatal(err)
	}
	if did, err := identity.RunOnce(ctx, uncertainIdentityEngine{}, nil, verification.WorkerOptions{}); err != nil || !did {
		t.Fatalf("uncertain identity review: did=%v err=%v", did, err)
	}
	e.admin(http.MethodPost, "/api/v1/admin/drivers/"+c.driverID+"/reject", map[string]any{"reason": "Құжаттар анық емес"}, http.StatusOK)

	// A's approval credited the welcome bonus (§22.1); C was rejected.
	e.wallet(a, 500, 0)

	// Top-ups: "I paid" never credits; the finance admin credits the verified
	// amount once; a second top-up is rejected.
	t1 := e.must(http.StatusCreated, e.call(e.h, http.MethodPost, "/api/v1/driver/wallet/topups",
		map[string]any{"amount": 500, "idempotency_key": uuid.NewString()}, a.auth), "create top-up").str("id")
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/wallet/topups/"+t1+"/submit",
		map[string]any{"payment_reference": "KASPI 0001"}, a.auth), "submit top-up")
	e.wallet(a, 500, 0)
	e.admin(http.MethodPost, "/api/v1/admin/topups/"+t1+"/confirm", map[string]any{
		"verified_amount": 120, "provider_transaction_id": "KSP-0001", "paid_at": time.Now().Add(-time.Minute).UTC(),
	}, http.StatusOK)
	e.wallet(a, 620, 0)
	t2 := e.must(http.StatusCreated, e.call(e.h, http.MethodPost, "/api/v1/driver/wallet/topups",
		map[string]any{"amount": 1000, "idempotency_key": uuid.NewString()}, a.auth), "second top-up").str("id")
	e.admin(http.MethodPost, "/api/v1/admin/topups/"+t2+"/reject", map[string]any{"reason": "Төлем табылмады"}, http.StatusOK)
	e.wallet(a, 620, 0)
	// A finance correction leaves A just enough for one commission.
	e.admin(http.MethodPost, "/api/v1/admin/drivers/"+a.driverID+"/wallet/adjustments", map[string]any{
		"amount": -530, "reason": "Түзету", "idempotency_key": uuid.NewString(),
	}, http.StatusCreated)
	e.wallet(a, 90, 0)

	// A reports a location and goes online (dispatch is by distance to the
	// pickup, so going online without a fresh fix is refused); B is an
	// approved online driver with money; both are offered the next order.
	e.must(http.StatusUnprocessableEntity, e.call(e.h, http.MethodPatch, "/api/v1/driver/availability", map[string]any{"online": true}, a.auth), "A online without location")
	e.must(http.StatusNoContent, e.call(e.h, http.MethodPost, "/api/v1/driver/location", map[string]any{"lat": 48.8270, "lon": 58.1450}, a.auth), "A location")
	e.must(http.StatusOK, e.call(e.h, http.MethodPatch, "/api/v1/driver/availability", map[string]any{"online": true}, a.auth), "A online")
	e.must(http.StatusNoContent, e.call(e.h, http.MethodPost, "/api/v1/driver/location", map[string]any{"lat": 48.8268, "lon": 58.1448}, a.auth), "A location online")
	bFix := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Online: true, Balance: 1000})
	b := flowDriver{userID: bFix.UserID, tgID: bFix.TelegramID, driverID: bFix.DriverID.String(),
		auth: e.signIn("driver", config.TestDriverBotToken, bFix.TelegramID)}

	// Passenger 1: a three-stop ride with driver A. Both offers reached the
	// driver bot; the commission is charged when A takes the order, which
	// leaves A below the next commission (one low-balance message), and B's
	// offer is marked taken.
	p1 := e.passenger()
	o1 := e.createOrder(p1, 3, 800)
	e.offersSent(o1)
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o1+"/accept", nil, a.auth), "A accepts")
	e.wallet(a, 40, 0)
	e.must(http.StatusConflict, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o1+"/accept", nil, b.auth), "B loses")
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o1+"/arriving", nil, a.auth), "arriving")
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o1+"/start", nil, a.auth), "start")
	detail := e.must(http.StatusOK, e.call(e.h, http.MethodGet, "/api/v1/driver/orders/"+o1, nil, a.auth), "driver detail")
	var stopID string
	for _, s := range detail.body["stops"].([]any) {
		if st := s.(map[string]any); st["kind"] == "intermediate" {
			stopID = st["id"].(string)
		}
	}
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o1+"/stops/"+stopID+"/complete", nil, a.auth), "stop")
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o1+"/complete", nil, a.auth), "complete")
	e.wallet(a, 40, 0) // nothing more on completion

	// The passenger disputes; the operator cancels the ride and refunds the
	// commission.
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/orders/"+o1+"/dispute",
		map[string]any{"reason": "Жүргізуші басқа жолмен жүрді"}, p1.auth), "dispute")
	disputes := e.admin(http.MethodGet, "/api/v1/admin/disputes?status=open", nil, http.StatusOK)
	disputeID := disputes.body["items"].([]any)[0].(map[string]any)["id"].(string)
	e.admin(http.MethodPost, "/api/v1/admin/disputes/"+disputeID+"/resolve", map[string]any{
		"outcome": "cancelled", "resolution": "Комиссия қайтарылды", "refund_commission": true,
	}, http.StatusOK)
	e.wallet(a, 90, 0)
	e.ledgerTypes(a, o1, "commission_charge", "commission_refund")

	// Suspension and restoration (A has no active order).
	e.admin(http.MethodPost, "/api/v1/admin/drivers/"+a.driverID+"/suspend", map[string]any{"reason": "Шағымды тексеру"}, http.StatusOK)
	e.admin(http.MethodPost, "/api/v1/admin/drivers/"+a.driverID+"/restore", nil, http.StatusOK)

	// Passenger 2 with driver B: a passenger cancellation keeps the
	// commission (§22.2), an operator cancellation refunds it, and a search
	// that finds nobody closes the offers.
	p2 := e.passenger()
	o2 := e.createOrder(p2, 2, 500)
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o2+"/accept", nil, b.auth), "B accepts o2")
	e.wallet(b, 950, 0)
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/orders/"+o2+"/cancel",
		map[string]any{"reason_code": "changed_plans"}, p2.auth), "passenger cancel")
	e.wallet(b, 950, 0)
	o3 := e.createOrder(p2, 2, 500)
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o3+"/accept", nil, b.auth), "B accepts o3")
	e.must(http.StatusNotFound, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o3+"/cancel",
		map[string]any{"reason": "Көлік бұзылды"}, b.auth), "driver cancel")
	e.admin(http.MethodPost, "/api/v1/admin/orders/"+o3+"/cancel",
		map[string]any{"reason": "Жүргізуші көлігі бұзылды", "refund_commission": true}, http.StatusOK)
	e.wallet(b, 950, 0)
	o4 := e.createOrder(p2, 2, 500)
	e.offersSent(o4)
	if _, err := e.pool.Exec(ctx, `UPDATE orders SET search_expires_at = now() - interval '1 second' WHERE id = $1`, o4); err != nil {
		t.Fatal(err)
	}
	if n, err := dispatch.ExpireSearches(ctx, e.pool); err != nil || n != 1 {
		t.Fatalf("search expiry: %d %v", n, err)
	}

	// Operator paths: re-assign an accepted order from B to A with a refund
	// for B (A is offline, which an operator may override, and pays the
	// commission), cancel it, then dispute B's completed ride and resolve it
	// as completed.
	o5 := e.createOrder(p2, 2, 500)
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o5+"/accept", nil, b.auth), "B accepts o5")
	e.wallet(b, 900, 0)
	e.admin(http.MethodPost, "/api/v1/admin/orders/"+o5+"/assign", map[string]any{"driver_id": a.driverID, "refund_commission": true}, http.StatusOK)
	e.wallet(b, 950, 0)
	e.wallet(a, 40, 0)
	e.admin(http.MethodPost, "/api/v1/admin/orders/"+o5+"/cancel", map[string]any{"reason": "Жолаушы бас тартты"}, http.StatusOK)
	e.wallet(a, 40, 0)
	p3 := e.passenger()
	o6 := e.createOrder(p3, 2, 500)
	for _, step := range []string{"accept", "arriving", "start", "complete"} {
		e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+o6+"/"+step, nil, b.auth), "o6 "+step)
	}
	e.wallet(b, 900, 0)
	e.admin(http.MethodPost, "/api/v1/admin/orders/"+o6+"/dispute", map[string]any{"reason": "Бағасы туралы шағым"}, http.StatusOK)
	disputes = e.admin(http.MethodGet, "/api/v1/admin/disputes?status=open", nil, http.StatusOK)
	disputeID = disputes.body["items"].([]any)[0].(map[string]any)["id"].(string)
	e.admin(http.MethodPost, "/api/v1/admin/disputes/"+disputeID+"/resolve", map[string]any{
		"outcome": "completed", "resolution": "Сапар дұрыс орындалған", "refund_commission": false,
	}, http.StatusOK)
	e.wallet(b, 900, 0) // charged once, not again on resolution
	e.ledgerTypes(b, o6, "commission_charge")

	// The company credits B a bonus (§22.7); a repeated click changes nothing.
	bonusKey := uuid.NewString()
	for _, status := range []int{http.StatusCreated, http.StatusOK} {
		e.admin(http.MethodPost, "/api/v1/admin/drivers/"+b.driverID+"/bonus", map[string]any{
			"amount": 1000, "comment": "Үздік жүргізуші", "idempotency_key": bonusKey,
		}, status)
	}
	e.wallet(b, 1900, 0)

	// Ledger and wallets agree.
	rec := e.admin(http.MethodGet, "/api/v1/admin/wallets/reconciliation", nil, http.StatusOK)
	if rec.body["ok"] != true {
		t.Fatalf("reconciliation: %s", rec.raw)
	}

	// --- Notifications -----------------------------------------------------
	type expect struct {
		template string
		user     uuid.UUID
		bot      notifications.Bot
	}
	want := []expect{
		{notifications.TplPhoneVerified, a.userID, notifications.DriverBot},
		{notifications.TplPhoneVerified, c.userID, notifications.DriverBot},
		{notifications.TplModerationSubmitted, a.userID, notifications.DriverBot},
		{notifications.TplModerationChangesRequested, a.userID, notifications.DriverBot},
		{notifications.TplModerationApproved, a.userID, notifications.DriverBot},
		{notifications.TplDriverWelcomeBonus, a.userID, notifications.DriverBot},
		{notifications.TplDriverBonusGranted, b.userID, notifications.DriverBot},
		{notifications.TplModerationSubmitted, c.userID, notifications.DriverBot},
		{notifications.TplModerationRejected, c.userID, notifications.DriverBot},
		{notifications.TplTopUpCreated, a.userID, notifications.DriverBot},
		{notifications.TplTopUpConfirmed, a.userID, notifications.DriverBot},
		{notifications.TplTopUpRejected, a.userID, notifications.DriverBot},
		{notifications.TplOrderCreatedPassenger, p1.userID, notifications.PassengerBot},
		{notifications.TplOrderAvailableDriver, a.userID, notifications.DriverBot},
		{notifications.TplOrderAvailableDriver, b.userID, notifications.DriverBot},
		{notifications.TplOrderAcceptedPassenger, p1.userID, notifications.PassengerBot},
		{notifications.TplOrderAcceptedDriver, a.userID, notifications.DriverBot},
		{notifications.TplOrderOfferWon, a.userID, notifications.DriverBot},
		{notifications.TplOrderOfferTaken, b.userID, notifications.DriverBot},
		{notifications.TplOrderOfferCancelled, b.userID, notifications.DriverBot},
		{notifications.TplOrderDriverArriving, p1.userID, notifications.PassengerBot},
		{notifications.TplOrderStarted, p1.userID, notifications.PassengerBot},
		{notifications.TplOrderStopCompleted, p1.userID, notifications.PassengerBot},
		{notifications.TplOrderCompletedPassenger, p1.userID, notifications.PassengerBot},
		{notifications.TplOrderCompletedDriver, a.userID, notifications.DriverBot},
		{notifications.TplWalletLowBalance, a.userID, notifications.DriverBot},
		{notifications.TplDisputeOpened, a.userID, notifications.DriverBot},
		{notifications.TplDisputeResolved, p1.userID, notifications.PassengerBot},
		{notifications.TplDisputeResolved, a.userID, notifications.DriverBot},
		{notifications.TplDriverSuspended, a.userID, notifications.DriverBot},
		{notifications.TplDriverRestored, a.userID, notifications.DriverBot},
		{notifications.TplOrderCancelledDriver, b.userID, notifications.DriverBot},
		{notifications.TplOrderCancelledPassenger, p2.userID, notifications.PassengerBot},
		{notifications.TplOrderNoDriver, p2.userID, notifications.PassengerBot},
		// Operator paths.
		{notifications.TplOrderCancelledDriver, a.userID, notifications.DriverBot},
		{notifications.TplDisputeOpened, p3.userID, notifications.PassengerBot},
		{notifications.TplDisputeOpened, b.userID, notifications.DriverBot},
		{notifications.TplDisputeResolved, p3.userID, notifications.PassengerBot},
		{notifications.TplDisputeResolved, b.userID, notifications.DriverBot},
	}
	rows := e.outbox()
	for _, w := range want {
		if !slices.ContainsFunc(rows, func(r outboxRow) bool {
			return r.template == w.template && r.userID == w.user && r.bot == string(w.bot)
		}) {
			t.Errorf("no %s message to %s via the %s bot", w.template, w.user, w.bot)
		}
	}
	// The passenger who opened the dispute is not told about it; B hears about
	// the passenger's and the operator's cancellation and the re-assignment;
	// P2 about the two operator cancellations.
	count := func(template string, user uuid.UUID) int {
		n := 0
		for _, r := range rows {
			if r.template == template && r.userID == user {
				n++
			}
		}
		return n
	}
	if n := count(notifications.TplDisputeOpened, p1.userID); n != 0 {
		t.Error("dispute_opened sent to the passenger who opened it")
	}
	if n := count(notifications.TplOrderCancelledDriver, b.userID); n != 3 {
		t.Errorf("order_cancelled_driver to B: %d, want 3", n)
	}
	if n := count(notifications.TplWalletLowBalance, a.userID); n != 1 {
		t.Errorf("wallet_low_balance to A: %d, want 1 (once a day)", n)
	}
	if n := count(notifications.TplDriverWelcomeBonus, c.userID); n != 0 {
		t.Error("welcome bonus for a rejected driver")
	}
	if n := count(notifications.TplOrderCancelledPassenger, p2.userID); n != 2 {
		t.Errorf("order_cancelled_passenger to P2: %d, want 2", n)
	}
	var covered []string
	byTemplate := map[string]bool{}
	for _, r := range rows {
		byTemplate[r.template] = true
	}
	for k := range byTemplate {
		covered = append(covered, k)
	}
	sort.Strings(covered)
	if !slices.Equal(covered, notifications.Keys()) {
		t.Errorf("templates enqueued:\n  %v\nregistered:\n  %v", covered, notifications.Keys())
	}
	// Every stored message renders in every language with its button.
	for _, r := range rows {
		tpl, _ := notifications.Lookup(r.template)
		url := "https://app.jantaxi.test/passenger/"
		if r.bot == string(notifications.DriverBot) {
			url = "https://app.jantaxi.test/driver/"
		}
		for _, lang := range notifications.Languages {
			msg, err := notifications.RenderFor(ctx, e.pool, r.template, lang, r.params, url)
			if err != nil {
				t.Errorf("render %s/%s: %v", r.template, lang, err)
				continue
			}
			if strings.TrimSpace(msg.Text) == "" || strings.Contains(msg.Text, "{{") {
				t.Errorf("render %s/%s: %q", r.template, lang, msg.Text)
			}
			if tpl.Button != nil && (msg.Markup == nil || len(msg.Markup.InlineKeyboard) == 0) {
				t.Errorf("render %s/%s: no web_app button", r.template, lang)
			}
		}
	}

	// --- Realtime -------------------------------------------------------------
	wantEvents := map[string][]string{
		realtime.OrderCreated:              {realtime.UserScope(p1.userID), realtime.AdminScope},
		realtime.OrderAvailable:            {realtime.DriverScope(uuid.MustParse(a.driverID)), realtime.DriverScope(bFix.DriverID)},
		realtime.OrderTaken:                {realtime.DriverScope(bFix.DriverID)},
		realtime.OrderAccepted:             {realtime.UserScope(p1.userID), realtime.DriverScope(uuid.MustParse(a.driverID)), realtime.AdminScope},
		realtime.OrderDriverArriving:       {realtime.UserScope(p1.userID), realtime.DriverScope(uuid.MustParse(a.driverID))},
		realtime.OrderStarted:              {realtime.UserScope(p1.userID), realtime.DriverScope(uuid.MustParse(a.driverID))},
		realtime.OrderStopCompleted:        {realtime.UserScope(p1.userID), realtime.DriverScope(uuid.MustParse(a.driverID))},
		realtime.OrderCompleted:            {realtime.UserScope(p1.userID), realtime.DriverScope(uuid.MustParse(a.driverID))},
		realtime.OrderDisputed:             {realtime.UserScope(p1.userID), realtime.DriverScope(uuid.MustParse(a.driverID))},
		realtime.OrderCancelled:            {realtime.UserScope(p2.userID), realtime.DriverScope(bFix.DriverID)},
		realtime.DriverLocationUpdated:     {realtime.AdminScope},
		realtime.DriverStatusChanged:       {realtime.DriverScope(uuid.MustParse(a.driverID)), realtime.UserScope(a.userID), realtime.AdminScope},
		realtime.WalletUpdated:             {realtime.DriverScope(uuid.MustParse(a.driverID))},
		realtime.TopUpUpdated:              {realtime.DriverScope(uuid.MustParse(a.driverID)), realtime.AdminScope},
		realtime.TopUpConfirmed:            {realtime.DriverScope(uuid.MustParse(a.driverID)), realtime.UserScope(a.userID)},
		realtime.ModerationSubmitted:       {realtime.DriverScope(uuid.MustParse(a.driverID)), realtime.UserScope(a.userID), realtime.AdminScope},
		realtime.ModerationChangesRequired: {realtime.DriverScope(uuid.MustParse(a.driverID)), realtime.UserScope(a.userID)},
		realtime.ModerationApproved:        {realtime.DriverScope(uuid.MustParse(a.driverID)), realtime.UserScope(a.userID)},
		realtime.ModerationRejected:        {realtime.DriverScope(uuid.MustParse(c.driverID)), realtime.UserScope(c.userID)},
		realtime.DriverPhoneVerified:       {realtime.UserScope(a.userID)},
	}
	e.events.waitFor(t, wantEvents)
}

// --- environment ---------------------------------------------------------------

type flowEnv struct {
	t      *testing.T
	pool   *pgxpool.Pool
	app    *app.App
	h, ih  http.Handler
	cookie string
	csrf   string
	events *eventLog
}

type flowResponse struct {
	code int
	body map[string]any
	raw  []byte
}

func (r flowResponse) str(key string) string {
	s, _ := r.body[key].(string)
	return s
}

type flowUser struct {
	userID uuid.UUID
	tgID   int64
	auth   map[string]string
}

type flowDriver struct {
	userID   uuid.UUID
	tgID     int64
	driverID string
	auth     map[string]string
}

func newFlowEnv(t *testing.T) *flowEnv {
	t.Helper()
	pool := testutil.NewDB(t)
	ctx := context.Background()
	osrm := httptest.NewServer(http.HandlerFunc(fakeOSRMRoute))
	t.Cleanup(osrm.Close)

	cfg := config.ForTesting("")
	cfg.FileStorageDir = t.TempDir()
	cfg.OSRMBaseURL = osrm.URL
	a, err := app.NewWithPool(ctx, cfg, slog.New(slog.DiscardHandler), pool)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	t.Cleanup(a.Close)
	t.Cleanup(func() { settings.Configure(settings.Env{}) })

	e := &flowEnv{t: t, pool: pool, app: a, h: a.Handler(), ih: a.InternalHandler()}
	e.events = listenEvents(t, pool)

	const pw = "correct horse battery staple"
	if _, err := a.Auth.CreateAdmin(ctx, "root@jantaxi.test", "Root", auth.RoleSuperAdmin, pw); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/login",
		strings.NewReader(`{"email":"root@jantaxi.test","password":"`+pw+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login: %d %s", rec.Code, rec.Body)
	}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == auth.AdminCookieName {
			e.cookie = ck.Value
		}
	}
	var login map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &login)
	e.csrf, _ = login["csrf_token"].(string)
	return e
}

func (e *flowEnv) call(h http.Handler, method, path string, body any, hdr map[string]string) flowResponse {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return e.serve(h, req)
}

func (e *flowEnv) serve(h http.Handler, req *http.Request) flowResponse {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := flowResponse{code: rec.Code, raw: rec.Body.Bytes()}
	_ = json.Unmarshal(res.raw, &res.body)
	return res
}

func (e *flowEnv) must(code int, r flowResponse, what string) flowResponse {
	e.t.Helper()
	if r.code != code {
		e.t.Fatalf("%s: %d (want %d) %s", what, r.code, code, r.raw)
	}
	return r
}

// admin calls an admin route with the session cookie and CSRF token.
func (e *flowEnv) admin(method, path string, body any, code int) flowResponse {
	e.t.Helper()
	return e.must(code, e.call(e.h, method, path, body, map[string]string{
		"Cookie": auth.AdminCookieName + "=" + e.cookie, auth.CSRFHeader: e.csrf,
	}), method+" "+path)
}

func (e *flowEnv) signIn(app, token string, tgID int64) map[string]string {
	e.t.Helper()
	r := e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/auth/telegram/"+app,
		map[string]any{"init_data": testutil.SignInitData(token, tgID, "Айгүл", time.Now(), "home")}, nil), app+" sign-in")
	return map[string]string{"Authorization": "Bearer " + r.str("token")}
}

func (e *flowEnv) userID(tgID int64) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE telegram_user_id = $1`, tgID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *flowEnv) passenger() flowUser {
	e.t.Helper()
	tg := testutil.NextTelegramID()
	h := e.signIn("passenger", config.TestPassengerBotToken, tg)
	return flowUser{userID: e.userID(tg), tgID: tg, auth: h}
}

// registerDriver signs a new driver in, verifies the phone through the driver
// bot's internal endpoint, fills in the registration and submits it.
func (e *flowEnv) registerDriver(name, phone, plate string) flowDriver {
	e.t.Helper()
	tg := testutil.NextTelegramID()
	h := e.signIn("driver", config.TestDriverBotToken, tg)
	e.must(http.StatusOK, e.call(e.ih, http.MethodPost, fmt.Sprintf("/internal/v1/driver/%d/contact", tg),
		map[string]any{"phone": phone, "contact_user_id": tg},
		map[string]string{"Authorization": "Bearer " + config.TestServiceSecret, auth.BotHeader: "driver"}), "verify phone")
	e.must(http.StatusOK, e.call(e.h, http.MethodPut, "/api/v1/driver/registration", map[string]any{
		"first_name": name, "last_name": "Бекенов",
		"vehicle": map[string]any{"make": "Toyota", "model": "Camry", "color": "Ақ", "plate": plate, "vehicle_class": "sedan"},
	}, h), "save registration")
	for i, kind := range []string{"avatar", "id_front", "id_back", "vehicle_main"} {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("kind", kind)
		fw, _ := mw.CreateFormFile("file", "photo.jpg")
		_, _ = fw.Write(driverstest.JPEG(e.t, 40, 30, uint8(i+1)))
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/driver/registration/files", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", h["Authorization"])
		e.must(http.StatusCreated, e.serve(e.h, req), "upload "+kind)
	}
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/register",
		map[string]any{"pd_consent": true, "pd_consent_version": "2026-10"}, h), "submit registration")
	uid := e.userID(tg)
	var driverID uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM driver_profiles WHERE user_id = $1`, uid).Scan(&driverID); err != nil {
		e.t.Fatal(err)
	}
	return flowDriver{userID: uid, tgID: tg, driverID: driverID.String(), auth: h}
}

// embiPoints are stops inside Embi.
var embiPoints = []map[string]any{
	{"lat": 48.8264, "lon": 58.1442, "address": "Жиенқали Тілепбергенов көшесі, 12"},
	{"lat": 48.8291, "lon": 58.1504, "address": "Теміржол вокзалы"},
	{"lat": 48.8310, "lon": 58.1550, "address": "Орталық базар"},
}

// createOrder quotes a taxi route with n stops and books it at fare.
func (e *flowEnv) createOrder(p flowUser, n, fare int) string {
	e.t.Helper()
	stops := append([]map[string]any(nil), embiPoints[:n-1]...)
	stops = append(stops, embiPoints[len(embiPoints)-1])
	q := e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/routes/preview",
		map[string]any{"category": "taxi", "stops": stops}, p.auth), "route preview")
	o := e.must(http.StatusCreated, e.call(e.h, http.MethodPost, "/api/v1/orders", map[string]any{
		"quote_id": q.str("quote_id"), "fare": fare, "payment_method": "cash", "idempotency_key": uuid.NewString(),
	}, p.auth), "create order")
	if o.body["status"] != "searching" {
		e.t.Fatalf("new order: %s", o.raw)
	}
	return o.str("id")
}

// wallet checks the driver's wallet as the driver app sees it.
func (e *flowEnv) wallet(d flowDriver, balance, reserved int64) {
	e.t.Helper()
	w := e.must(http.StatusOK, e.call(e.h, http.MethodGet, "/api/v1/driver/wallet", nil, d.auth), "wallet")
	if int64(w.body["balance"].(float64)) != balance || int64(w.body["reserved"].(float64)) != reserved ||
		int64(w.body["available"].(float64)) != balance-reserved {
		e.t.Fatalf("wallet of %s: %s, want balance %d reserved %d", d.driverID, w.raw, balance, reserved)
	}
}

// offersSent marks the order's offer messages as delivered by the driver bot,
// as the notification worker would.
func (e *flowEnv) offersSent(orderID string) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		UPDATE notification_outbox SET status = 'sent', sent_at = now(), telegram_message_id = 4242
		WHERE template = 'order_available_driver' AND params->>'order_id' = $1`, orderID); err != nil {
		e.t.Fatal(err)
	}
}

// ledgerTypes checks the order's ledger entries (each exactly once).
func (e *flowEnv) ledgerTypes(d flowDriver, orderID string, want ...string) {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `
		SELECT l.entry_type FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id
		WHERE w.driver_id = $1 AND l.order_id = $2 ORDER BY 1`, d.driverID, orderID)
	if err != nil {
		e.t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		e.t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		e.t.Fatalf("ledger of order %s: %v, want %v", orderID, got, want)
	}
}

type outboxRow struct {
	template string
	userID   uuid.UUID
	bot      string
	dedupe   string
	params   map[string]any
}

func (e *flowEnv) outbox() []outboxRow {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT template, user_id, bot, dedupe_key, params FROM notification_outbox ORDER BY created_at`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		var raw []byte
		if err := rows.Scan(&r.template, &r.userID, &r.bot, &r.dedupe, &raw); err != nil {
			e.t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber() // as the worker decodes them
		if err := dec.Decode(&r.params); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// --- realtime capture -----------------------------------------------------------

type eventLog struct {
	mu     sync.Mutex
	scopes map[string]map[string]bool // type → scopes seen
}

// listenEvents LISTENs on the realtime channel with its own connection, as an
// API instance does, so only committed events are recorded.
func listenEvents(t *testing.T, pool *pgxpool.Pool) *eventLog {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+realtime.Channel); err != nil {
		t.Fatal(err)
	}
	l := &eventLog{scopes: map[string]map[string]bool{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				return
			}
			var ev struct {
				Type   string   `json:"type"`
				Scopes []string `json:"scopes"`
			}
			if json.Unmarshal([]byte(n.Payload), &ev) != nil {
				continue
			}
			l.mu.Lock()
			if l.scopes[ev.Type] == nil {
				l.scopes[ev.Type] = map[string]bool{}
			}
			for _, s := range ev.Scopes {
				l.scopes[ev.Type][s] = true
			}
			l.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close(context.Background())
	})
	return l
}

// waitFor waits until every event type in want was seen with (at least) the
// listed scopes.
func (l *eventLog) waitFor(t *testing.T, want map[string][]string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		missing := l.missing(want)
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("realtime events not published:\n  %s", strings.Join(missing, "\n  "))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (l *eventLog) missing(want map[string][]string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for typ, scopes := range want {
		got := l.scopes[typ]
		if got == nil {
			out = append(out, typ+" (never)")
			continue
		}
		for _, s := range scopes {
			if !got[s] {
				out = append(out, typ+" without scope "+s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// --- OSRM ---------------------------------------------------------------------

// fakeOSRMRoute answers /route/v1/driving/<lon,lat;...> like osrm-routed: one
// leg per pair of points at 1 km and 2 min each, the points as geometry.
func fakeOSRMRoute(w http.ResponseWriter, r *http.Request) {
	coords, ok := strings.CutPrefix(r.URL.Path, "/route/v1/driving/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	var pts [][2]float64
	for _, c := range strings.Split(coords, ";") {
		lon, lat, _ := strings.Cut(c, ",")
		x, _ := strconv.ParseFloat(lon, 64)
		y, _ := strconv.ParseFloat(lat, 64)
		pts = append(pts, [2]float64{x, y})
	}
	var legs []map[string]any
	var wps []map[string]any
	for i, p := range pts {
		wps = append(wps, map[string]any{"hint": "h", "distance": 5.0, "name": "көше", "location": p})
		if i > 0 {
			legs = append(legs, map[string]any{"distance": 1000.0, "duration": 120.0, "summary": "", "weight": 120.0})
		}
	}
	total := float64(len(legs))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code": "Ok",
		"routes": []any{map[string]any{
			"distance": 1000 * total, "duration": 120 * total, "weight": 120 * total, "weight_name": "routability",
			"geometry": map[string]any{"type": "LineString", "coordinates": pts},
			"legs":     legs,
		}},
		"waypoints": wps,
	})
}

// Empty recognition must go to review rather than approve or reject a driver.
type uncertainIdentityEngine struct{}

func (uncertainIdentityEngine) Read(context.Context, verification.Job) (verification.Result, error) {
	return verification.Result{}, nil
}
