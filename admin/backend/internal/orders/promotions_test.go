package orders_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/marketing"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
)

// Promotions end to end (ARCHITECTURE §23): the customer's two launch
// campaigns through the real order API, wallet and award worker.

type promoEnv struct {
	*env
	mk *marketing.Service
}

func newPromoEnv(t *testing.T) *promoEnv {
	t.Helper()
	e := newEnv(t)
	mk := marketing.New(e.pool, marketing.Options{PassengerBotUsername: "JanTaxiBot", Log: slog.New(slog.DiscardHandler)})
	r := chi.NewRouter()
	mk.MountPassenger(r.With(e.auth.RequireApp(auth.AppPassenger)))
	mk.MountDriver(r.With(e.auth.RequireApp(auth.AppDriver), e.auth.LoadDriverProfile(true)))
	r.Mount("/", e.handler)
	e.handler = r
	return &promoEnv{env: e, mk: mk}
}

// activate switches a seeded campaign on (as an approver would), slightly in
// the past so rides completed now count.
func (e *promoEnv) activate(key string) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		UPDATE marketing_campaigns SET status = 'active', active_since = now() - interval '1 minute',
		       activated_at = now() WHERE key = $1`, key); err != nil {
		e.t.Fatalf("activate %s: %v", key, err)
	}
}

func (e *promoEnv) process() {
	e.t.Helper()
	if _, err := e.mk.ProcessEvents(context.Background()); err != nil {
		e.t.Fatalf("process events: %v", err)
	}
}

// ride takes an order of passenger pc through accept, start and complete
// with drv and returns it.
func (e *promoEnv) ride(drv testDriver, pid uuid.UUID, pc caller, fare int, promo map[string]any) orders.Order {
	e.t.Helper()
	o := e.create(pid, pc, fare, promo, http.StatusCreated)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/start", o.ID), nil), http.StatusOK, "")
	// A real ride takes minutes (rides shorter than the referral campaign's
	// minimum go to review).
	if _, err := e.pool.Exec(context.Background(), `UPDATE orders SET started_at = now() - interval '10 minutes' WHERE id = $1`, o.ID); err != nil {
		e.t.Fatal(err)
	}
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil), http.StatusOK, "")
	return o
}

func (e *promoEnv) create(pid uuid.UUID, pc caller, fare int, promo map[string]any, status int) orders.Order {
	e.t.Helper()
	q := e.quote(pid, "taxi", embiStops[:2])
	body := taxiBody(q, fare)
	for k, v := range promo {
		body[k] = v
	}
	r := e.do(pc, http.MethodPost, "/orders", body)
	e.expect(r, status, "")
	var o orders.Order
	if status == http.StatusCreated {
		r.decode(e.t, &o)
	}
	return o
}

func (e *promoEnv) promotions(pc caller) marketing.PassengerPromotions {
	e.t.Helper()
	r := e.do(pc, http.MethodGet, "/promotions", nil)
	e.expect(r, http.StatusOK, "")
	var p marketing.PassengerPromotions
	r.decode(e.t, &p)
	return p
}

func (e *promoEnv) reconcile() marketing.Reconciliation {
	e.t.Helper()
	rep, err := marketing.Reconcile(context.Background(), e.pool)
	if err != nil {
		e.t.Fatalf("reconcile: %v", err)
	}
	if !rep.OK {
		e.t.Fatalf("reconciliation issues: %+v", rep.Issues)
	}
	return rep
}

func couponPromo(discount int64) map[string]any {
	return map[string]any{"promotion": "coupon", "promotion_discount": discount}
}

// TestFirstRideCoupon: the first completed ride earns one 400 ₸ coupon valid
// 72 hours; the next ride of 1 500 ₸ costs the passenger 1 100 ₸ in cash and
// the driver gets the 400 ₸ on the wallet once; nothing can reuse it.
func TestFirstRideCoupon(t *testing.T) {
	e := newPromoEnv(t)
	e.activate("first_ride_coupon_400")
	drv := e.driver(1000, true)
	pid, pc := e.passenger()

	if p := e.promotions(pc); len(p.Offers) != 1 || p.Offers[0].Amount != 400 || len(p.Coupons) != 0 {
		t.Fatalf("new passenger promotions: %+v", p)
	}
	// Ordering, accepting or cancelling earns nothing.
	o := e.create(pid, pc, 800, nil, http.StatusCreated)
	e.expect(e.do(pc, http.MethodPost, path("/orders/%s/cancel", o.ID), cancelBody("changed_plans")), http.StatusOK, "")
	e.process()
	if n := e.count(`SELECT count(*) FROM marketing_coupons WHERE user_id = $1`, pid); n != 0 {
		t.Fatalf("coupon after a cancelled order: %d", n)
	}

	first := e.ride(drv, pid, pc, 800, nil)
	e.process()
	e.process() // a replayed run awards nothing more
	var amount int64
	var validity time.Duration
	var source uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `
		SELECT amount, expires_at - issued_at, source_order_id FROM marketing_coupons WHERE user_id = $1`, pid).
		Scan(&amount, &validity, &source); err != nil {
		t.Fatalf("coupon: %v", err)
	}
	if amount != 400 || validity != 72*time.Hour || source != first.ID {
		t.Fatalf("coupon %d valid %v from %s", amount, validity, source)
	}
	if n := e.count(`SELECT count(*) FROM marketing_coupons WHERE user_id = $1`, pid); n != 1 {
		t.Fatalf("coupons = %d", n)
	}
	if e.outbox("promo_coupon_issued", pid) != 1 {
		t.Fatal("coupon notification missing")
	}
	p := e.promotions(pc)
	if len(p.Coupons) != 1 || p.Coupons[0].Status != "issued" || len(p.Offers) != 0 || p.Referral.State != "unavailable" {
		t.Fatalf("after first ride: %+v", p)
	}
	if p.ServerTime.IsZero() || !p.Coupons[0].ExpiresAt.After(p.ServerTime) {
		t.Fatalf("server time %v expiry %v", p.ServerTime, p.Coupons[0].ExpiresAt)
	}

	// A discount the server would not give is refused, nothing reserved.
	r := e.do(pc, http.MethodPost, "/orders", func() map[string]any {
		b := taxiBody(e.quote(pid, "taxi", embiStops[:2]), 1500)
		b["promotion"], b["promotion_discount"] = "coupon", 300
		return b
	}())
	e.expect(r, http.StatusConflict, "promotion_changed")
	if d := r.err(t).Error.Details; d["discount"] != float64(400) || d["reason"] != "discount_changed" {
		t.Fatalf("details %+v", d)
	}

	// Second ride: 1 500 ₸ fare, 400 ₸ coupon, 1 100 ₸ cash.
	balBefore, _ := e.wallet(drv.DriverID)
	second := e.create(pid, pc, 1500, couponPromo(400), http.StatusCreated)
	if second.Fare != 1500 || second.PromoDiscount != 400 || second.CashDue != 1100 || second.Promotion == nil ||
		second.Promotion.Status != "reserved" {
		t.Fatalf("second order: fare %d discount %d cash %d %+v", second.Fare, second.PromoDiscount, second.CashDue, second.Promotion)
	}
	var avail dispatch.Available
	ar := e.do(drv.c, http.MethodGet, "/driver/orders", nil)
	e.expect(ar, http.StatusOK, "")
	ar.decode(t, &avail)
	if len(avail.Items) != 1 || avail.Items[0].PromoDiscount != 400 || avail.Items[0].CashDue != 1100 || avail.Items[0].Fare != 1500 {
		t.Fatalf("driver card: %+v", avail.Items)
	}
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", second.ID), nil), http.StatusOK, "")
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/start", second.ID), nil), http.StatusOK, "")
	var dd orders.DriverOrderDetail
	cr := e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", second.ID), nil)
	e.expect(cr, http.StatusOK, "")
	cr.decode(t, &dd)
	if dd.CashDue != 1100 || dd.Promotion == nil || dd.Promotion.Status != "settled" || dd.Promotion.Compensation == nil ||
		*dd.Promotion.Compensation != 400 || dd.Promotion.Title != nil {
		t.Fatalf("driver completion view: %+v", dd.Promotion)
	}
	// Commission on the gross fare (fixed 50 ₸) is unchanged; +400 ₸ compensation.
	if bal, _ := e.wallet(drv.DriverID); bal != balBefore-50+400 {
		t.Fatalf("driver balance %d, want %d", bal, balBefore-50+400)
	}
	if n := e.count(`SELECT count(*) FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id
		WHERE w.driver_id = $1 AND l.entry_type = 'promo_compensation' AND l.balance_delta = 400 AND l.order_id = $2`,
		drv.DriverID, second.ID); n != 1 {
		t.Fatalf("compensation entries = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM notification_outbox WHERE template = 'order_completed_driver'
		AND params->>'promo_compensation' = '400' AND params->>'cash_due' = '1100'`); n != 1 {
		t.Fatalf("driver completion message with compensation = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM marketing_coupons WHERE user_id = $1 AND status = 'redeemed' AND redeemed_order_id = $2`,
		pid, second.ID); n != 1 {
		t.Fatal("coupon not redeemed")
	}
	// A repeated completion changes nothing (409, no second credit).
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", second.ID), nil), http.StatusConflict, "invalid_transition")
	if bal, _ := e.wallet(drv.DriverID); bal != balBefore-50+400 {
		t.Fatalf("balance moved on replay: %d", bal)
	}
	// The coupon cannot be used again; the second ride earned no coupon.
	e.process()
	e.create(pid, pc, 1500, couponPromo(400), http.StatusConflict)
	if n := e.count(`SELECT count(*) FROM marketing_coupons WHERE user_id = $1`, pid); n != 1 {
		t.Fatalf("coupons after second ride = %d", n)
	}
	rep := e.reconcile()
	if rep.Totals.DiscountsSettled != 400 || rep.Totals.CompensationCredited != 400 {
		t.Fatalf("totals %+v", rep.Totals)
	}
}

// TestCouponReleasedOnCancel: a cancelled coupon order returns the coupon,
// which the next order uses; no driver is compensated for the cancelled one.
func TestCouponReleasedOnCancel(t *testing.T) {
	e := newPromoEnv(t)
	e.activate("first_ride_coupon_400")
	drv := e.driver(2000, true)
	pid, pc := e.passenger()
	e.ride(drv, pid, pc, 800, nil)
	e.process()

	o := e.create(pid, pc, 1500, couponPromo(400), http.StatusCreated)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	e.expect(e.do(pc, http.MethodPost, path("/orders/%s/cancel", o.ID), cancelBody("changed_plans")), http.StatusOK, "")
	if n := e.count(`SELECT count(*) FROM marketing_coupons WHERE user_id = $1 AND status = 'issued'`, pid); n != 1 {
		t.Fatal("coupon not released")
	}
	if n := e.count(`SELECT count(*) FROM order_promotions WHERE order_id = $1 AND status = 'released'`, o.ID); n != 1 {
		t.Fatal("promotion not released")
	}
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE entry_type = 'promo_compensation'`); n != 0 {
		t.Fatalf("compensation on a cancelled order: %d", n)
	}
	next := e.create(pid, pc, 1500, couponPromo(400), http.StatusCreated)
	if next.CashDue != 1100 {
		t.Fatalf("re-used coupon: %+v", next)
	}
	e.reconcile()
}

// TestCouponReservedOnce: simultaneous orders of one passenger reserve the
// coupon at most once.
func TestCouponReservedOnce(t *testing.T) {
	e := newPromoEnv(t)
	e.activate("first_ride_coupon_400")
	drv := e.driver(2000, true)
	pid, pc := e.passenger()
	e.ride(drv, pid, pc, 800, nil)
	e.process()

	const n = 8
	bodies := make([]map[string]any, n)
	for i := range bodies {
		bodies[i] = taxiBody(e.quote(pid, "taxi", embiStops[:2]), 1500)
		bodies[i]["promotion"], bodies[i]["promotion_discount"] = "coupon", 400
	}
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := range bodies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = e.do(pc, http.MethodPost, "/orders", bodies[i]).Status
		}(i)
	}
	wg.Wait()
	created := 0
	for _, s := range statuses {
		if s == http.StatusCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created %d orders: %v", created, statuses)
	}
	if c := e.count(`SELECT count(*) FROM order_promotions WHERE user_id = $1 AND status = 'reserved'`, pid); c != 1 {
		t.Fatalf("reservations = %d", c)
	}
	e.reconcile()
}

// TestCouponExpiryAndMinimumFare: an expired coupon is refused at once (even
// before the expiry job), and a fare below the coupon's minimum never makes
// the cash negative.
func TestCouponExpiryAndMinimumFare(t *testing.T) {
	e := newPromoEnv(t)
	e.activate("first_ride_coupon_400")
	drv := e.driver(2000, true)
	pid, pc := e.passenger()
	e.ride(drv, pid, pc, 800, nil)
	e.process()

	if _, err := e.pool.Exec(context.Background(), `UPDATE marketing_coupons SET min_order_fare = 1000 WHERE user_id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	r := e.do(pc, http.MethodPost, "/orders", func() map[string]any {
		b := taxiBody(e.quote(pid, "taxi", embiStops[:2]), 800)
		b["promotion"] = "coupon"
		return b
	}())
	e.expect(r, http.StatusConflict, "promotion_changed")
	if r.err(t).Error.Details["reason"] != "fare_below_minimum" {
		t.Fatalf("reason %+v", r.err(t).Error.Details)
	}
	if _, err := e.pool.Exec(context.Background(), `
		UPDATE marketing_coupons SET issued_at = now() - interval '73 hours', expires_at = now() - interval '1 hour'
		WHERE user_id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	r = e.do(pc, http.MethodPost, "/orders", func() map[string]any {
		b := taxiBody(e.quote(pid, "taxi", embiStops[:2]), 1500)
		b["promotion"] = "coupon"
		return b
	}())
	e.expect(r, http.StatusConflict, "promotion_changed")
	if r.err(t).Error.Details["reason"] != "coupon_expired" {
		t.Fatalf("reason %+v", r.err(t).Error.Details)
	}
	if p := e.promotions(pc); len(p.Coupons) != 1 || p.Coupons[0].Status != "expired" {
		t.Fatalf("expired coupon view: %+v", p.Coupons)
	}
	if n, err := e.mk.ExpireCoupons(context.Background()); err != nil || n != 1 {
		t.Fatalf("expire job: %d %v", n, err)
	}
	// A coupon can never make the cash negative: the schema refuses it.
	if _, err := e.pool.Exec(context.Background(), `UPDATE marketing_coupons SET min_order_fare = 100 WHERE user_id = $1`, pid); err == nil {
		t.Fatal("min_order_fare below the amount was accepted")
	}
}

// TestReferralOncePerReferrer: the inviter (allowed only after redeeming the
// first-ride coupon) gets 300 ₸ once, however many friends ride; friends get
// their own first-ride coupon, never the referral bonus; the bonus pays part
// of a later ride and the driver is compensated.
func TestReferralOncePerReferrer(t *testing.T) {
	e := newPromoEnv(t)
	e.activate("first_ride_coupon_400")
	e.activate("referral_300_once")
	drv := e.driver(5000, true)
	ctx := context.Background()

	aID, ac := e.passenger()
	if p := e.promotions(ac); p.Referral.State != "locked" {
		t.Fatalf("referral before the first ride: %+v", p.Referral)
	}
	e.ride(drv, aID, ac, 800, nil)
	e.process()
	if p := e.promotions(ac); p.Referral.State != "locked" || p.Referral.LockReason != "coupon_not_redeemed" {
		t.Fatalf("referral while the coupon is unused: %+v", p.Referral)
	}
	e.ride(drv, aID, ac, 1500, couponPromo(400))
	var ref marketing.ReferralView
	r := e.do(ac, http.MethodGet, "/promotions/referral", nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &ref)
	if ref.State != "unlocked" || len(ref.Code) != 8 || ref.Link != "https://t.me/JanTaxiBot?start=ref_"+ref.Code ||
		ref.RewardAmount != 300 {
		t.Fatalf("referral view: %+v", ref)
	}

	capture := func(user uuid.UUID, payload string) marketing.CaptureResult {
		t.Helper()
		res, err := e.mk.CaptureReferral(ctx, user, payload, "bot_start")
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		return res
	}
	if res := capture(aID, "ref_"+ref.Code); res.Attributed || res.Reason != "self_referral" {
		t.Fatalf("self referral: %+v", res)
	}
	old, _ := testutil.CreateUser(t, e.pool)
	if _, err := e.pool.Exec(ctx, `UPDATE users SET created_at = now() - interval '2 days' WHERE id = $1`, old); err != nil {
		t.Fatal(err)
	}
	if res := capture(old, "ref_"+ref.Code); res.Attributed || res.Reason != "invitee_not_new" {
		t.Fatalf("existing user: %+v", res)
	}
	if res := capture(old, "ref_ZZZZ2222"); res.Attributed || res.Reason != "unknown_code" {
		t.Fatalf("unknown code: %+v", res)
	}

	friends := make([]struct {
		id uuid.UUID
		c  caller
	}, 3)
	for i := range friends {
		friends[i].id, friends[i].c = e.passenger()
		if res := capture(friends[i].id, "ref_"+ref.Code); !res.Attributed {
			t.Fatalf("friend %d: %+v", i, res)
		}
		// A repeated /start never rewrites the attribution.
		if res := capture(friends[i].id, "ref_"+ref.Code); res.Attributed || res.Outcome != "duplicate" {
			t.Fatalf("repeat: %+v", res)
		}
	}
	for _, f := range friends {
		e.ride(drv, f.id, f.c, 800, nil)
	}
	e.process()
	if n := e.count(`SELECT count(*) FROM marketing_grants WHERE user_id = $1 AND reason = 'referral_reward'`, aID); n != 1 {
		t.Fatalf("referral rewards = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM referral_attributions WHERE referrer_user_id = $1 AND status = 'capped'`, aID); n != 2 {
		t.Fatalf("capped = %d", n)
	}
	for _, f := range friends {
		if b, _ := marketing.GetBonus(ctx, e.pool, f.id); b.Balance != 0 {
			t.Fatalf("invitee got a bonus: %+v", b)
		}
		if n := e.count(`SELECT count(*) FROM marketing_coupons WHERE user_id = $1`, f.id); n != 1 {
			t.Fatalf("invitee coupon = %d", n)
		}
	}
	if b, _ := marketing.GetBonus(ctx, e.pool, aID); b.Balance != 300 || b.Available != 300 {
		t.Fatalf("referrer bonus: %+v", b)
	}
	if e.outbox("promo_referral_reward", aID) != 1 {
		t.Fatal("reward notification missing")
	}

	// The bonus pays part of the next ride.
	balBefore, _ := e.wallet(drv.DriverID)
	o := e.create(aID, ac, 800, map[string]any{"promotion": "bonus", "promotion_discount": 300}, http.StatusCreated)
	if o.CashDue != 500 {
		t.Fatalf("bonus order: %+v", o)
	}
	if b, _ := marketing.GetBonus(ctx, e.pool, aID); b.Reserved != 300 || b.Available != 0 {
		t.Fatalf("bonus reserved: %+v", b)
	}
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/start", o.ID), nil), http.StatusOK, "")
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil), http.StatusOK, "")
	if b, _ := marketing.GetBonus(ctx, e.pool, aID); b.Balance != 0 || b.Reserved != 0 {
		t.Fatalf("bonus after the ride: %+v", b)
	}
	if bal, _ := e.wallet(drv.DriverID); bal != balBefore-50+300 {
		t.Fatalf("driver balance %d, want %d", bal, balBefore-50+300)
	}
	e.reconcile()
}

// TestReferralImmediateAccess: an approver can open invitations without a
// first-ride coupon; changing access preserves the custom reward and its cap.
func TestReferralImmediateAccess(t *testing.T) {
	e := newPromoEnv(t)
	ctx := context.Background()
	admin := chi.NewRouter()
	e.mk.MountAdmin(admin.With(testAdmin))
	router := chi.NewRouter()
	router.Mount("/marketing-admin", admin)
	router.Mount("/", e.handler)
	e.handler = router

	var campaignID uuid.UUID
	var version int
	if err := e.pool.QueryRow(ctx, `SELECT id, current_version FROM marketing_campaigns WHERE key = 'referral_300_once'`).
		Scan(&campaignID, &version); err != nil {
		t.Fatal(err)
	}
	campaignPath := "/marketing-admin/marketing/campaigns/" + campaignID.String()
	transition := func(action string) {
		t.Helper()
		e.expect(e.do(e.operator(), http.MethodPost, campaignPath+"/transition", map[string]any{
			"action": action, "reason": "Referral access regression", "confirm": true, "expected_version": version,
		}), http.StatusOK, "")
	}
	setAccess := func(unlock string) {
		t.Helper()
		r := e.do(e.operator(), http.MethodPost, campaignPath+"/versions", map[string]any{
			"rules": map[string]any{"reward_amount": 200, "categories": []string{"taxi"},
				"attribution_window_minutes": 60, "unlock": unlock, "min_ride_seconds": 60},
			"change_note": "Configure invitation access",
		})
		e.expect(r, http.StatusOK, "")
		var c marketing.Campaign
		r.decode(t, &c)
		version = c.Version
	}
	referral := func(pc caller) marketing.ReferralView {
		t.Helper()
		r := e.do(pc, http.MethodGet, "/promotions/referral", nil)
		e.expect(r, http.StatusOK, "")
		var ref marketing.ReferralView
		r.decode(t, &ref)
		return ref
	}

	// Reproduce the reported screen: the campaign is active with a custom
	// 200 ₸ reward, but its stored coupon rule still locks this passenger.
	setAccess("coupon_redeemed")
	transition("activate")
	aID, ac := e.passenger()
	if ref := referral(ac); ref.State != "locked" || ref.LockReason != "coupon_not_redeemed" || ref.RewardAmount != 200 || ref.Link != "" {
		t.Fatalf("coupon access before the fix: %+v", ref)
	}
	setAccess("immediate")
	previewResponse := e.do(e.operator(), http.MethodGet, campaignPath+"/preview", nil)
	e.expect(previewResponse, http.StatusOK, "")
	var preview marketing.CampaignPreview
	previewResponse.decode(t, &preview)
	if preview.PerPerson != 200 {
		t.Fatalf("immediate preview reward: %+v", preview)
	}
	for _, warning := range preview.Warnings {
		if warning == "legacy_passengers_locked" || warning == "coupon_campaign_not_active" {
			t.Fatalf("immediate preview still requires coupons: %+v", preview.Warnings)
		}
	}
	ref := referral(ac)
	if ref.State != "unlocked" || len(ref.Code) != 8 || ref.Link != "https://t.me/JanTaxiBot?start=ref_"+ref.Code ||
		ref.AppLink != "https://t.me/JanTaxiBot?startapp=ref_"+ref.Code || ref.RewardAmount != 200 {
		t.Fatalf("immediate referral view: %+v", ref)
	}
	if p := e.promotions(ac); p.Referral.State != "unlocked" || p.Referral.RewardAmount != 200 || len(p.Coupons) != 0 {
		t.Fatalf("immediate promotions: %+v", p)
	}
	if n := e.count(`SELECT count(*) FROM orders WHERE passenger_id = $1`, aID); n != 0 {
		t.Fatalf("inviter unexpectedly needed a ride: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM marketing_campaign_versions WHERE campaign_id = $1 AND rules->>'unlock' = 'coupon_redeemed'`, campaignID); n == 0 {
		t.Fatal("changing access lost the earlier version")
	}

	// Campaign and emergency switches still control whether the link is shown.
	transition("pause")
	if paused := referral(ac); paused.State != "unavailable" || paused.Code != "" || paused.Link != "" {
		t.Fatalf("paused referral: %+v", paused)
	}
	friendID, fc := e.passenger()
	if res, err := e.mk.CaptureReferral(ctx, friendID, "ref_"+ref.Code, "bot_start"); err != nil || res.Attributed || res.Reason != "campaign_inactive" {
		t.Fatalf("paused capture: %+v %v", res, err)
	}
	transition("resume")
	if resumed := referral(ac); resumed.Code != ref.Code || resumed.Link != ref.Link {
		t.Fatalf("resume changed the invitation: %+v", resumed)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE app_settings SET value = 'false'::jsonb WHERE key = 'marketing.awards_enabled'`); err != nil {
		t.Fatal(err)
	}
	if paused := referral(ac); paused.State != "locked" || paused.LockReason != "paused" || paused.Link != "" {
		t.Fatalf("awards disabled: %+v", paused)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE app_settings SET value = 'true'::jsonb WHERE key = 'marketing.awards_enabled'`); err != nil {
		t.Fatal(err)
	}

	// Both attribution and payout use the new rule: no coupon or ride is
	// required from the inviter, and a second friend earns no second reward.
	drv := e.driver(5000, true)
	for i := range 2 {
		if i > 0 {
			friendID, fc = e.passenger()
		}
		if res, err := e.mk.CaptureReferral(ctx, friendID, "ref_"+ref.Code, "bot_start"); err != nil || !res.Attributed {
			t.Fatalf("friend %d capture: %+v %v", i, res, err)
		}
		if i == 0 {
			if b, err := marketing.GetBonus(ctx, e.pool, aID); err != nil || b.Balance != 0 {
				t.Fatalf("reward before the friend's first ride: %+v %v", b, err)
			}
		}
		e.ride(drv, friendID, fc, 800, nil)
		e.process()
	}
	e.process()
	if b, err := marketing.GetBonus(ctx, e.pool, aID); err != nil || b.Balance != 200 || b.Available != 200 {
		t.Fatalf("custom referral reward: %+v %v", b, err)
	}
	if n := e.count(`SELECT count(*) FROM marketing_grants WHERE user_id = $1 AND reason = 'referral_reward'`, aID); n != 1 {
		t.Fatalf("referral rewards = %d", n)
	}
	if e.outbox("promo_referral_reward", aID) != 1 {
		t.Fatal("reward notification missing or duplicated")
	}
	if _, err := e.pool.Exec(ctx, `UPDATE referral_codes SET disabled_at = now() WHERE user_id = $1`, aID); err != nil {
		t.Fatal(err)
	}
	if disabled := referral(ac); disabled.State != "locked" || disabled.LockReason != "disabled" || disabled.Code != "" || disabled.Link != "" {
		t.Fatalf("disabled referral code: %+v", disabled)
	}
	friendID, _ = e.passenger()
	if res, err := e.mk.CaptureReferral(ctx, friendID, "ref_"+ref.Code, "bot_start"); err != nil || res.Attributed || res.Reason != "unknown_code" {
		t.Fatalf("disabled code capture: %+v %v", res, err)
	}
	e.reconcile()
}

// Short first rides wait for an administrator; exactly 60 seconds qualifies.
// A missing invitee phone must not leave the award event stuck on a NULL scan.
func TestReferralDurationAndReview(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration time.Duration
		decision string
	}{
		{"below_minimum_approved", 60*time.Second - time.Millisecond, "approve"},
		{"at_minimum", 60 * time.Second, ""},
		{"above_minimum", 61 * time.Second, ""},
		{"short_ride_rejected", 2 * time.Second, "reject"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPromoEnv(t)
			ctx := context.Background()
			if _, err := e.pool.Exec(ctx, `
				INSERT INTO marketing_campaign_versions (campaign_id, version, title, rules)
				SELECT id, 2, '{"kk":"Досыңды шақыр"}',
				       '{"reward_amount":200,"categories":["taxi"],"attribution_window_minutes":60,"unlock":"immediate","min_ride_seconds":60}'
				FROM marketing_campaigns WHERE key = 'referral_300_once';
				UPDATE marketing_campaigns SET current_version = 2 WHERE key = 'referral_300_once'`); err != nil {
				t.Fatal(err)
			}
			e.activate("referral_300_once")
			admin := chi.NewRouter()
			e.mk.MountAdmin(admin.With(testAdmin))
			router := chi.NewRouter()
			router.Mount("/marketing-admin", admin)
			router.Mount("/", e.handler)
			e.handler = router

			referrerID, referrer := e.passenger()
			if _, err := e.pool.Exec(ctx, `UPDATE users SET phone_e164 = '+77000000001' WHERE id = $1`, referrerID); err != nil {
				t.Fatal(err)
			}
			view := func() marketing.ReferralView {
				t.Helper()
				r := e.do(referrer, http.MethodGet, "/promotions/referral", nil)
				e.expect(r, http.StatusOK, "")
				var v marketing.ReferralView
				r.decode(t, &v)
				return v
			}
			code := view().Code
			inviteeID, invitee := e.passenger()
			if res, err := e.mk.CaptureReferral(ctx, inviteeID, "ref_"+code, "bot_start"); err != nil || !res.Attributed {
				t.Fatalf("capture: %+v %v", res, err)
			}
			driver := e.driver(5000, true)
			order := e.ride(driver, inviteeID, invitee, 800, nil)
			if _, err := e.pool.Exec(ctx, `UPDATE orders SET started_at = completed_at - $2::bigint * interval '1 microsecond' WHERE id = $1`,
				order.ID, tc.duration.Microseconds()); err != nil {
				t.Fatal(err)
			}

			listener, err := e.pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Release()
			if _, err := listener.Exec(ctx, "LISTEN "+realtime.Channel); err != nil {
				t.Fatal(err)
			}
			expectUpdate := func() {
				t.Helper()
				waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				for {
					n, err := listener.Conn().WaitForNotification(waitCtx)
					if err != nil {
						t.Fatalf("referrer did not receive a promotion update: %v", err)
					}
					var ev struct {
						Type   string   `json:"type"`
						Scopes []string `json:"scopes"`
					}
					if err := json.Unmarshal([]byte(n.Payload), &ev); err != nil {
						t.Fatal(err)
					}
					if ev.Type == marketing.EventPromotionsUpdated && slices.Contains(ev.Scopes, realtime.UserScope(referrerID)) {
						return
					}
				}
			}

			e.process()
			expectUpdate()
			var attributionID uuid.UUID
			var status string
			if err := e.pool.QueryRow(ctx, `SELECT id, status FROM referral_attributions WHERE invitee_user_id = $1`, inviteeID).
				Scan(&attributionID, &status); err != nil {
				t.Fatal(err)
			}
			if n := e.count(`SELECT count(*) FROM marketing_events WHERE status = 'done'`); n != 1 {
				t.Fatalf("processed awards = %d", n)
			}
			if tc.decision != "" {
				v := view()
				if status != marketing.ReferralReview || v.Earned != 0 || len(v.Friends) != 1 ||
					v.Friends[0].Status != "not_counted" || !v.Friends[0].UnderReview {
					t.Fatalf("pending review: status %s, view %+v", status, v)
				}
				queue := e.do(e.operator(), http.MethodGet, "/marketing-admin/marketing/referrals?status=review", nil)
				e.expect(queue, http.StatusOK, "")
				var pending struct {
					Total int `json:"total"`
					Items []struct {
						ID     uuid.UUID `json:"id"`
						Reason string    `json:"reason"`
					} `json:"items"`
				}
				queue.decode(t, &pending)
				if pending.Total != 1 || len(pending.Items) != 1 || pending.Items[0].ID != attributionID || pending.Items[0].Reason != "short_ride" {
					t.Fatalf("administrator review queue: %+v", pending)
				}
				reviewPath := path("/marketing-admin/marketing/referrals/%s/review", attributionID)
				body := map[string]any{"decision": tc.decision, "reason": "Checked the customer's test ride"}
				e.expect(e.do(e.operator(), http.MethodPost, reviewPath, body), http.StatusOK, "")
				expectUpdate()
				e.expect(e.do(e.operator(), http.MethodPost, reviewPath, body), http.StatusConflict, "invalid_transition")
				if n := e.count(`SELECT count(*) FROM admin_audit_logs WHERE action = $1`, "marketing.referral."+tc.decision); n != 1 {
					t.Fatalf("review audit entries = %d", n)
				}
			} else if status != marketing.ReferralRewarded {
				t.Fatalf("qualifying ride was not rewarded: %s", status)
			}
			e.process() // replay cannot pay a second time
			wantStatus, wantEarned := marketing.ReferralRewarded, int64(200)
			if tc.decision == "reject" {
				wantStatus, wantEarned = "not_counted", 0
			}
			v := view()
			if v.Earned != wantEarned || len(v.Friends) != 1 || v.Friends[0].Status != wantStatus || v.Friends[0].UnderReview {
				t.Fatalf("decided referral: %+v", v)
			}
			balance, err := marketing.GetBonus(ctx, e.pool, referrerID)
			if err != nil || balance.Available != wantEarned {
				t.Fatalf("bonus: %+v %v", balance, err)
			}
			e.reconcile()
		})
	}
}

// TestReferralRaceRewardsOnce: two friends' first rides processed at the same
// time still reward the inviter once.
func TestReferralRaceRewardsOnce(t *testing.T) {
	e := newPromoEnv(t)
	e.activate("referral_300_once")
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO marketing_campaign_versions (campaign_id, version, title, rules)
		SELECT id, 2, '{"kk":"Досыңды шақыр","ru":"Пригласи друга","en":"Invite a friend"}',
		       '{"reward_amount":300,"categories":["taxi"],"attribution_window_minutes":60,"unlock":"first_ride_completed","min_ride_seconds":0}'
		FROM marketing_campaigns WHERE key = 'referral_300_once';
		UPDATE marketing_campaigns SET current_version = 2 WHERE key = 'referral_300_once'`); err != nil {
		t.Fatal(err)
	}
	drv := e.driver(5000, true)
	aID, ac := e.passenger()
	e.ride(drv, aID, ac, 800, nil)
	var ref marketing.ReferralView
	r := e.do(ac, http.MethodGet, "/promotions/referral", nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &ref)
	if ref.State != "unlocked" {
		t.Fatalf("referral: %+v", ref)
	}
	for range 4 {
		fid, fc := e.passenger()
		if res, err := e.mk.CaptureReferral(context.Background(), fid, "ref_"+ref.Code, "miniapp"); err != nil || !res.Attributed {
			t.Fatalf("capture: %+v %v", res, err)
		}
		e.ride(drv, fid, fc, 800, nil)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.mk.ProcessEvents(context.Background())
		}()
	}
	wg.Wait()
	e.process()
	if n := e.count(`SELECT count(*) FROM marketing_grants WHERE user_id = $1`, aID); n != 1 {
		t.Fatalf("grants = %d", n)
	}
	if b, _ := marketing.GetBonus(context.Background(), e.pool, aID); b.Balance != 300 {
		t.Fatalf("bonus = %+v", b)
	}
	e.reconcile()
}

// TestAwardsSwitchedOff: with the campaign paused nothing is queued or
// awarded, and switching it on later never awards earlier rides.
func TestAwardsSwitchedOff(t *testing.T) {
	e := newPromoEnv(t)
	drv := e.driver(2000, true)
	pid, pc := e.passenger()
	e.ride(drv, pid, pc, 800, nil) // campaign still a draft
	e.activate("first_ride_coupon_400")
	e.process()
	if n := e.count(`SELECT count(*) FROM marketing_coupons`); n != 0 {
		t.Fatalf("retroactive coupon: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM marketing_events`); n != 0 {
		t.Fatalf("events queued while off: %d", n)
	}
}

// TestDisputeCancelledRideEarnsNothing: a first ride cancelled after
// completion by a dispute loses its coupon even while it is reserved on the
// next order, and a referral reward it earned is flagged for finance.
func TestDisputeCancelledRideEarnsNothing(t *testing.T) {
	e := newPromoEnv(t)
	e.activate("first_ride_coupon_400")
	e.activate("referral_300_once")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO marketing_campaign_versions (campaign_id, version, title, rules)
		SELECT id, 2, '{"kk":"Досыңды шақыр","ru":"Пригласи друга","en":"Invite a friend"}',
		       '{"reward_amount":300,"categories":["taxi"],"attribution_window_minutes":60,"unlock":"first_ride_completed","min_ride_seconds":60}'
		FROM marketing_campaigns WHERE key = 'referral_300_once';
		UPDATE marketing_campaigns SET current_version = 2 WHERE key = 'referral_300_once'`); err != nil {
		t.Fatal(err)
	}
	drv := e.driver(5000, true)
	disputeCancel := func(orderID uuid.UUID, pc caller) {
		t.Helper()
		e.expect(e.do(pc, http.MethodPost, path("/orders/%s/dispute", orderID), map[string]any{"reason": "Сапар болмады"}),
			http.StatusOK, "")
		var disputeID uuid.UUID
		if err := e.pool.QueryRow(ctx, `SELECT id FROM disputes WHERE order_id = $1 AND status = 'open'`, orderID).Scan(&disputeID); err != nil {
			t.Fatal(err)
		}
		e.expect(e.resolve(disputeID, map[string]any{"outcome": "cancelled", "resolution": "Сапар болмаған", "refund_commission": false}),
			http.StatusOK, "")
	}

	// Coupon reserved on the next order when its first ride is cancelled.
	pid, pc := e.passenger()
	first := e.ride(drv, pid, pc, 800, nil)
	e.process()
	next := e.create(pid, pc, 1500, couponPromo(400), http.StatusCreated)
	disputeCancel(first.ID, pc)
	e.expect(e.do(pc, http.MethodPost, path("/orders/%s/cancel", next.ID), cancelBody("changed_plans")), http.StatusOK, "")
	var status, reason string
	if err := e.pool.QueryRow(ctx, `SELECT status, COALESCE(void_reason, '') FROM marketing_coupons WHERE user_id = $1`, pid).
		Scan(&status, &reason); err != nil || status != "voided" || reason != "qualifying_ride_cancelled" {
		t.Fatalf("coupon of a cancelled first ride: %s %s %v", status, reason, err)
	}

	// The referral reward a cancelled ride earned is flagged, not kept silently.
	aID, ac := e.passenger()
	e.ride(drv, aID, ac, 800, nil)
	var ref marketing.ReferralView
	r := e.do(ac, http.MethodGet, "/promotions/referral", nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &ref)
	iID, ic := e.passenger()
	if res, err := e.mk.CaptureReferral(ctx, iID, "ref_"+ref.Code, "bot_start"); err != nil || !res.Attributed {
		t.Fatalf("capture: %+v %v", res, err)
	}
	invited := e.ride(drv, iID, ic, 800, nil)
	e.process()
	disputeCancel(invited.ID, ic)
	if err := e.pool.QueryRow(ctx, `SELECT status, COALESCE(reason, '') FROM referral_attributions WHERE invitee_user_id = $1`, iID).
		Scan(&status, &reason); err != nil || status != "rewarded" || reason != "qualifying_ride_cancelled" {
		t.Fatalf("referral of a cancelled ride: %s %s %v", status, reason, err)
	}
	e.reconcile()
}
