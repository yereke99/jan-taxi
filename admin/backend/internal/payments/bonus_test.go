package payments

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// TestAdminBonus (§22.7): finance can credit a driver a bonus; it lands in
// the ledger as bonus_credit with key bonus:<idempotency_key>, is audited,
// published and announced in the driver bot once; a replay changes nothing.
func TestAdminBonus(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	_, fin := e.admin(auth.RoleFinanceManager)
	path := "/drivers/" + d.DriverID.String() + "/bonus"
	key := uuid.NewString()

	bad := do(t, fin, http.MethodPost, path, map[string]any{"amount": 0, "idempotency_key": "x"})
	expect(t, bad, http.StatusUnprocessableEntity)
	if bad.errField("amount") != "too_small" || bad.errField("idempotency_key") != "invalid" {
		t.Fatalf("validation = %s", bad.raw)
	}
	missing := do(t, fin, http.MethodPost, path, map[string]any{})
	if missing.errField("amount") != "required" || missing.errField("idempotency_key") != "required" {
		t.Fatalf("missing fields = %s", missing.raw)
	}
	big := do(t, fin, http.MethodPost, path, map[string]any{"amount": MaxBonus + 1, "idempotency_key": uuid.NewString()})
	if big.errField("amount") != "too_large" || big.errDetails()["max"].(float64) != MaxBonus {
		t.Fatalf("too large = %s", big.raw)
	}
	long := make([]rune, 201)
	for i := range long {
		long[i] = 'ә'
	}
	if r := do(t, fin, http.MethodPost, path, map[string]any{"amount": 100, "comment": string(long), "idempotency_key": uuid.NewString()}); r.errField("comment") != "too_long" {
		t.Fatalf("long comment = %s", r.raw)
	}
	expect(t, do(t, fin, http.MethodPost, "/drivers/"+uuid.NewString()+"/bonus",
		map[string]any{"amount": 100, "idempotency_key": uuid.NewString()}), http.StatusNotFound)

	events := listen(t, e)
	r := do(t, fin, http.MethodPost, path, map[string]any{"amount": 1000, "comment": "  Үздік жүргізуші  ", "idempotency_key": key})
	expect(t, r, http.StatusCreated)
	events.expect(t, "wallet.updated", "driver:"+d.DriverID.String())
	entry := r.body["entry"].(map[string]any)
	wal := r.body["wallet"].(map[string]any)
	if wal["balance"].(float64) != 1000 || wal["available"].(float64) != 1000 || entry["entry_type"] != wallets.EntryBonusCredit ||
		entry["idempotency_key"] != "bonus:"+key || entry["balance_delta"].(float64) != 1000 || entry["admin"] == nil ||
		entry["description"] != "Үздік жүргізуші" || entry["driver"].(map[string]any)["code"] != d.Code {
		t.Fatalf("bonus = %v", r.body)
	}
	// The same request again (a double click, a retry) changes nothing.
	replay := do(t, fin, http.MethodPost, path, map[string]any{"amount": 1000, "comment": "Үздік жүргізуші", "idempotency_key": "bonus:" + key})
	expect(t, replay, http.StatusOK)
	if replay.body["entry"].(map[string]any)["id"] != entry["id"] {
		t.Fatalf("replay = %v", replay.body)
	}
	expect(t, do(t, fin, http.MethodPost, path, map[string]any{"amount": 2000, "idempotency_key": key}), http.StatusConflict)
	other := e.driver("approved")
	expect(t, do(t, fin, http.MethodPost, "/drivers/"+other.DriverID.String()+"/bonus",
		map[string]any{"amount": 1000, "idempotency_key": key}), http.StatusConflict)

	if b, res := balance(t, e.pool, d.DriverID); b != 1000 || res != 0 {
		t.Fatalf("balance = %d/%d", b, res)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM wallet_ledger WHERE entry_type = 'bonus_credit'`); n != 1 {
		t.Fatalf("bonus entries = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM admin_audit_logs WHERE action = 'wallet.bonus' AND entity_id = $1
		AND after->>'amount' = '1000' AND after->>'comment' = 'Үздік жүргізуші'`, d.DriverID.String()); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'driver_bonus_granted'
		AND params->>'amount' = '1000' AND params->>'balance' = '1000' AND params->>'comment' = 'Үздік жүргізуші'`, d.UserID); n != 1 {
		t.Fatalf("bonus notifications = %d", n)
	}

	// A bonus without a comment, and the ledger filter shows bonus history.
	expect(t, do(t, fin, http.MethodPost, path, map[string]any{"amount": 500, "idempotency_key": uuid.NewString()}), http.StatusCreated)
	if n := count(t, e.pool, `SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'driver_bonus_granted'
		AND NOT params ? 'comment'`, d.UserID); n != 1 {
		t.Fatal("bonus without comment")
	}
	hist := do(t, fin, http.MethodGet, "/drivers/"+d.DriverID.String()+"/ledger?type=bonus_credit", nil)
	expect(t, hist, http.StatusOK)
	if hist.body["total"].(float64) != 2 {
		t.Fatalf("bonus history = %s", hist.raw)
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 1500 {
		t.Fatalf("balance = %d", b)
	}
	if mism, err := wallets.Reconcile(t.Context(), e.pool); err != nil || len(mism) != 0 {
		t.Fatalf("reconcile = %v, %v", mism, err)
	}

	// Operators cannot give money.
	_, op := e.admin(auth.RoleOperator)
	expect(t, do(t, op, http.MethodPost, path, map[string]any{"amount": 1000, "idempotency_key": uuid.NewString()}), http.StatusForbidden)
}
