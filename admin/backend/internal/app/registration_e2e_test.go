package app_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// TestDriverSelfRegistrationToFirstOrder: a new driver verifies the phone
// through the driver bot, fills in both registration pages (the body type
// stays the preselected Sedan), uploads the profile photo and submits. The
// automatic checks approve at once with the welcome bonus; with a fresh
// location the driver goes online and takes an order. The same checks refuse
// bad and duplicate IINs, a phone or plate another driver holds, and nothing
// writes to the retired document-check tables.
func TestDriverSelfRegistrationToFirstOrder(t *testing.T) {
	e := newFlowEnv(t)
	ctx := context.Background()
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := e.pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	iin := driverstest.NewIIN()
	d := e.draftDriver("Ерлан", "+77471239901", "234 ehk 04", iin)
	reg := e.must(http.StatusOK, e.call(e.h, http.MethodGet, "/api/v1/driver/registration", nil, d.auth), "registration")
	vehicle := reg.body["vehicle"].(map[string]any)
	if reg.body["can_submit"] != true || reg.body["iin"] != iin || vehicle["vehicle_class"] != "sedan" || vehicle["seats"] != float64(4) {
		t.Fatalf("complete draft: %s", reg.raw)
	}
	sub := e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/register",
		map[string]any{"pd_consent": true, "pd_consent_version": "driver-pd-2026-10-v3"}, d.auth), "submit")
	if sub.str("status") != "approved" || sub.body["editable"] != false {
		t.Fatalf("submit: %s", sub.raw)
	}
	e.wallet(d, 500, 0)
	if n := count(`SELECT count(*) FROM welcome_bonus_grants WHERE driver_id = $1`, d.driverID); n != 1 {
		t.Fatalf("welcome bonus grants: %d", n)
	}
	me := e.must(http.StatusOK, e.call(e.h, http.MethodGet, "/api/v1/me", nil, d.auth), "me")
	if me.body["driver"].(map[string]any)["status"] != "approved" {
		t.Fatalf("me: %s", me.raw)
	}

	// Online needs a fresh location; then the driver takes an order.
	e.must(http.StatusUnprocessableEntity, e.call(e.h, http.MethodPatch, "/api/v1/driver/availability", map[string]any{"online": true}, d.auth), "online without location")
	e.must(http.StatusNoContent, e.call(e.h, http.MethodPost, "/api/v1/driver/location", map[string]any{"lat": 48.8270, "lon": 58.1450}, d.auth), "location")
	if r := e.must(http.StatusOK, e.call(e.h, http.MethodPatch, "/api/v1/driver/availability", map[string]any{"online": true}, d.auth), "online"); r.body["is_online"] != true {
		t.Fatalf("online: %s", r.raw)
	}
	p := e.passenger()
	order := e.createOrder(p, 2, 600)
	list := e.must(http.StatusOK, e.call(e.h, http.MethodGet, "/api/v1/driver/orders", nil, d.auth), "available orders")
	if !strings.Contains(string(list.raw), order) {
		t.Fatalf("order not offered: %s", list.raw)
	}
	e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/driver/orders/"+order+"/accept", nil, d.auth), "accept")
	if n := count(`SELECT count(*) FROM orders WHERE id = $1 AND driver_id = $2 AND status = 'accepted'`, order, d.driverID); n != 1 {
		t.Fatal("order not accepted by the new driver")
	}
	e.wallet(d, 450, 0) // the fixed commission is charged at acceptance (§22.2)
	if n := count(`SELECT count(*) FROM wallet_ledger WHERE entry_type = 'bonus_credit' AND idempotency_key = 'welcome_bonus:' || $1`, d.driverID); n != 1 {
		t.Fatalf("welcome bonus credited %d times", n)
	}

	// Bad IINs never reach the profile.
	x := e.draftDriver("Нұрлан", "+77471239902", "", "")
	for _, tt := range []struct{ iin, reason string }{
		{iin[:11], "invalid"}, {iin + "0", "invalid"}, {iin[:11] + fmt.Sprint((int(iin[11]-'0')+1)%10), "checksum"},
	} {
		r := e.call(e.h, http.MethodPut, "/api/v1/driver/registration", map[string]any{"iin": tt.iin}, x.auth)
		fields, _ := r.body["error"].(map[string]any)["fields"].(map[string]any)
		if r.code != http.StatusUnprocessableEntity || fields["iin"] != tt.reason {
			t.Fatalf("IIN %s: %d %s", tt.iin, r.code, r.raw)
		}
	}
	// The approved driver's IIN, phone and plate are taken.
	if r := e.call(e.h, http.MethodPut, "/api/v1/driver/registration", map[string]any{"iin": iin}, x.auth); r.code != http.StatusConflict ||
		r.body["error"].(map[string]any)["code"] != "iin_taken" {
		t.Fatalf("duplicate IIN: %d %s", r.code, r.raw)
	}
	if r := e.call(e.h, http.MethodPut, "/api/v1/driver/registration", map[string]any{"vehicle": map[string]any{"plate": "234 ЕНК 04"}}, x.auth); r.code != http.StatusConflict ||
		r.body["error"].(map[string]any)["code"] != "plate_taken" {
		t.Fatalf("duplicate plate: %d %s", r.code, r.raw)
	}
	tg := testutil.NextTelegramID()
	e.signIn("driver", config.TestDriverBotToken, tg)
	if r := e.call(e.ih, http.MethodPost, fmt.Sprintf("/internal/v1/driver/%d/contact", tg),
		map[string]any{"phone": "+77471239901", "contact_user_id": tg},
		map[string]string{"Authorization": "Bearer " + config.TestServiceSecret, auth.BotHeader: "driver"}); r.code != http.StatusConflict ||
		r.body["error"].(map[string]any)["code"] != "phone_taken" {
		t.Fatalf("duplicate verified phone: %d %s", r.code, r.raw)
	}
	// A draft that typed the IIN before the other driver was approved is
	// refused on submission.
	y := e.draftDriver("Әлия", "+77471239903", "235 ehk 04", driverstest.NewIIN())
	if _, err := e.pool.Exec(ctx, `UPDATE driver_profiles SET iin = $2 WHERE id = $1`, y.driverID, iin); err != nil {
		t.Fatal(err)
	}
	if r := e.call(e.h, http.MethodPost, "/api/v1/driver/register", map[string]any{"pd_consent": true, "pd_consent_version": "driver-pd-2026-10-v3"}, y.auth); r.code != http.StatusConflict ||
		r.body["error"].(map[string]any)["code"] != "iin_taken" {
		t.Fatalf("submit with a taken IIN: %d %s", r.code, r.raw)
	}
	if n := count(`SELECT count(*) FROM driver_profiles WHERE id = $1 AND status = 'draft'`, y.driverID); n != 1 {
		t.Fatal("refused submission changed the status")
	}

	// The retired document checks: no jobs, inputs or claims, no ID scans.
	for _, table := range []string{"driver_verification_jobs", "driver_identity_inputs", "driver_identity_claims", "driver_documents"} {
		if n := count(`SELECT count(*) FROM ` + table); n != 0 {
			t.Errorf("%s has %d rows", table, n)
		}
	}
	if n := count(`SELECT count(*) FROM files WHERE purpose <> 'driver_avatar'`); n != 0 {
		t.Errorf("%d files besides profile photos", n)
	}
}
