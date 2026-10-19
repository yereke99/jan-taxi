package moderation_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// pendingDriver registers and submits a driver with phone and plate.
func pendingDriver(t *testing.T, env *driverstest.Env, phone, plate string) (uuid.UUID, *driverstest.Client) {
	t.Helper()
	c := env.DriverClient()
	id := c.CompleteDraft(driverstest.Registration{Plate: plate, Phone: phone})
	if res := c.Submit(); res.Code != http.StatusOK {
		t.Fatalf("submit: %s", res)
	}
	return id, c
}

func balance(t *testing.T, env *driverstest.Env, driverID uuid.UUID) int {
	t.Helper()
	return env.Count(`SELECT COALESCE((SELECT balance FROM wallets WHERE driver_id = $1), 0)`, driverID)
}

// releaseClaims frees a driver's phone and plate for others, as a rejection
// or a closed account would.
func releaseClaims(t *testing.T, env *driverstest.Env, driverID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET status = 'rejected', is_online = false WHERE id = $1`, driverID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Pool.Exec(ctx, `UPDATE vehicles SET status = 'released' WHERE driver_id = $1`, driverID); err != nil {
		t.Fatal(err)
	}
}

// TestWelcomeBonusOnFirstApproval: approving a new driver credits 500 KZT once
// (bonus_credit, welcome_bonus:<driver_id>), audited and announced (§22.1).
func TestWelcomeBonusOnFirstApproval(t *testing.T) {
	env := driverstest.New(t)
	id, c := pendingDriver(t, env, "+77015550101", "101AAA04")
	if balance(t, env, id) != 0 {
		t.Fatal("money before approval")
	}
	mod := env.AdminClient(auth.RoleDriverModerator)
	res := mod.Do(http.MethodPost, adminPath(id, "approve"), nil)
	if res.Code != http.StatusOK {
		t.Fatalf("approve: %s", res)
	}
	w := res.Body["wallet"].(map[string]any)
	b, _ := res.Body["welcome_bonus"].(map[string]any)
	if w["balance"] != float64(500) || w["available"] != float64(500) || b == nil || b["amount"] != float64(500) {
		t.Fatalf("approved driver: wallet %v bonus %v", w, b)
	}
	hist, _ := res.Body["bonuses"].([]any)
	if len(hist) != 1 || hist[0].(map[string]any)["kind"] != "welcome" || hist[0].(map[string]any)["amount"] != float64(500) ||
		hist[0].(map[string]any)["comment"] != nil || hist[0].(map[string]any)["admin_id"] != mod.Admin.String() {
		t.Fatalf("bonus history %v", res.Body["bonuses"])
	}
	if n := env.Count(`SELECT count(*) FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id
		WHERE w.driver_id = $1 AND l.entry_type = 'bonus_credit' AND l.balance_delta = 500
		  AND l.idempotency_key = 'welcome_bonus:' || $1 AND l.admin_id = $2`, id, mod.Admin); n != 1 {
		t.Fatal("bonus ledger entry")
	}
	if n := env.Count(`SELECT count(*) FROM welcome_bonus_grants WHERE driver_id = $1 AND phone_e164 = '+77015550101'
		AND plate_normalized = '101AAA04' AND amount = 500`, id); n != 1 {
		t.Fatal("grant not recorded")
	}
	if n := env.Count(`SELECT count(*) FROM admin_audit_logs WHERE action = 'driver.welcome_bonus' AND entity_id = $1
		AND admin_id = $2 AND after->>'amount' = '500'`, id.String(), mod.Admin); n != 1 {
		t.Fatal("bonus not audited")
	}
	if n := env.Count(`SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'driver_welcome_bonus'
		AND params->>'amount' = '500' AND params->>'balance' = '500'`, c.UserID); n != 1 {
		t.Fatal("bonus notification")
	}

	// Suspension and restoration never grant again; neither does a later
	// approval of the same driver.
	adm := env.AdminClient(auth.RoleSuperAdmin)
	if res := adm.Do(http.MethodPost, adminPath(id, "suspend"), map[string]any{"reason": "Тексеру"}); res.Code != http.StatusOK {
		t.Fatalf("suspend: %s", res)
	}
	if res := adm.Do(http.MethodPost, adminPath(id, "restore"), nil); res.Code != http.StatusOK {
		t.Fatalf("restore: %s", res)
	}
	ctx := context.Background()
	if _, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET status = 'pending' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Pool.Exec(ctx, `INSERT INTO driver_applications (driver_id, round, status, snapshot)
		VALUES ($1, 2, 'pending', '{}')`, id); err != nil {
		t.Fatal(err)
	}
	if res := mod.Do(http.MethodPost, adminPath(id, "approve"), nil); res.Code != http.StatusOK {
		t.Fatalf("second approval: %s", res)
	}
	if got := balance(t, env, id); got != 500 {
		t.Fatalf("balance after re-approval %d", got)
	}
	if n := env.Count(`SELECT count(*) FROM wallet_ledger WHERE entry_type = 'bonus_credit'`); n != 1 {
		t.Fatalf("bonus entries = %d", n)
	}
}

// TestWelcomeBonusOncePerPhoneAndPlate: a new driver whose phone or plate
// already received the bonus gets nothing; a driver with a fresh phone and
// plate does.
func TestWelcomeBonusOncePerPhoneAndPlate(t *testing.T) {
	env := driverstest.New(t)
	mod := env.AdminClient(auth.RoleDriverModerator)
	approve := func(id uuid.UUID) {
		t.Helper()
		if res := mod.Do(http.MethodPost, adminPath(id, "approve"), nil); res.Code != http.StatusOK {
			t.Fatalf("approve: %s", res)
		}
	}
	first, _ := pendingDriver(t, env, "+77015550201", "201BBB04")
	approve(first)
	releaseClaims(t, env, first)

	samePhone, _ := pendingDriver(t, env, "+77015550201", "202BBB04")
	approve(samePhone)
	if got := balance(t, env, samePhone); got != 0 {
		t.Fatalf("bonus for a phone that had it: %d", got)
	}
	releaseClaims(t, env, samePhone)

	samePlate, c := pendingDriver(t, env, "+77015550203", "201 ВВВ 04")
	approve(samePlate)
	if got := balance(t, env, samePlate); got != 0 {
		t.Fatalf("bonus for a plate that had it: %d", got)
	}
	if n := env.Count(`SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'driver_welcome_bonus'`, c.UserID); n != 0 {
		t.Fatal("bonus message without a bonus")
	}

	fresh, _ := pendingDriver(t, env, "+77015550204", "204BBB04")
	approve(fresh)
	if got := balance(t, env, fresh); got != 500 {
		t.Fatalf("fresh driver balance %d", got)
	}
	if n := env.Count(`SELECT count(*) FROM welcome_bonus_grants`); n != 2 {
		t.Fatalf("grants = %d", n)
	}

	// The bonus follows the setting; 0 disables it.
	if err := settings.Set(context.Background(), env.Pool, "driver.welcome_bonus", int64(0), uuid.Nil); err != nil {
		t.Fatal(err)
	}
	off, _ := pendingDriver(t, env, "+77015550205", "205BBB04")
	approve(off)
	if got := balance(t, env, off); got != 0 {
		t.Fatalf("bonus while disabled: %d", got)
	}
	if err := settings.Set(context.Background(), env.Pool, "driver.welcome_bonus", int64(700), uuid.Nil); err != nil {
		t.Fatal(err)
	}
	seven, _ := pendingDriver(t, env, "+77015550206", "206BBB04")
	approve(seven)
	if got := balance(t, env, seven); got != 700 {
		t.Fatalf("bonus after a settings change: %d", got)
	}
}

// TestConcurrentApprovalsGrantOnce: many simultaneous approvals of one driver
// approve and credit once.
func TestConcurrentApprovalsGrantOnce(t *testing.T) {
	env := driverstest.New(t)
	id, _ := pendingDriver(t, env, "+77015550301", "301CCC04")
	mod := env.AdminClient(auth.RoleDriverModerator)
	const n = 10
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = mod.Do(http.MethodPost, adminPath(id, "approve"), nil).Code
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Fatalf("approve: %d", c)
		}
	}
	if ok != 1 {
		t.Fatalf("approvals = %d", ok)
	}
	if got := balance(t, env, id); got != 500 {
		t.Fatalf("balance %d", got)
	}
	if n := env.Count(`SELECT count(*) FROM wallet_ledger WHERE entry_type = 'bonus_credit'`); n != 1 {
		t.Fatalf("bonus entries = %d", n)
	}
}
