package orders_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
)

func deliveryBody(q pricing.StoredQuote, fare int, extra map[string]any) map[string]any {
	b := map[string]any{
		"quote_id": q.ID.String(), "fare": fare, "payment_method": "bank_transfer",
		"idempotency_key": uuid.NewString(),
	}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

// TestCreateIdempotentReplay: a replay returns the original order with 200,
// a reused key with another payload is a conflict and a consumed quote cannot
// create a second order.
func TestCreateIdempotentReplay(t *testing.T) {
	e := newEnv(t)
	pid, pc := e.passenger()
	q := e.quote(pid, "taxi", embiStops[:2])
	body := taxiBody(q, 800)

	r := e.do(pc, http.MethodPost, "/orders", body)
	e.expect(r, http.StatusCreated, "")
	var first orders.Order
	r.decode(t, &first)

	r = e.do(pc, http.MethodPost, "/orders", body)
	e.expect(r, http.StatusOK, "")
	var replay orders.Order
	r.decode(t, &replay)
	if replay.ID != first.ID || replay.Number != first.Number {
		t.Fatalf("replay returned %s, want %s", replay.ID, first.ID)
	}

	changed := taxiBody(q, 850)
	changed["idempotency_key"] = body["idempotency_key"]
	r = e.do(pc, http.MethodPost, "/orders", changed)
	e.expect(r, http.StatusConflict, "conflict")
	if r.err(t).Error.Details["reason"] != "idempotency_key_reused" {
		t.Fatalf("details: %s", r.Body)
	}

	// The same quote with a new key: the quote is consumed.
	r = e.do(pc, http.MethodPost, "/orders", taxiBody(q, 800))
	e.expect(r, http.StatusUnprocessableEntity, "quote_expired")
	if r.err(t).Error.Details["reason"] != "quote_used" {
		t.Fatalf("details: %s", r.Body)
	}

	if n := e.count(`SELECT count(*) FROM orders WHERE passenger_id = $1`, pid); n != 1 {
		t.Fatalf("orders = %d", n)
	}
	if n := e.outbox("order_created_passenger", pid); n != 1 {
		t.Fatalf("created notifications = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM order_status_events WHERE order_id = $1`, first.ID); n != 1 {
		t.Fatalf("events = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM fare_quotes WHERE id = $1 AND used_at IS NOT NULL`, q.ID); n != 1 {
		t.Fatal("quote not marked used")
	}

	// Another passenger cannot use this passenger's quote.
	_, stranger := e.passenger()
	e.expect(e.do(stranger, http.MethodPost, "/orders", taxiBody(q, 800)), http.StatusNotFound, "not_found")

	// An expired quote.
	pid2, pc2 := e.passenger()
	old := e.quote(pid2, "taxi", embiStops[:2])
	if _, err := e.pool.Exec(context.Background(), `UPDATE fare_quotes SET expires_at = now() - interval '1 second' WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	r = e.do(pc2, http.MethodPost, "/orders", taxiBody(old, 800))
	e.expect(r, http.StatusUnprocessableEntity, "quote_expired")
	if _, ok := r.err(t).Error.Details["reason"]; ok {
		t.Fatalf("expired quote reported as used: %s", r.Body)
	}
}

// TestConcurrentIdempotentCreate: simultaneous submissions of the same request
// create one order; every other caller gets it back.
func TestConcurrentIdempotentCreate(t *testing.T) {
	e := newEnv(t)
	pid, pc := e.passenger()
	q := e.quote(pid, "taxi", embiStops[:2])
	body := taxiBody(q, 800)

	const n = 10
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]response, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = e.do(pc, http.MethodPost, "/orders", body)
		}(i)
	}
	close(start)
	wg.Wait()

	created := 0
	var id uuid.UUID
	for _, r := range results {
		if r.Status != http.StatusCreated && r.Status != http.StatusOK {
			t.Fatalf("create: %d %s", r.Status, r.Body)
		}
		if r.Status == http.StatusCreated {
			created++
		}
		var o orders.Order
		r.decode(t, &o)
		if id == uuid.Nil {
			id = o.ID
		} else if o.ID != id {
			t.Fatalf("different orders %s and %s", id, o.ID)
		}
	}
	if created != 1 {
		t.Fatalf("201 responses = %d", created)
	}
	if got := e.count(`SELECT count(*) FROM orders WHERE passenger_id = $1`, pid); got != 1 {
		t.Fatalf("orders = %d", got)
	}
}

// kazakhText returns NFC Kazakh text of exactly n code points.
func kazakhText(n int) string {
	const base = "Қайырлы күн! Кіреберіс алдында күтемін, үйдің нөмірі он екі, ақ қақпа. "
	r := []rune(strings.Repeat(base, n/utf8.RuneCountInString(base)+1))[:n]
	if r[n-1] == ' ' {
		r[n-1] = 'ә'
	}
	return string(r)
}

// TestCreateCommentLength: 120 code points after NFC are accepted, 121 are
// not, counted the way the Mini Apps count them.
func TestCreateCommentLength(t *testing.T) {
	e := newEnv(t)
	pid, pc := e.passenger()
	q := e.quote(pid, "taxi", embiStops[:2])

	long := kazakhText(121)
	body := taxiBody(q, 800)
	body["comment"] = long
	r := e.do(pc, http.MethodPost, "/orders", body)
	e.expect(r, http.StatusUnprocessableEntity, "validation_failed")
	if r.err(t).Error.Fields["comment"] != "too_long" {
		t.Fatalf("fields: %s", r.Body)
	}

	exact := kazakhText(120)
	if utf8.RuneCountInString(exact) != 120 {
		t.Fatalf("fixture has %d code points", utf8.RuneCountInString(exact))
	}
	// "й" decomposed (и + U+0306) is longer before NFC and exactly 120 after.
	decomposed := strings.ReplaceAll(exact, "й", "й")
	if utf8.RuneCountInString(decomposed) <= 120 {
		t.Fatal("decomposed fixture is not longer than 120 code points")
	}
	body = taxiBody(q, 800)
	body["comment"] = decomposed
	r = e.do(pc, http.MethodPost, "/orders", body)
	e.expect(r, http.StatusCreated, "")
	var o orders.Order
	r.decode(t, &o)
	if o.Comment == nil || *o.Comment != exact {
		t.Fatalf("comment stored as %v", o.Comment)
	}
}

// TestCreateCategoryRules covers the per-category required and forbidden
// fields, prohibited content, fares and payment methods.
func TestCreateCategoryRules(t *testing.T) {
	e := newEnv(t)
	pid, pc := e.passenger()
	parcel := e.quote(pid, "parcel", embiStops[:2])
	ack := map[string]any{"prohibited_items_ack": true, "cargo_description": "Құжаттар салынған конверт"}

	fieldErr := func(body map[string]any, field, reason string) {
		t.Helper()
		r := e.do(pc, http.MethodPost, "/orders", body)
		e.expect(r, http.StatusUnprocessableEntity, "validation_failed")
		if got := r.err(t).Error.Fields[field]; got != reason {
			t.Fatalf("fields.%s = %q, want %q: %s", field, got, reason, r.Body)
		}
	}

	// Parcel without a recipient phone, with an invalid one, without the
	// acknowledgement or the description.
	fieldErr(deliveryBody(parcel, 800, ack), "recipient_phone", "required")
	fieldErr(deliveryBody(parcel, 800, map[string]any{"prohibited_items_ack": true,
		"cargo_description": "Кітаптар", "recipient_phone": "+7 495 123 45 67"}), "recipient_phone", "invalid")
	fieldErr(deliveryBody(parcel, 800, map[string]any{"cargo_description": "Кітаптар",
		"recipient_phone": "8 701 123 45 67"}), "prohibited_items_ack", "must_be_true")
	fieldErr(deliveryBody(parcel, 800, map[string]any{"prohibited_items_ack": true,
		"recipient_phone": "8 701 123 45 67"}), "cargo_description", "required")
	// Blocked keywords, whatever the case, also inside inflected words.
	fieldErr(deliveryBody(parcel, 800, map[string]any{"prohibited_items_ack": true,
		"cargo_description": "ЕСІРТКІ бар қорап", "recipient_phone": "8 701 123 45 67"}), "cargo_description", "prohibited_content")
	fieldErr(deliveryBody(parcel, 800, map[string]any{"prohibited_items_ack": true,
		"cargo_description": "Кітаптар", "comment": "ішінде оружием", "recipient_phone": "8 701 123 45 67"}), "comment", "prohibited_content")

	// Fares off the grid or outside the range, and unknown payment methods.
	for _, fare := range []int{825, 450, 2000} {
		r := e.do(pc, http.MethodPost, "/orders", deliveryBody(parcel, fare, map[string]any{
			"prohibited_items_ack": true, "cargo_description": "Кітаптар", "recipient_phone": "8 701 123 45 67"}))
		e.expect(r, http.StatusUnprocessableEntity, "fare_not_allowed")
		if d := r.err(t).Error.Details; d["min"] != float64(500) || d["max"] != float64(1500) || d["step"] != float64(50) {
			t.Fatalf("fare details: %s", r.Body)
		}
	}
	bad := deliveryBody(parcel, 800, nil)
	bad["payment_method"] = "card"
	fieldErr(bad, "payment_method", "invalid")
	unknown := deliveryBody(parcel, 800, nil)
	unknown["telegram_id"] = 1
	e.expect(e.do(pc, http.MethodPost, "/orders", unknown), http.StatusBadRequest, "bad_request")

	// A valid parcel order: the phone is normalised and the quote consumed.
	r := e.do(pc, http.MethodPost, "/orders", deliveryBody(parcel, 900, map[string]any{
		"prohibited_items_ack": true, "cargo_description": "  Ойыншықтар\nжәне кітаптар ", "recipient_phone": "8 701 123 45 67"}))
	e.expect(r, http.StatusCreated, "")
	var o orders.Order
	r.decode(t, &o)
	if o.Category != "parcel" || o.ServiceType != "delivery" || o.CargoType != "parcel" || o.RecipientPhone == nil ||
		*o.RecipientPhone != "+77011234567" || o.CargoDescription == nil || *o.CargoDescription != "Ойыншықтар және кітаптар" ||
		o.PaymentMethod != "bank_transfer" {
		t.Fatalf("parcel order: %+v", o)
	}

	// A second active delivery is refused; a ride is still allowed.
	other := e.quote(pid, "other", embiStops[:2])
	r = e.do(pc, http.MethodPost, "/orders", deliveryBody(other, 700, map[string]any{
		"prohibited_items_ack": true, "cargo_description": "Шағын сөмке"}))
	e.expect(r, http.StatusConflict, "conflict")
	if d := r.err(t).Error.Details; d["reason"] != "active_order_exists" || d["active_order_id"] != o.ID.String() {
		t.Fatalf("details: %s", r.Body)
	}

	taxi := e.quote(pid, "taxi", embiStops[:2])
	fieldErr(deliveryBody(taxi, 800, map[string]any{"cargo_description": "Кітаптар"}), "cargo_description", "not_allowed")
	fieldErr(deliveryBody(taxi, 800, map[string]any{"recipient_phone": "8 701 123 45 67"}), "recipient_phone", "not_allowed")
	fieldErr(deliveryBody(taxi, 800, map[string]any{"prohibited_items_ack": true}), "prohibited_items_ack", "not_allowed")
	// Taxi comments are not screened against the delivery keywords
	// (a landmark such as a drugstore is a normal pickup hint).
	r = e.do(pc, http.MethodPost, "/orders", deliveryBody(taxi, 800, map[string]any{"comment": "Drugstore жанында күтемін"}))
	e.expect(r, http.StatusCreated, "")

	// "other" needs a description and the acknowledgement, never a recipient phone.
	pid3, pc3 := e.passenger()
	oq := e.quote(pid3, "other", embiStops[:2])
	r = e.do(pc3, http.MethodPost, "/orders", deliveryBody(oq, 700, map[string]any{
		"prohibited_items_ack": true, "cargo_description": "Шағын сөмке", "recipient_phone": "8 701 123 45 67"}))
	e.expect(r, http.StatusUnprocessableEntity, "validation_failed")
	if r.err(t).Error.Fields["recipient_phone"] != "not_allowed" {
		t.Fatalf("fields: %s", r.Body)
	}
	r = e.do(pc3, http.MethodPost, "/orders", deliveryBody(oq, 700, map[string]any{
		"prohibited_items_ack": true, "cargo_description": "Шағын сөмке"}))
	e.expect(r, http.StatusCreated, "")
	r.decode(t, &o)
	if o.CargoType != "other" || o.RecipientPhone != nil {
		t.Fatalf("other order: %+v", o)
	}
}

// TestDeliveryContactHiddenUntilAccept: drivers see the recipient phone,
// passenger contact and address details only once they hold the order.
func TestDeliveryContactHiddenUntilAccept(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	pid, pc := e.passenger()
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET phone_e164 = '+77029876543' WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	q := e.quote(pid, "parcel", embiStops[:2])
	r := e.do(pc, http.MethodPost, "/orders", deliveryBody(q, 800, map[string]any{
		"prohibited_items_ack": true, "cargo_description": "Құжаттар", "recipient_phone": "+77011234567"}))
	e.expect(r, http.StatusCreated, "")
	var o orders.Order
	r.decode(t, &o)

	r = e.do(drv.c, http.MethodGet, "/driver/orders", nil)
	e.expect(r, http.StatusOK, "")
	for _, leak := range []string{"+77011234567", "+77029876543", "2-кіреберіс"} {
		if strings.Contains(string(r.Body), leak) {
			t.Fatalf("available list leaks %q: %s", leak, r.Body)
		}
	}
	r = e.do(drv.c, http.MethodGet, path("/driver/orders/%s", o.ID), nil)
	e.expect(r, http.StatusOK, "")
	for _, leak := range []string{"+77011234567", "+77029876543", "2-кіреберіс"} {
		if strings.Contains(string(r.Body), leak) {
			t.Fatalf("available detail leaks %q: %s", leak, r.Body)
		}
	}

	r = e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil)
	e.expect(r, http.StatusOK, "")
	var dd orders.DriverOrderDetail
	r.decode(t, &dd)
	// The passenger never shared the number through Telegram: the driver gets
	// the Telegram link only (§22.5).
	if dd.RecipientPhone == nil || *dd.RecipientPhone != "+77011234567" || dd.Passenger == nil ||
		dd.Passenger.Phone != nil || dd.Passenger.PhoneVerified || dd.Stops[0].AddressDetails == nil ||
		dd.Passenger.TelegramUsername == nil || dd.Passenger.TelegramURL != "https://t.me/"+*dd.Passenger.TelegramUsername ||
		dd.CanCancel {
		t.Fatalf("accepted detail: %+v", dd)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET phone_verified_at = now(), username = NULL WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	r = e.do(drv.c, http.MethodGet, path("/driver/orders/%s", o.ID), nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &dd)
	var tg int64
	_ = e.pool.QueryRow(context.Background(), `SELECT telegram_user_id FROM users WHERE id = $1`, pid).Scan(&tg)
	if dd.Passenger == nil || dd.Passenger.Phone == nil || *dd.Passenger.Phone != "+77029876543" || !dd.Passenger.PhoneVerified ||
		dd.Passenger.TelegramUsername != nil || dd.Passenger.TelegramURL != "tg://user?id="+strconv.FormatInt(tg, 10) {
		t.Fatalf("verified passenger: %+v", dd.Passenger)
	}
	// The commission is never part of the passenger view.
	r = e.do(pc, http.MethodGet, path("/orders/%s", o.ID), nil)
	if strings.Contains(string(r.Body), "commission") {
		t.Fatalf("passenger view carries the commission: %s", r.Body)
	}
}

// TestConcurrentSecondRide: simultaneous rides of one passenger with different
// quotes and keys: one is created, the others see the active order.
func TestConcurrentSecondRide(t *testing.T) {
	e := newEnv(t)
	pid, pc := e.passenger()
	const n = 5
	bodies := make([]map[string]any, n)
	for i := range bodies {
		bodies[i] = taxiBody(e.quote(pid, "taxi", embiStops[:2]), 800)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]response, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = e.do(pc, http.MethodPost, "/orders", bodies[i])
		}(i)
	}
	close(start)
	wg.Wait()
	var created orders.Order
	for _, r := range results {
		if r.Status == http.StatusCreated {
			if created.ID != uuid.Nil {
				t.Fatal("two rides created")
			}
			r.decode(t, &created)
		}
	}
	if created.ID == uuid.Nil {
		t.Fatal("no ride created")
	}
	for _, r := range results {
		if r.Status == http.StatusCreated {
			continue
		}
		e.expect(r, http.StatusConflict, "conflict")
		if d := r.err(t).Error.Details; d["reason"] != "active_order_exists" || d["active_order_id"] != created.ID.String() {
			t.Fatalf("details: %s", r.Body)
		}
	}
	if got := e.count(`SELECT count(*) FROM orders WHERE passenger_id = $1`, pid); got != 1 {
		t.Fatalf("orders = %d", got)
	}
}
