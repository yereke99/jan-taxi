package payments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

func TestConcurrentConfirmCreditsOnce(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	_, fin1 := e.admin(auth.RoleFinanceManager)
	_, fin2 := e.admin(auth.RoleSuperAdmin)
	id, _ := createTopUp(t, d, 3000)
	expect(t, submit(t, d, id, nil), http.StatusOK)

	const n = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = map[int]int{}
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := fin1
			if i%2 == 1 {
				h = fin2
			}
			r := confirm(t, h, id, 3000, "KASPI-CONC-1")
			mu.Lock()
			results[r.code]++
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if results[http.StatusOK] != 1 || results[http.StatusConflict] != n-1 {
		t.Fatalf("results = %v", results)
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 3000 {
		t.Fatalf("balance = %d, want 3000", b)
	}
	if c := count(t, e.pool, `SELECT count(*) FROM wallet_ledger WHERE topup_id = $1`, id); c != 1 {
		t.Fatalf("ledger rows = %d", c)
	}
	if c := count(t, e.pool, `SELECT count(*) FROM payment_transactions WHERE topup_id = $1`, id); c != 1 {
		t.Fatalf("payment rows = %d", c)
	}
}

func TestSameTransactionOnTwoTopUps(t *testing.T) {
	e := newEnv(t)
	a := e.driver("approved")
	b := e.driver("approved")
	_, fin := e.admin(auth.RoleFinanceManager)
	ida, _ := createTopUp(t, a, 1000)
	idb, _ := createTopUp(t, b, 1000)
	expect(t, submit(t, b, idb, map[string]any{"payment_reference": "kaspi 77"}), http.StatusOK)

	expect(t, confirm(t, fin, ida, 1000, "KASPI 77"), http.StatusOK)
	// Spacing and case do not make the same receipt number a different payment.
	r := confirm(t, fin, idb, 1000, " kaspi77 ")
	expect(t, r, http.StatusConflict)
	if r.errCode() != "payment_reference_used" || r.errDetails()["topup_id"] != ida.String() {
		t.Fatalf("second confirm = %s", r.raw)
	}
	if topUpStatus(t, e.pool, idb) != StatusPendingVerification {
		t.Fatal("rejected confirmation changed the top-up")
	}
	if bal, _ := balance(t, e.pool, b.DriverID); bal != 0 {
		t.Fatalf("b balance = %d", bal)
	}
	// The pending top-up quoting the used receipt is flagged for finance.
	det := do(t, fin, http.MethodGet, "/topups/"+idb.String(), nil)
	expect(t, det, http.StatusOK)
	warns := det.body["duplicate_warnings"].([]any)
	if len(warns) != 1 || warns[0].(map[string]any)["kind"] != WarnTransactionRecorded ||
		warns[0].(map[string]any)["topup_id"] != ida.String() {
		t.Fatalf("warnings = %v", det.body["duplicate_warnings"])
	}
	// The confirmed top-up shows the other claim on its transaction number.
	det = do(t, fin, http.MethodGet, "/topups/"+ida.String(), nil)
	expect(t, det, http.StatusOK)
	warns = det.body["duplicate_warnings"].([]any)
	if len(warns) != 1 || warns[0].(map[string]any)["kind"] != WarnSameDriverReference ||
		warns[0].(map[string]any)["topup_id"] != idb.String() {
		t.Fatalf("confirmed warnings = %v", det.body["duplicate_warnings"])
	}

	// Concurrent confirmations of two top-ups with one transaction: one wins.
	c := e.driver("approved")
	idc, _ := createTopUp(t, c, 1000)
	idd, _ := createTopUp(t, c, 1000)
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, id := range []uuid.UUID{idc, idd} {
		wg.Add(1)
		go func(i int, id uuid.UUID) {
			defer wg.Done()
			codes[i] = confirm(t, fin, id, 1000, "KASPI-RACE").code
		}(i, id)
	}
	wg.Wait()
	if !(codes[0] == 200 && codes[1] == 409 || codes[0] == 409 && codes[1] == 200) {
		t.Fatalf("race codes = %v", codes)
	}
	if bal, _ := balance(t, e.pool, c.DriverID); bal != 1000 {
		t.Fatalf("c balance = %d", bal)
	}
}

func TestDuplicateDriverReferences(t *testing.T) {
	e := newEnv(t)
	a := e.driver("approved")
	b := e.driver("approved")
	_, fin := e.admin(auth.RoleAuditor)
	ida, _ := createTopUp(t, a, 1000)
	idb, _ := createTopUp(t, b, 1000)
	expect(t, submit(t, a, ida, map[string]any{"payment_reference": "QR 555"}), http.StatusOK)
	expect(t, submit(t, b, idb, map[string]any{"payment_reference": "qr555"}), http.StatusOK)
	det := do(t, fin, http.MethodGet, "/topups/"+ida.String(), nil)
	expect(t, det, http.StatusOK)
	warns := det.body["duplicate_warnings"].([]any)
	if len(warns) != 1 || warns[0].(map[string]any)["kind"] != WarnSameDriverReference ||
		warns[0].(map[string]any)["topup_id"] != idb.String() || warns[0].(map[string]any)["driver_code"] != b.Code {
		t.Fatalf("warnings = %v", warns)
	}
}

func TestRejectTopUp(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	_, fin := e.admin(auth.RoleFinanceManager)
	id, _ := createTopUp(t, d, 1000)
	expect(t, submit(t, d, id, nil), http.StatusOK)

	empty := do(t, fin, http.MethodPost, "/topups/"+id.String()+"/reject", map[string]any{"reason": "  "})
	expect(t, empty, http.StatusUnprocessableEntity)
	r := do(t, fin, http.MethodPost, "/topups/"+id.String()+"/reject", map[string]any{"reason": "Kaspi-де бұл кодпен төлем табылмады"})
	expect(t, r, http.StatusOK)
	if r.body["status"] != StatusRejected || r.body["rejected_reason"] != "Kaspi-де бұл кодпен төлем табылмады" || r.body["rejected_by"] == nil {
		t.Fatalf("rejected = %v", r.body)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM notification_outbox WHERE template = 'topup_rejected' AND params->>'topup_id' = $1`, id.String()); n != 1 {
		t.Fatalf("topup_rejected notifications = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM admin_audit_logs WHERE action = 'topup.reject' AND entity_id = $1`, id.String()); n != 1 {
		t.Fatalf("audit = %d", n)
	}
	expect(t, do(t, fin, http.MethodPost, "/topups/"+id.String()+"/reject", map[string]any{"reason": "x"}), http.StatusConflict)
	expect(t, confirm(t, fin, id, 1000, "AFTER-REJECT"), http.StatusConflict)
	if b, _ := balance(t, e.pool, d.DriverID); b != 0 {
		t.Fatalf("balance = %d", b)
	}
	g := do(t, d.h, http.MethodGet, "/driver/wallet/topups/"+id.String(), nil)
	if g.body["status"] != StatusRejected || g.body["rejected_reason"] == nil {
		t.Fatalf("driver view = %v", g.body)
	}
	// A created (not yet submitted) top-up can be rejected too.
	id2, _ := createTopUp(t, d, 1000)
	expect(t, do(t, fin, http.MethodPost, "/topups/"+id2.String()+"/reject", map[string]any{"reason": "duplicate"}), http.StatusOK)
}

func TestRefundTopUp(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	_, fin := e.admin(auth.RoleFinanceManager)

	// Enough balance: refunded and debited once.
	id, _ := createTopUp(t, d, 1000)
	expect(t, confirm(t, fin, id, 1000, "REF-1"), http.StatusOK)
	expect(t, do(t, fin, http.MethodPost, "/topups/"+id.String()+"/refund", map[string]any{}), http.StatusUnprocessableEntity)
	r := do(t, fin, http.MethodPost, "/topups/"+id.String()+"/refund", map[string]any{"reason": "Driver asked for the money back"})
	expect(t, r, http.StatusOK)
	if r.body["status"] != StatusRefunded || r.body["refunded_by"] == nil {
		t.Fatalf("refunded = %v", r.body)
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 0 {
		t.Fatalf("balance after refund = %d", b)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM wallet_ledger WHERE idempotency_key = $1 AND balance_delta = -1000`, "topup_refund:"+id.String()); n != 1 {
		t.Fatalf("refund rows = %d", n)
	}
	again := do(t, fin, http.MethodPost, "/topups/"+id.String()+"/refund", map[string]any{"reason": "again"})
	expect(t, again, http.StatusConflict)
	if again.errCode() != "invalid_transition" {
		t.Fatalf("second refund = %s", again.raw)
	}

	// Money already used: 402 with details, nothing changes.
	id2, _ := createTopUp(t, d, 1000)
	expect(t, confirm(t, fin, id2, 1000, "REF-2"), http.StatusOK)
	adj := do(t, fin, http.MethodPost, "/drivers/"+d.DriverID.String()+"/wallet/adjustments",
		map[string]any{"amount": -600, "reason": "commission debt", "idempotency_key": uuid.NewString()})
	expect(t, adj, http.StatusCreated)
	short := do(t, fin, http.MethodPost, "/topups/"+id2.String()+"/refund", map[string]any{"reason": "refund"})
	expect(t, short, http.StatusPaymentRequired)
	det := short.errDetails()
	if short.errCode() != "insufficient_balance" || det["required"].(float64) != 1000 || det["available"].(float64) != 400 ||
		det["currency"] != "KZT" || det["topup_min_amount"].(float64) != 500 {
		t.Fatalf("short refund = %s", short.raw)
	}
	if topUpStatus(t, e.pool, id2) != StatusConfirmed {
		t.Fatal("failed refund changed the status")
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 400 {
		t.Fatalf("balance = %d, want 400", b)
	}
	// Refund only applies to confirmed top-ups.
	id3, _ := createTopUp(t, d, 1000)
	expect(t, do(t, fin, http.MethodPost, "/topups/"+id3.String()+"/refund", map[string]any{"reason": "x"}), http.StatusConflict)
	if mism, err := wallets.Reconcile(context.Background(), e.pool); err != nil || len(mism) != 0 {
		t.Fatalf("reconcile = %v, %v", mism, err)
	}
}

func TestExpiryJob(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	ctx := context.Background()
	old, _ := createTopUp(t, d, 1000)
	waiting, _ := createTopUp(t, d, 1000)
	fresh, _ := createTopUp(t, d, 1000)
	expect(t, submit(t, d, waiting, nil), http.StatusOK)
	if _, err := e.pool.Exec(ctx, `UPDATE top_up_requests SET expires_at = now() - interval '1 minute' WHERE id = ANY($1)`,
		[]uuid.UUID{old, waiting}); err != nil {
		t.Fatal(err)
	}
	n, err := e.svc.ExpireTopUps(ctx)
	if err != nil || n != 1 {
		t.Fatalf("expired %d, %v", n, err)
	}
	if topUpStatus(t, e.pool, old) != StatusExpired || topUpStatus(t, e.pool, waiting) != StatusPendingVerification ||
		topUpStatus(t, e.pool, fresh) != StatusCreated {
		t.Fatal("wrong top-ups expired")
	}
	if c := count(t, e.pool, `SELECT count(*) FROM top_up_events WHERE topup_id = $1 AND actor_type = 'system' AND note = $2`, old, NoteExpired); c != 1 {
		t.Fatalf("expiry events = %d", c)
	}
	if n, err := e.svc.ExpireTopUps(ctx); err != nil || n != 0 {
		t.Fatalf("second run expired %d, %v", n, err)
	}
}

func TestAdminPermissionMatrix(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	id, _ := createTopUp(t, d, 1000)
	type call struct {
		name, method, path string
		body               func() any
		perm               auth.Permission
	}
	calls := []call{
		{"list", http.MethodGet, "/topups", nil, auth.PermFinanceView},
		{"detail", http.MethodGet, "/topups/" + id.String(), nil, auth.PermFinanceView},
		{"ledger", http.MethodGet, "/ledger", nil, auth.PermFinanceView},
		{"driver ledger", http.MethodGet, "/drivers/" + d.DriverID.String() + "/ledger", nil, auth.PermFinanceView},
		{"reconciliation", http.MethodGet, "/wallets/reconciliation", nil, auth.PermFinanceView},
		{"reject", http.MethodPost, "/topups/" + uuid.NewString() + "/reject", func() any { return map[string]any{"reason": "x"} }, auth.PermFinanceManage},
		{"refund", http.MethodPost, "/topups/" + uuid.NewString() + "/refund", func() any { return map[string]any{"reason": "x"} }, auth.PermFinanceManage},
		{"adjust", http.MethodPost, "/drivers/" + uuid.NewString() + "/wallet/adjustments",
			func() any { return map[string]any{"amount": 1, "reason": "x", "idempotency_key": uuid.NewString()} }, auth.PermFinanceManage},
		{"confirm", http.MethodPost, "/topups/" + uuid.NewString() + "/confirm", func() any { return confirmBody(1000, uuid.NewString()) }, auth.PermFinanceManage},
	}
	for _, role := range auth.Roles {
		_, h := e.admin(role)
		for _, c := range calls {
			var body any
			if c.body != nil {
				body = c.body()
			}
			r := do(t, h, c.method, c.path, body)
			allowed := role.Has(c.perm)
			if allowed && r.code == http.StatusForbidden || !allowed && r.code != http.StatusForbidden {
				t.Errorf("%s %s: status %d (allowed=%v) %s", role, c.name, r.code, allowed, r.raw)
			}
		}
	}
	// The explicit case from the spec: an operator cannot confirm a payment.
	_, op := e.admin(auth.RoleOperator)
	r := confirm(t, op, id, 1000, "OP-1")
	expect(t, r, http.StatusForbidden)
	if topUpStatus(t, e.pool, id) != StatusCreated {
		t.Fatal("operator changed the top-up")
	}
}

func TestAdjustmentsAndLedger(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	_, fin := e.admin(auth.RoleFinanceManager)
	path := "/drivers/" + d.DriverID.String() + "/wallet/adjustments"
	key := uuid.NewString()

	bad := do(t, fin, http.MethodPost, path, map[string]any{"amount": 0, "reason": "", "idempotency_key": "x"})
	expect(t, bad, http.StatusUnprocessableEntity)
	if bad.errField("amount") != "invalid" || bad.errField("reason") != "required" || bad.errField("idempotency_key") != "invalid" {
		t.Fatalf("validation = %s", bad.raw)
	}
	expect(t, do(t, fin, http.MethodPost, "/drivers/"+uuid.NewString()+"/wallet/adjustments",
		map[string]any{"amount": 100, "reason": "x", "idempotency_key": uuid.NewString()}), http.StatusNotFound)

	r := do(t, fin, http.MethodPost, path, map[string]any{"amount": 700, "reason": "Бонус =1+1", "idempotency_key": key})
	expect(t, r, http.StatusCreated)
	entry := r.body["entry"].(map[string]any)
	if r.body["wallet"].(map[string]any)["balance"].(float64) != 700 || entry["entry_type"] != wallets.EntryAdjustmentCredit ||
		entry["idempotency_key"] != "adjust:"+key || entry["admin"] == nil || entry["driver"].(map[string]any)["code"] != d.Code {
		t.Fatalf("adjustment = %v", r.body)
	}
	replay := do(t, fin, http.MethodPost, path, map[string]any{"amount": 700, "reason": "Бонус =1+1", "idempotency_key": key})
	expect(t, replay, http.StatusOK)
	if replay.body["entry"].(map[string]any)["id"] != entry["id"] {
		t.Fatalf("replay = %v", replay.body)
	}
	conflict := do(t, fin, http.MethodPost, path, map[string]any{"amount": 800, "reason": "x", "idempotency_key": key})
	expect(t, conflict, http.StatusConflict)
	over := do(t, fin, http.MethodPost, path, map[string]any{"amount": -701, "reason": "debt", "idempotency_key": uuid.NewString()})
	expect(t, over, http.StatusPaymentRequired)
	if over.errDetails()["available"].(float64) != 700 {
		t.Fatalf("over = %s", over.raw)
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 700 {
		t.Fatalf("balance = %d", b)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM admin_audit_logs WHERE action = 'wallet.adjust' AND entity_id = $1`, d.DriverID.String()); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}

	// Ledger listings and filters.
	other := e.driver("approved")
	expect(t, do(t, fin, http.MethodPost, "/drivers/"+other.DriverID.String()+"/wallet/adjustments",
		map[string]any{"amount": 50, "reason": "x", "idempotency_key": uuid.NewString()}), http.StatusCreated)
	all := do(t, fin, http.MethodGet, "/ledger?page_size=1", nil)
	expect(t, all, http.StatusOK)
	if all.body["total"].(float64) != 2 || len(all.body["items"].([]any)) != 1 {
		t.Fatalf("ledger = %v", all.body)
	}
	mine := do(t, fin, http.MethodGet, "/drivers/"+d.DriverID.String()+"/ledger?type=adjustment_credit", nil)
	expect(t, mine, http.StatusOK)
	if mine.body["total"].(float64) != 1 {
		t.Fatalf("driver ledger = %v", mine.body)
	}
	byDriver := do(t, fin, http.MethodGet, "/ledger?driver_id="+other.DriverID.String()+"&from=2000-01-01&to="+time.Now().Add(time.Hour).UTC().Format(time.RFC3339), nil)
	expect(t, byDriver, http.StatusOK)
	if byDriver.body["total"].(float64) != 1 {
		t.Fatalf("filtered ledger = %v", byDriver.body)
	}
	expect(t, do(t, fin, http.MethodGet, "/ledger?type=bogus", nil), http.StatusUnprocessableEntity)
	expect(t, do(t, fin, http.MethodGet, "/ledger?from=2026-10-02&to=2026-10-01", nil), http.StatusUnprocessableEntity)
	expect(t, do(t, fin, http.MethodGet, "/drivers/"+uuid.NewString()+"/ledger", nil), http.StatusNotFound)

	rec := do(t, fin, http.MethodGet, "/wallets/reconciliation", nil)
	expect(t, rec, http.StatusOK)
	if rec.body["ok"] != true || rec.body["wallets_checked"].(float64) < 2 || len(rec.body["mismatches"].([]any)) != 0 {
		t.Fatalf("reconciliation = %v", rec.body)
	}
	// A corrupted wallet shows up as a mismatch.
	if _, err := e.pool.Exec(context.Background(), `UPDATE wallets SET balance = balance + 5 WHERE driver_id = $1`, d.DriverID); err != nil {
		t.Fatal(err)
	}
	rec = do(t, fin, http.MethodGet, "/wallets/reconciliation", nil)
	mm := rec.body["mismatches"].([]any)
	if rec.body["ok"] != false || len(mm) != 1 || mm[0].(map[string]any)["balance_diff"].(float64) != 5 ||
		mm[0].(map[string]any)["driver"].(map[string]any)["code"] != d.Code {
		t.Fatalf("reconciliation = %v", rec.body)
	}
}

func TestAdminTopUpSearch(t *testing.T) {
	e := newEnv(t)
	a := e.driver("approved")
	b := e.driver("approved")
	_, fin := e.admin(auth.RoleAuditor)
	ida, bodyA := createTopUp(t, a, 1000)
	createTopUp(t, b, 1000)
	idb2, _ := createTopUp(t, b, 2000)
	expect(t, submit(t, b, idb2, nil), http.StatusOK)

	check := func(query string, want int) {
		t.Helper()
		r := do(t, fin, http.MethodGet, "/topups?"+query, nil)
		expect(t, r, http.StatusOK)
		if int(r.body["total"].(float64)) != want {
			t.Fatalf("%s: total %v, want %d", query, r.body["total"], want)
		}
	}
	check("", 3)
	check("q="+bodyA["reference"].(string), 1)
	check("q="+a.Code, 1)
	check("q="+b.Code, 2)
	check("q="+url.QueryEscape(a.Phone), 1)
	check("status=pending_verification", 1)
	check("driver_id="+b.DriverID.String(), 2)
	check("status=created&q="+b.Code, 1)
	expect(t, do(t, fin, http.MethodGet, "/topups?status=paid", nil), http.StatusUnprocessableEntity)
	expect(t, do(t, fin, http.MethodGet, "/topups/"+uuid.NewString(), nil), http.StatusNotFound)
	_ = ida
}

// fakeProvider has verifiable callbacks for webhook tests.
type fakeProvider struct{ KaspiManual }

func (f *fakeProvider) Name() string         { return "fakepay" }
func (f *fakeProvider) WebhookEnabled() bool { return true }
func (f *fakeProvider) VerifyWebhook(r *http.Request) (*ProviderPayment, error) {
	if r.Header.Get("X-Signature") != "ok" {
		return nil, errors.New("bad signature")
	}
	raw, _ := io.ReadAll(r.Body)
	var in struct {
		ID     string `json:"id"`
		Amount int64  `json:"amount"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return &ProviderPayment{ProviderTransactionID: in.ID, Amount: in.Amount, Raw: raw}, nil
}

func TestWebhook(t *testing.T) {
	e := newEnv(t)
	pub := func(svc *Service) http.Handler {
		r := newRouter()
		svc.MountPublic(r)
		return r
	}
	h := pub(e.svc)
	for _, p := range []string{"kaspi_manual", "unknown", "BAD-NAME"} {
		r := do(t, h, http.MethodPost, "/payments/webhooks/"+p, map[string]any{"id": "1"})
		if r.code != http.StatusNotFound {
			t.Fatalf("%s: %d", p, r.code)
		}
	}
	if n := count(t, e.pool, `SELECT count(*) FROM payment_transactions`); n != 0 {
		t.Fatalf("payments recorded = %d", n)
	}

	// A provider with verified callbacks records the payment but credits nothing.
	svc := NewService(e.pool, e.svc.store, Options{KaspiPaymentURL: testPaymentURL, Log: e.svc.log},
		&fakeProvider{KaspiManual: *NewKaspiManual(e.pool, testPaymentURL, "")})
	h = pub(svc)
	unsigned := do(t, h, http.MethodPost, "/payments/webhooks/fakepay", map[string]any{"id": "W-1", "amount": 1000})
	expect(t, unsigned, http.StatusUnauthorized)
	req := func() *http.Request {
		r := newJSONRequest(http.MethodPost, "/payments/webhooks/fakepay", `{"id":"W-1","amount":1000}`)
		r.Header.Set("X-Signature", "ok")
		return r
	}
	expect(t, serve(t, h, req()), http.StatusOK)
	expect(t, serve(t, h, req()), http.StatusOK)
	if n := count(t, e.pool, `SELECT count(*) FROM payment_transactions WHERE provider = 'fakepay' AND source = 'webhook' AND topup_id IS NULL`); n != 1 {
		t.Fatalf("webhook payments = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM wallet_ledger`); n != 0 {
		t.Fatalf("webhook credited the ledger: %d rows", n)
	}

	// An admin confirmation of a fakepay top-up links the recorded payment.
	e.svc = svc
	d := e.driver("approved")
	_, fin := e.admin(auth.RoleFinanceManager)
	id, _ := createTopUp(t, d, 1000)
	if _, err := e.pool.Exec(context.Background(), `UPDATE top_up_requests SET provider = 'fakepay' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	mismatch := confirm(t, fin, id, 900, "W-1")
	expect(t, mismatch, http.StatusConflict)
	ok := confirm(t, fin, id, 1000, "w-1")
	expect(t, ok, http.StatusOK)
	if ok.body["payment_transaction"].(map[string]any)["source"] != "webhook" {
		t.Fatalf("linked = %v", ok.body)
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 1000 {
		t.Fatalf("balance = %d", b)
	}
}
