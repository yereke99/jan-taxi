package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

var referenceRe = regexp.MustCompile(`^[A-Z]{3}[0-9]{3,}-TOPUP-[0-9]{6,}$`)

func TestTopUpLifecycle(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	_, fin := e.admin(auth.RoleFinanceManager)
	ctx := context.Background()

	// Wallet starts empty.
	w := do(t, d.h, http.MethodGet, "/driver/wallet", nil)
	expect(t, w, http.StatusOK)
	if w.body["balance"].(float64) != 0 || w.body["available"].(float64) != 0 || w.body["currency"] != "KZT" ||
		w.body["min_online_balance"].(float64) != 50 {
		t.Fatalf("wallet = %v", w.body)
	}

	id, body := createTopUp(t, d, 2000)
	ref := body["reference"].(string)
	if !referenceRe.MatchString(ref) || !strings.HasPrefix(ref, d.Code+"-TOPUP-") {
		t.Fatalf("reference %q", ref)
	}
	pay := body["payment"].(map[string]any)
	if body["status"] != StatusCreated || body["amount"].(float64) != 2000 || pay["url"] != testPaymentURL ||
		pay["reference"] != ref || pay["amount"].(float64) != 2000 || pay["method"] != ProviderKaspiManual ||
		!strings.Contains(pay["instructions"].(string), ref) || body["can_submit"] != true || body["can_cancel"] != true {
		t.Fatalf("top-up = %v", body)
	}
	var snapshot string
	if err := e.pool.QueryRow(ctx, `SELECT payment_url FROM top_up_requests WHERE id = $1`, id).Scan(&snapshot); err != nil || snapshot != testPaymentURL {
		t.Fatalf("payment_url snapshot %q, %v", snapshot, err)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM notification_outbox WHERE template = 'topup_created' AND dedupe_key = $1 AND params->>'reference' = $2`,
		"topup_created:"+id.String(), ref); n != 1 {
		t.Fatalf("topup_created notifications = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM top_up_events WHERE topup_id = $1 AND to_status = 'created' AND actor_type = 'driver'`, id); n != 1 {
		t.Fatalf("creation events = %d", n)
	}

	// Receipt and "I paid" are evidence only: the balance does not move.
	rr := serve(t, d.h, multipartRequest(t, "/driver/wallet/topups/"+id.String()+"/receipt", "file", pngImage(t)))
	expect(t, rr, http.StatusOK)
	if rr.body["has_receipt"] != true {
		t.Fatalf("receipt not attached: %v", rr.body)
	}
	listener := listen(t, e)
	s := submit(t, d, id, map[string]any{"payment_reference": " 4815 162342 ", "note": "10:05-те Kaspi Gold картасынан төледім"})
	expect(t, s, http.StatusOK)
	if s.body["status"] != StatusPendingVerification || s.body["driver_reported_paid_at"] == nil ||
		s.body["driver_payment_reference"] != "4815 162342" || s.body["can_submit"] != false {
		t.Fatalf("submitted = %v", s.body)
	}
	listener.expect(t, realtime.TopUpUpdated, realtime.AdminScope)
	if b, _ := balance(t, e.pool, d.DriverID); b != 0 {
		t.Fatalf("balance after submit = %d", b)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id WHERE w.driver_id = $1`, d.DriverID); n != 0 {
		t.Fatalf("ledger rows before confirmation = %d", n)
	}
	// A second "I paid" is idempotent.
	expect(t, submit(t, d, id, nil), http.StatusOK)

	// Finance sees it in the verification queue with history.
	l := do(t, fin, http.MethodGet, "/topups?status=pending_verification", nil)
	expect(t, l, http.StatusOK)
	if l.body["total"].(float64) != 1 {
		t.Fatalf("queue = %v", l.body)
	}
	det := do(t, fin, http.MethodGet, "/topups/"+id.String(), nil)
	expect(t, det, http.StatusOK)
	if det.body["receipt_url"] != "/api/v1/admin/topups/"+id.String()+"/receipt" || len(det.body["events"].([]any)) != 3 ||
		det.body["payment_transaction"] != nil || det.body["driver"].(map[string]any)["code"] != d.Code {
		t.Fatalf("detail = %v", det.body)
	}
	img := do(t, fin, http.MethodGet, "/topups/"+id.String()+"/receipt", nil)
	expect(t, img, http.StatusOK)
	if img.hdr.Get("Content-Type") != "image/jpeg" || img.hdr.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("receipt headers = %v", img.hdr)
	}

	// Confirmation credits exactly the verified amount, once.
	c := confirm(t, fin, id, 1500, "4815162342")
	expect(t, c, http.StatusOK)
	if c.body["status"] != StatusConfirmed || c.body["verified_amount"].(float64) != 1500 ||
		c.body["provider_transaction_id"] != "4815162342" || c.body["confirmed_by"] == nil ||
		c.body["payment_transaction"].(map[string]any)["source"] != "manual_admin" {
		t.Fatalf("confirmed = %v", c.body)
	}
	listener.expect(t, realtime.WalletUpdated, realtime.DriverScope(d.DriverID))
	listener.expect(t, realtime.TopUpConfirmed, realtime.DriverScope(d.DriverID))
	if b, _ := balance(t, e.pool, d.DriverID); b != 1500 {
		t.Fatalf("balance = %d, want 1500", b)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM wallet_ledger WHERE idempotency_key = $1 AND balance_delta = 1500`, "topup:"+id.String()); n != 1 {
		t.Fatalf("credit rows = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM notification_outbox WHERE template = 'topup_confirmed' AND params->>'balance' = '1500'`); n != 1 {
		t.Fatalf("topup_confirmed notifications = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM admin_audit_logs WHERE action = 'topup.confirm' AND entity_id = $1`, id.String()); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	// Confirming again is a state error and changes nothing.
	again := confirm(t, fin, id, 1500, "4815162342")
	expect(t, again, http.StatusConflict)
	if again.errCode() != "invalid_transition" {
		t.Fatalf("second confirm = %s", again.raw)
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 1500 {
		t.Fatalf("balance after second confirm = %d", b)
	}

	// The driver sees the result and the ledger entry.
	g := do(t, d.h, http.MethodGet, "/driver/wallet/topups/"+id.String(), nil)
	expect(t, g, http.StatusOK)
	if g.body["status"] != StatusConfirmed || g.body["verified_amount"].(float64) != 1500 || g.body["can_upload_receipt"] != false {
		t.Fatalf("driver view = %v", g.body)
	}
	tx := do(t, d.h, http.MethodGet, "/driver/wallet/transactions", nil)
	expect(t, tx, http.StatusOK)
	items := tx.body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["entry_type"] != wallets.EntryTopUpCredit ||
		items[0].(map[string]any)["topup_reference"] != ref || items[0].(map[string]any)["currency"] != "KZT" {
		t.Fatalf("transactions = %v", tx.body)
	}
	if mism, err := wallets.Reconcile(ctx, e.pool); err != nil || len(mism) != 0 {
		t.Fatalf("reconcile = %v, %v", mism, err)
	}
}

func TestCreateTopUpValidation(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	key := uuid.NewString()
	cases := []struct {
		name  string
		body  any
		code  int
		field string
		want  string
	}{
		{"too small", map[string]any{"amount": 499, "idempotency_key": key}, 422, "amount", "too_small"},
		{"too large", map[string]any{"amount": 100001, "idempotency_key": key}, 422, "amount", "too_large"},
		{"missing amount", map[string]any{"idempotency_key": key}, 422, "amount", "required"},
		{"missing key", map[string]any{"amount": 1000}, 422, "idempotency_key", "required"},
		{"bad key", map[string]any{"amount": 1000, "idempotency_key": "abc"}, 422, "idempotency_key", "invalid"},
		{"fraction", `{"amount": 1000.5, "idempotency_key": "` + key + `"}`, 400, "", ""},
		{"unknown field", map[string]any{"amount": 1000, "idempotency_key": key, "telegram_id": 1}, 400, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := do(t, d.h, http.MethodPost, "/driver/wallet/topups", tc.body)
			expect(t, r, tc.code)
			if tc.field != "" && r.errField(tc.field) != tc.want {
				t.Fatalf("fields = %s", r.raw)
			}
		})
	}

	// Idempotent replay returns the same top-up with 200; a different amount is a conflict.
	first := do(t, d.h, http.MethodPost, "/driver/wallet/topups", map[string]any{"amount": 1000, "idempotency_key": key})
	expect(t, first, http.StatusCreated)
	replay := do(t, d.h, http.MethodPost, "/driver/wallet/topups", map[string]any{"amount": 1000, "idempotency_key": key})
	expect(t, replay, http.StatusOK)
	if replay.body["id"] != first.body["id"] || replay.body["reference"] != first.body["reference"] {
		t.Fatalf("replay %v vs %v", replay.body, first.body)
	}
	other := do(t, d.h, http.MethodPost, "/driver/wallet/topups", map[string]any{"amount": 2000, "idempotency_key": key})
	expect(t, other, http.StatusConflict)
	if n := count(t, e.pool, `SELECT count(*) FROM top_up_requests WHERE driver_id = $1`, d.DriverID); n != 1 {
		t.Fatalf("top-ups = %d", n)
	}

	// At most MaxOpenTopUps open top-ups.
	for i := 1; i < MaxOpenTopUps; i++ {
		createTopUp(t, d, 1000)
	}
	capped := do(t, d.h, http.MethodPost, "/driver/wallet/topups", map[string]any{"amount": 1000, "idempotency_key": uuid.NewString()})
	expect(t, capped, http.StatusConflict)
	if capped.errDetails()["reason"] != "too_many_open_topups" {
		t.Fatalf("cap = %s", capped.raw)
	}

	// Status rules: drafts are not registered; rejected applicants cannot pay in.
	draft := e.driver("draft")
	r := do(t, draft.h, http.MethodPost, "/driver/wallet/topups", map[string]any{"amount": 1000, "idempotency_key": uuid.NewString()})
	expect(t, r, http.StatusForbidden)
	if r.errCode() != "driver_not_registered" {
		t.Fatalf("draft = %s", r.raw)
	}
	expect(t, do(t, draft.h, http.MethodGet, "/driver/wallet", nil), http.StatusForbidden)
	rejected := e.driver("rejected")
	r = do(t, rejected.h, http.MethodPost, "/driver/wallet/topups", map[string]any{"amount": 1000, "idempotency_key": uuid.NewString()})
	expect(t, r, http.StatusForbidden)
	for _, st := range []string{"pending", "changes_requested", "suspended"} {
		dd := e.driver(st)
		createTopUp(t, dd, 1000)
	}

	// Without any Kaspi link or recipient there is nowhere to pay.
	bare := NewService(e.pool, e.svc.store, Options{})
	saved := e.svc
	e.svc = bare
	nd := e.driver("approved")
	e.svc = saved
	r = do(t, nd.h, http.MethodPost, "/driver/wallet/topups", map[string]any{"amount": 1000, "idempotency_key": uuid.NewString()})
	expect(t, r, http.StatusConflict)
	if r.errDetails()["reason"] != "payment_not_configured" {
		t.Fatalf("not configured = %s", r.raw)
	}
}

func TestDriverCannotSeeOthersTopUp(t *testing.T) {
	e := newEnv(t)
	a := e.driver("approved")
	b := e.driver("approved")
	id, _ := createTopUp(t, a, 1000)
	p := "/driver/wallet/topups/" + id.String()
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, p}, {http.MethodPost, p + "/submit"}, {http.MethodPost, p + "/cancel"},
	} {
		r := do(t, b.h, req.method, req.path, nil)
		if r.code != http.StatusNotFound || r.errCode() != "not_found" {
			t.Fatalf("%s %s = %d %s", req.method, req.path, r.code, r.raw)
		}
	}
	rr := serve(t, b.h, multipartRequest(t, p+"/receipt", "file", pngImage(t)))
	expect(t, rr, http.StatusNotFound)
	list := do(t, b.h, http.MethodGet, "/driver/wallet/topups", nil)
	expect(t, list, http.StatusOK)
	if len(list.body["items"].([]any)) != 0 {
		t.Fatalf("b sees %v", list.body)
	}
	expect(t, do(t, b.h, http.MethodGet, "/driver/wallet/topups/not-a-uuid", nil), http.StatusNotFound)
	if topUpStatus(t, e.pool, id) != StatusCreated {
		t.Fatal("other driver changed the top-up")
	}
}

func TestCancelAndListTopUps(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	var ids []uuid.UUID
	for i := 0; i < 3; i++ {
		id, _ := createTopUp(t, d, int64(1000+i*500))
		ids = append(ids, id)
	}
	c := do(t, d.h, http.MethodPost, "/driver/wallet/topups/"+ids[0].String()+"/cancel", nil)
	expect(t, c, http.StatusOK)
	if c.body["status"] != StatusExpired || c.body["can_cancel"] != false {
		t.Fatalf("cancelled = %v", c.body)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM top_up_events WHERE topup_id = $1 AND to_status = 'expired' AND note = $2 AND actor_type = 'driver'`,
		ids[0], NoteCancelledByDriver); n != 1 {
		t.Fatalf("cancel events = %d", n)
	}
	r := do(t, d.h, http.MethodPost, "/driver/wallet/topups/"+ids[0].String()+"/cancel", nil)
	expect(t, r, http.StatusConflict)
	if r.errCode() != "invalid_transition" || r.errDetails()["from"] != StatusExpired {
		t.Fatalf("second cancel = %s", r.raw)
	}
	expect(t, submit(t, d, ids[1], nil), http.StatusOK)
	expect(t, do(t, d.h, http.MethodPost, "/driver/wallet/topups/"+ids[1].String()+"/cancel", nil), http.StatusConflict)
	// Submitting an expired top-up is refused.
	expect(t, submit(t, d, ids[0], nil), http.StatusConflict)

	// Cursor pagination, newest first.
	p1 := do(t, d.h, http.MethodGet, "/driver/wallet/topups?limit=2", nil)
	expect(t, p1, http.StatusOK)
	items := p1.body["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != ids[2].String() || p1.body["next_cursor"] == nil {
		t.Fatalf("page 1 = %v", p1.body)
	}
	p2 := do(t, d.h, http.MethodGet, "/driver/wallet/topups?limit=2&before="+p1.body["next_cursor"].(string), nil)
	expect(t, p2, http.StatusOK)
	items = p2.body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != ids[0].String() || p2.body["next_cursor"] != nil {
		t.Fatalf("page 2 = %v", p2.body)
	}
	expect(t, do(t, d.h, http.MethodGet, "/driver/wallet/topups?before=x", nil), http.StatusUnprocessableEntity)
}

func TestReceiptUploadRules(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	_, fin := e.admin(auth.RoleFinanceManager)
	id, _ := createTopUp(t, d, 1000)
	p := "/driver/wallet/topups/" + id.String() + "/receipt"

	notMultipart := do(t, d.h, http.MethodPost, p, map[string]any{"file": "x"})
	expect(t, notMultipart, http.StatusUnsupportedMediaType)
	missing := serve(t, d.h, multipartRequest(t, p, "other", pngImage(t)))
	expect(t, missing, http.StatusUnprocessableEntity)
	if missing.errField("file") != "required" {
		t.Fatalf("missing = %s", missing.raw)
	}
	notImage := serve(t, d.h, multipartRequest(t, p, "file", []byte("%PDF-1.4 not an image")))
	expect(t, notImage, http.StatusUnprocessableEntity)

	first := serve(t, d.h, multipartRequest(t, p, "file", pngImage(t)))
	expect(t, first, http.StatusOK)
	second := serve(t, d.h, multipartRequest(t, p, "file", pngImage(t)))
	expect(t, second, http.StatusOK)
	if n := count(t, e.pool, `SELECT count(*) FROM top_up_events WHERE topup_id = $1 AND note = $2`, id, NoteReceiptUploaded); n != 2 {
		t.Fatalf("receipt events = %d", n)
	}
	var meta []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT meta FROM top_up_events WHERE topup_id = $1 AND note = $2 ORDER BY id DESC LIMIT 1`,
		id, NoteReceiptUploaded).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	_ = json.Unmarshal(meta, &m)
	if m["previous_receipt_file_id"] == "" || m["receipt_file_id"] == m["previous_receipt_file_id"] {
		t.Fatalf("receipt meta = %s", meta)
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 0 {
		t.Fatalf("balance after receipts = %d", b)
	}

	expect(t, confirm(t, fin, id, 1000, "R-1"), http.StatusOK)
	late := serve(t, d.h, multipartRequest(t, p, "file", pngImage(t)))
	expect(t, late, http.StatusConflict)
	if late.errCode() != "invalid_transition" {
		t.Fatalf("late receipt = %s", late.raw)
	}
	// No receipt → 404 for the admin image endpoint.
	id2, _ := createTopUp(t, d, 1000)
	expect(t, do(t, fin, http.MethodGet, "/topups/"+id2.String()+"/receipt", nil), http.StatusNotFound)
}

// eventListener receives realtime events published through pg_notify.
type eventListener struct {
	events chan map[string]any
}

func listen(t *testing.T, e *env) *eventListener {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `LISTEN `+realtime.Channel); err != nil {
		t.Fatal(err)
	}
	l := &eventListener{events: make(chan map[string]any, 64)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				return
			}
			var ev map[string]any
			if json.Unmarshal([]byte(n.Payload), &ev) == nil {
				l.events <- ev
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		conn.Release()
	})
	return l
}

func (l *eventListener) expect(t *testing.T, typ, scope string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-l.events:
			if ev["type"] != typ {
				continue
			}
			for _, s := range ev["scopes"].([]any) {
				if s == scope {
					return
				}
			}
		case <-deadline:
			t.Fatalf("no %s event for scope %s", typ, scope)
		}
	}
}
