package vehicleclasses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

type adminClient struct {
	t *testing.T
	h http.Handler
}

func newAdminClient(t *testing.T, pool *pgxpool.Pool, role auth.Role) adminClient {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO admin_users (email, full_name, password_hash, role) VALUES ($1, 'Көлік Әкімші', 'x', $2) RETURNING id`,
		uuid.NewString()+"@jantaxi.kz", string(role)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithAdmin(req.Context(), auth.AdminPrincipal{AdminID: id, Role: role})))
		})
	})
	NewAdminHandler(pool).MountAdmin(r)
	return adminClient{t: t, h: r}
}

func (c adminClient) do(method, path, body string) (int, map[string]any) {
	c.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			c.t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, out
}

func fieldsOf(body map[string]any) (string, map[string]any) {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	f, _ := e["fields"].(map[string]any)
	return code, f
}

func TestSeededClasses(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	list, err := List(ctx, pool, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		code                 string
		seats, max, cargo    int
		passengers, cargoCap bool
		serves               string
	}{
		{"sedan", 4, 4, 400, true, true, "[sedan]"},
		{"universal", 4, 4, 600, true, true, "[universal sedan]"},
		{"minivan", 7, 8, 800, true, true, "[minivan]"},
		{"microbus", 15, 20, 1500, true, true, "[microbus]"},
		{"cargo_van", 2, 2, 6000, false, true, "[cargo_van]"},
		{"truck", 2, 2, 15000, false, true, "[truck]"},
	}
	if len(list) != len(want) {
		t.Fatalf("classes = %+v", list)
	}
	for i, w := range want {
		c := list[i]
		if c.Code != w.code || c.SeatsDefault != w.seats || c.SeatsMax != w.max || c.CargoVolumeLDefault != w.cargo ||
			c.PassengerCapable != w.passengers || c.CargoCapable != w.cargoCap || fmt.Sprint(c.Serves) != w.serves ||
			c.Icon != w.code || !c.IsActive || c.Name.KK == "" || c.Name.RU == "" || c.Name.EN == "" {
			t.Errorf("class %d = %+v, want %+v", i, c, w)
		}
	}
	if c, _ := Get(ctx, pool, "cargo_van"); c.Name.KK != "Жүк фургоны" || c.Name.RU != "Грузовой фургон" || c.Name.EN != "Cargo van" {
		t.Errorf("cargo van names = %+v", c.Name)
	}
	if _, err := Get(ctx, pool, "bus"); err != ErrNotFound {
		t.Errorf("unknown class: %v", err)
	}
	if !list[1].ServesClass("sedan") || list[0].ServesClass("universal") {
		t.Error("ServesClass")
	}
	if !list[2].CanCarry("taxi", 8, 0) || list[2].CanCarry("taxi", 9, 0) || list[4].CanCarry("taxi", 1, 0) ||
		!list[4].CanCarry("parcel", 0, 1500) || list[0].CanCarry("other", 0, 1500) {
		t.Error("CanCarry")
	}
	for size, vol := range map[string]int{"s": 10, "m": 60, "l": 400, "xl": 1500} {
		if v, ok := CargoVolumeL(size); !ok || v != vol {
			t.Errorf("CargoVolumeL(%s) = %d %v", size, v, ok)
		}
	}
	if _, ok := CargoVolumeL("xxl"); ok {
		t.Error("CargoVolumeL(xxl)")
	}
}

func TestAdminVehicleClassCRUD(t *testing.T) {
	pool := testutil.NewDB(t)
	c := newAdminClient(t, pool, auth.RoleSuperAdmin)
	ctx := context.Background()

	code, list := c.do(http.MethodGet, "/vehicle-classes", "")
	if code != http.StatusOK || len(list["items"].([]any)) != 6 {
		t.Fatalf("list: %d %v", code, list)
	}

	// Create a 30-seat coach that also takes minibus orders.
	body := `{"code":"coach","name":{"kk":" Автобус ","ru":"Автобус","en":"Coach"},"examples":"Yutong ZK6122",
		"seats_default":30,"seats_max":50,"cargo_volume_l_default":3000,"passenger_capable":true,"cargo_capable":false,
		"serves":["microbus","coach","microbus"],"icon":"microbus","sort":45}`
	code, created := c.do(http.MethodPost, "/vehicle-classes", body)
	if code != http.StatusCreated || created["code"] != "coach" || created["name"].(map[string]any)["kk"] != "Автобус" ||
		fmt.Sprint(created["serves"]) != "[coach microbus]" || created["is_active"] != true || created["seats_max"] != float64(50) {
		t.Fatalf("create: %d %v", code, created)
	}
	if code, out := c.do(http.MethodPost, "/vehicle-classes", body); code != http.StatusConflict {
		t.Fatalf("duplicate code: %d %v", code, out)
	} else if errCode, _ := fieldsOf(out); errCode != "vehicle_class_exists" {
		t.Fatalf("duplicate code error: %v", out)
	}
	_, list = c.do(http.MethodGet, "/vehicle-classes", "")
	items := list["items"].([]any)
	if len(items) != 7 || items[4].(map[string]any)["code"] != "coach" {
		t.Fatalf("coach must sort between microbus and cargo van: %v", items)
	}

	// Update: partial, code immutable, serves always includes itself.
	code, updated := c.do(http.MethodPatch, "/vehicle-classes/minivan", `{"seats_max":9,"examples":"Kia Carnival","serves":["sedan"]}`)
	if code != http.StatusOK || updated["seats_max"] != float64(9) || updated["seats_default"] != float64(7) ||
		updated["examples"] != "Kia Carnival" || fmt.Sprint(updated["serves"]) != "[minivan sedan]" ||
		updated["name"].(map[string]any)["en"] != "Minivan" {
		t.Fatalf("update: %d %v", code, updated)
	}
	if code, out := c.do(http.MethodPatch, "/vehicle-classes/minivan", `{"is_active":false}`); code != http.StatusOK || out["is_active"] != false {
		t.Fatalf("deactivate: %d %v", code, out)
	}
	if active, _ := List(ctx, pool, true); len(active) != 6 {
		t.Fatalf("active classes after deactivation = %d", len(active))
	}
	if code, _ := c.do(http.MethodPatch, "/vehicle-classes/bus", `{"sort":1}`); code != http.StatusNotFound {
		t.Fatalf("unknown class: %d", code)
	}

	// Every write is audited with before/after.
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM admin_audit_logs WHERE entity_type = 'vehicle_class'
		  AND ((action = 'vehicle_class.create' AND entity_id = 'coach' AND before IS NULL AND after IS NOT NULL)
		    OR (action = 'vehicle_class.update' AND entity_id = 'minivan' AND before IS NOT NULL AND after IS NOT NULL))`).
		Scan(&n); err != nil || n != 3 {
		t.Fatalf("audit rows = %d %v", n, err)
	}
}

func TestAdminVehicleClassValidation(t *testing.T) {
	pool := testutil.NewDB(t)
	c := newAdminClient(t, pool, auth.RoleSuperAdmin)
	valid := map[string]any{"code": "pickup", "name": map[string]string{"kk": "Пикап", "ru": "Пикап", "en": "Pickup"},
		"seats_default": 2, "seats_max": 5, "cargo_volume_l_default": 1000, "passenger_capable": true,
		"cargo_capable": true, "icon": "truck"}
	// with returns the valid body with overrides; a nil value removes the key.
	with := func(overrides map[string]any) string {
		m := map[string]any{}
		for key, val := range valid {
			m[key] = val
		}
		for key, val := range overrides {
			if val == nil {
				delete(m, key)
			} else {
				m[key] = val
			}
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	type o = map[string]any
	for _, tt := range []struct {
		name  string
		over  o
		field string
		want  string
	}{
		{"bad code", o{"code": "Pick-Up"}, "code", "invalid"},
		{"missing code", o{"code": nil}, "code", "required"},
		{"missing name", o{"name": nil}, "name", "required"},
		{"blank kk name", o{"name": map[string]string{"kk": " ", "ru": "Пикап", "en": "Pickup"}}, "name.kk", "required"},
		{"seats over max", o{"seats_default": 6}, "seats_default", "too_large"},
		{"zero seats", o{"seats_max": 0}, "seats_max", "too_small"},
		{"huge cargo", o{"cargo_volume_l_default": 200000}, "cargo_volume_l_default", "too_large"},
		{"bad icon", o{"icon": "rocket"}, "icon", "invalid"},
		{"unknown served class", o{"serves": []string{"bus"}}, "serves.0", "not_found"},
		{"sort out of range", o{"sort": 5000}, "sort", "out_of_range"},
		{"carries nothing", o{"passenger_capable": false, "cargo_capable": false}, "cargo_capable", "one_required"},
	} {
		code, out := c.do(http.MethodPost, "/vehicle-classes", with(tt.over))
		if errCode, fields := fieldsOf(out); code != http.StatusUnprocessableEntity || errCode != "validation_failed" || fields[tt.field] != tt.want {
			t.Errorf("%s: %d %v", tt.name, code, out)
		}
	}
	if code, out := c.do(http.MethodPatch, "/vehicle-classes/sedan", `{"code":"car"}`); code != 422 {
		t.Fatalf("rename: %d %v", code, out)
	} else if _, f := fieldsOf(out); f["code"] != "immutable" {
		t.Fatalf("rename fields: %v", out)
	}
	if code, out := c.do(http.MethodPatch, "/vehicle-classes/sedan", `{"seats_default":5}`); code != 422 {
		t.Fatalf("seats over the stored max: %d %v", code, out)
	}
	if code, _ := c.do(http.MethodPost, "/vehicle-classes", `{"code":"x1","unknown":true}`); code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", code)
	}
	if code, out := c.do(http.MethodPost, "/vehicle-classes", with(nil)); code != http.StatusCreated {
		t.Fatalf("valid create: %d %v", code, out)
	}
}

func TestAdminVehicleClassPermissions(t *testing.T) {
	pool := testutil.NewDB(t)
	for _, role := range []auth.Role{auth.RoleOperator, auth.RoleDriverModerator, auth.RoleFinanceManager, auth.RoleAuditor} {
		c := newAdminClient(t, pool, role)
		for _, req := range [][2]string{
			{http.MethodGet, "/vehicle-classes"},
			{http.MethodPost, "/vehicle-classes"},
			{http.MethodPatch, "/vehicle-classes/sedan"},
		} {
			if code, _ := c.do(req[0], req[1], `{}`); code != http.StatusForbidden {
				t.Errorf("%s %s %s: %d", role, req[0], req[1], code)
			}
		}
	}
}
