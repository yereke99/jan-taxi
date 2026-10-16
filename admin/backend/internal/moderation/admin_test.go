package moderation_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func listCodes(t *testing.T, c *driverstest.Client, query string) []string {
	t.Helper()
	res := c.Do(http.MethodGet, "/api/v1/admin/drivers?"+query, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("list %q: %s", query, res)
	}
	var codes []string
	for _, it := range res.Body["items"].([]any) {
		codes = append(codes, it.(map[string]any)["code"].(string))
	}
	return codes
}

func TestAdminListFiltersAndSearch(t *testing.T) {
	env := driverstest.New(t)
	a := env.DriverClient()
	aID := a.CompleteDraft(driverstest.Registration{FirstName: "Нұрлан", LastName: "Әбенов", Plate: "456KMH04", Phone: "+77019876543"})
	if res := a.Submit(); res.Code != http.StatusOK {
		t.Fatalf("submit: %s", res)
	}
	approved := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true, Balance: 500})
	var aCode string
	if err := env.Pool.QueryRow(context.Background(), `SELECT code FROM driver_profiles WHERE id = $1`, aID).Scan(&aCode); err != nil {
		t.Fatal(err)
	}

	viewer := env.AdminClient(auth.RoleAuditor)
	cases := []struct {
		query string
		want  []string
	}{
		{"status=pending", []string{aCode}},
		{"status=approved&online=true", []string{approved.Code}},
		{"q=" + url.QueryEscape(aCode), []string{aCode}},
		{"q=" + url.QueryEscape("8 (701) 987-65-43"), []string{aCode}},
		{"q=9876543", []string{aCode}},
		{"q=" + url.QueryEscape("456 КМН 04"), []string{aCode}},
		{"q=" + url.QueryEscape("әбенов"), []string{aCode}},
		{"q=" + url.QueryEscape(approved.Plate), []string{approved.Code}},
	}
	for _, tt := range cases {
		got := listCodes(t, viewer, tt.query)
		if len(got) != len(tt.want) || (len(got) > 0 && got[0] != tt.want[0]) {
			t.Errorf("list %q = %v, want %v", tt.query, got, tt.want)
		}
	}
	res := viewer.Do(http.MethodGet, "/api/v1/admin/drivers?status=pending", nil)
	item := res.Body["items"].([]any)[0].(map[string]any)
	if res.Body["total"] != float64(1) || item["phone_verified"] != true || item["vehicle"].(map[string]any)["plate"] != "456 KMH 04" ||
		item["city"].(map[string]any)["code"] != "EMB" || item["wallet_available"] != float64(0) {
		t.Fatalf("list item: %v", res.Body)
	}
	for _, bad := range []string{"status=unknown", "city_id=nope", "online=maybe", "page=0"} {
		if res := viewer.Do(http.MethodGet, "/api/v1/admin/drivers?"+bad, nil); res.Code != http.StatusUnprocessableEntity {
			t.Errorf("list %q: %s", bad, res)
		}
	}
}

func TestAdminDetailDocumentsNeedPermission(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	id := c.CompleteDraft(driverstest.Registration{Plate: "111ABC04", Phone: "+77001234567"})

	res := env.AdminClient(auth.RoleOperator).Do(http.MethodGet, adminPath(id, ""), nil)
	if res.Code != http.StatusOK || res.Body["documents"] != nil || res.Body["avatar"] == nil {
		t.Fatalf("operator detail: %s", res)
	}
	res = env.AdminClient(auth.RoleDriverModerator).Do(http.MethodGet, adminPath(id, ""), nil)
	docs, _ := res.Body["documents"].(map[string]any)
	front, _ := docs["id_front"].(map[string]any)
	if res.Code != http.StatusOK || front == nil || front["url"] != "/api/v1/admin/files/"+front["id"].(string) {
		t.Fatalf("moderator detail: %s", res)
	}
	user := res.Body["user"].(map[string]any)
	vehicle := res.Body["vehicle"].(map[string]any)
	if user["telegram_user_id"] == nil || vehicle["plate_kind"] != "current" || res.Body["stats"] == nil {
		t.Fatalf("detail fields: %s", res)
	}
	if res := env.AdminClient(auth.RoleAuditor).Do(http.MethodGet, adminPath(uuid.New(), ""), nil); res.Code != http.StatusNotFound {
		t.Fatalf("unknown driver: %s", res)
	}
}

func TestSuspendRestoreAndCity(t *testing.T) {
	env := driverstest.New(t)
	d := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true, Balance: 1000})
	op := env.AdminClient(auth.RoleOperator)

	if res := env.AdminClient(auth.RoleFinanceManager).Do(http.MethodPost, adminPath(d.DriverID, "suspend"), `{"reason":"x"}`); res.Code != http.StatusForbidden {
		t.Fatalf("finance suspend: %s", res)
	}
	// Not while the driver is on an order.
	passenger, _ := testutil.CreateUser(t, env.Pool)
	order := env.CreateOrder(passenger, d.DriverID, "in_progress")
	res := op.Do(http.MethodPost, adminPath(d.DriverID, "suspend"), `{"reason":"Жолаушылар шағымы"}`)
	if res.Code != http.StatusConflict || res.Details()["reason"] != "active_order_exists" || res.Details()["active_order_id"] != order.String() {
		t.Fatalf("suspend with active order: %s", res)
	}
	if _, err := env.Pool.Exec(context.Background(), `UPDATE orders SET status = 'completed', completed_at = now() WHERE id = $1`, order); err != nil {
		t.Fatal(err)
	}
	res = op.Do(http.MethodPost, adminPath(d.DriverID, "suspend"), `{"reason":"Жолаушылар шағымы"}`)
	if res.Code != http.StatusOK || res.Body["status"] != "suspended" || res.Body["is_online"] != false || res.Body["suspended_reason"] != "Жолаушылар шағымы" {
		t.Fatalf("suspend: %s", res)
	}
	if res.Body["stats"].(map[string]any)["completed_trips"] != float64(1) {
		t.Fatalf("stats: %v", res.Body["stats"])
	}
	driver := env.AppClient(d.UserID, auth.AppDriver)
	if res := driver.Do(http.MethodPatch, "/api/v1/driver/availability", `{"online":true}`); res.Code != http.StatusForbidden ||
		res.ErrorCode() != "driver_not_approved" || res.Details()["reason"] != "suspended" {
		t.Fatalf("suspended driver goes online: %s", res)
	}
	if n := env.Count(`SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'driver_suspended'`, d.UserID); n != 1 {
		t.Fatal("driver_suspended not queued")
	}
	if res := op.Do(http.MethodPost, adminPath(d.DriverID, "suspend"), `{"reason":"again"}`); res.Code != http.StatusConflict {
		t.Fatalf("suspend twice: %s", res)
	}
	res = op.Do(http.MethodPost, adminPath(d.DriverID, "restore"), nil)
	if res.Code != http.StatusOK || res.Body["status"] != "approved" || res.Body["is_online"] != false || res.Body["suspended_reason"] != nil {
		t.Fatalf("restore: %s", res)
	}
	if res := driver.Do(http.MethodPatch, "/api/v1/driver/availability", `{"online":true}`); res.Code != http.StatusOK || res.Body["is_online"] != true {
		t.Fatalf("restored driver goes online: %s", res)
	}

	// City change.
	var otherCity uuid.UUID
	if err := env.Pool.QueryRow(context.Background(), `
		INSERT INTO cities (code, name_kk, name_ru, name_en, center_lat, center_lon) VALUES ('AKX', 'Ақтөбе', 'Актобе', 'Aktobe', 50.2839, 57.167)
		RETURNING id`).Scan(&otherCity); err != nil {
		t.Fatal(err)
	}
	if res := op.Do(http.MethodPatch, adminPath(d.DriverID, ""), `{"city_id":"`+uuid.NewString()+`"}`); res.Code != 422 || res.Fields()["city_id"] != "invalid" {
		t.Fatalf("unknown city: %s", res)
	}
	res = op.Do(http.MethodPatch, adminPath(d.DriverID, ""), `{"city_id":"`+otherCity.String()+`"}`)
	if res.Code != http.StatusOK || res.Body["city"].(map[string]any)["code"] != "AKX" {
		t.Fatalf("change city: %s", res)
	}
	events := res.Body["moderation_events"].([]any)
	if events[0].(map[string]any)["action"] != "city_changed" || events[0].(map[string]any)["comment"] != "EMB → AKX" {
		t.Fatalf("city event: %v", events[0])
	}
	for _, action := range []string{"driver.suspend", "driver.restore", "driver.city_change"} {
		if n := env.Count(`SELECT count(*) FROM admin_audit_logs WHERE action = $1 AND entity_id = $2`, action, d.DriverID.String()); n != 1 {
			t.Errorf("%s audited %d times", action, n)
		}
	}
}
