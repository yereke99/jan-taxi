package cities

import (
	"bytes"
	"context"
	"encoding/json"
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

func newAdminClient(t *testing.T, pool *pgxpool.Pool, role auth.Role) (adminClient, uuid.UUID) {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO admin_users (email, full_name, password_hash, role) VALUES ($1, 'Admin', 'x', $2) RETURNING id`,
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
	return adminClient{t: t, h: r}, id
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

func fieldsOf(body map[string]any) map[string]any {
	e, _ := body["error"].(map[string]any)
	f, _ := e["fields"].(map[string]any)
	return f
}

func auditCount(t *testing.T, pool *pgxpool.Pool, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_audit_logs WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

const aktobeCity = `{"code":"akx","name":{"kk":"Ақтөбе","ru":"Актобе","en":"Aktobe"},"center":{"lat":50.2839,"lon":57.167},
	"default_zoom":12.5,"timezone":"Asia/Aqtobe","dispatch_radius_m":30000,"require_driver_location":true,
	"driver_location_max_age_s":600,"search_timeout_s":600}`

func TestAdminCities(t *testing.T) {
	pool := testutil.NewDB(t)
	c, _ := newAdminClient(t, pool, auth.RoleSuperAdmin)

	code, city := c.do(http.MethodPost, "/cities", aktobeCity)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, city)
	}
	center := city["center"].(map[string]any)
	if city["code"] != "AKX" || city["default_zoom"] != 12.5 || center["lat"] != 50.2839 || city["require_driver_location"] != true ||
		city["dispatch_radius_m"] != float64(30000) || city["is_active"] != true {
		t.Fatalf("created = %v", city)
	}
	id := city["id"].(string)
	if auditCount(t, pool, "city.create") != 1 {
		t.Fatal("create not audited")
	}

	if code, body := c.do(http.MethodPost, "/cities", aktobeCity); code != http.StatusConflict || body["error"].(map[string]any)["code"] != "city_code_taken" {
		t.Fatalf("duplicate: %d %v", code, body)
	}

	invalid := []struct {
		name, body string
		fields     map[string]string
	}{
		{"missing everything", `{}`, map[string]string{"code": reasonRequired, "name": reasonRequired, "center": reasonInvalid}},
		{"bad code and names", `{"code":"AB1","name":{"kk":"","ru":"Р","en":""},"center":{"lat":48.8,"lon":58.1}}`,
			map[string]string{"code": reasonInvalid, "name.kk": reasonRequired, "name.en": reasonRequired}},
		{"outside kazakhstan", `{"code":"MSK","name":{"kk":"М","ru":"М","en":"M"},"center":{"lat":55.7558,"lon":37.6173}}`,
			map[string]string{"center": reasonOutside}},
		{"ranges", `{"code":"QQQ","name":{"kk":"Q","ru":"Q","en":"Q"},"center":{"lat":48.8,"lon":58.1},"default_zoom":25,
			"timezone":"Europe/Moscow","dispatch_radius_m":100,"driver_location_max_age_s":5,"search_timeout_s":10}`,
			map[string]string{"default_zoom": reasonOutOfRange, "timezone": reasonInvalid, "dispatch_radius_m": reasonOutOfRange,
				"driver_location_max_age_s": reasonOutOfRange, "search_timeout_s": reasonOutOfRange}},
	}
	for _, tt := range invalid {
		code, body := c.do(http.MethodPost, "/cities", tt.body)
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %v", tt.name, code, body)
		}
		f := fieldsOf(body)
		for k, v := range tt.fields {
			if f[k] != v {
				t.Errorf("%s: field %s = %v, want %s (all: %v)", tt.name, k, f[k], v, f)
			}
		}
	}

	code, city = c.do(http.MethodPatch, "/cities/"+id, `{"is_active":false,"search_timeout_s":1200,"code":"AKX","name":{"kk":"Ақтөбе қаласы","ru":"Актобе","en":"Aktobe"}}`)
	if code != http.StatusOK || city["is_active"] != false || city["search_timeout_s"] != float64(1200) || city["name"].(map[string]any)["kk"] != "Ақтөбе қаласы" {
		t.Fatalf("patch: %d %v", code, city)
	}
	if code, body := c.do(http.MethodPatch, "/cities/"+id, `{"code":"AKT"}`); code != 422 || fieldsOf(body)["code"] != reasonImmutable {
		t.Fatalf("code change: %d %v", code, body)
	}
	if code, body := c.do(http.MethodPatch, "/cities/"+id, `{"center":{"lat":41.3,"lon":69.2}}`); code != 422 || fieldsOf(body)["center"] != reasonOutside {
		t.Fatalf("center outside: %d %v", code, body)
	}
	if code, _ := c.do(http.MethodPatch, "/cities/"+uuid.NewString(), `{"is_active":true}`); code != http.StatusNotFound {
		t.Fatalf("unknown city: %d", code)
	}
	if code, _ := c.do(http.MethodPatch, "/cities/"+id, `{"population":1}`); code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", code)
	}
	if auditCount(t, pool, "city.update") != 1 {
		t.Fatal("update audit count")
	}

	code, list := c.do(http.MethodGet, "/cities?page_size=1", "")
	if code != http.StatusOK || list["total"] != float64(2) || len(list["items"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, list)
	}
}

func TestAdminCityPermissions(t *testing.T) {
	pool := testutil.NewDB(t)
	op, _ := newAdminClient(t, pool, auth.RoleOperator)
	if code, _ := op.do(http.MethodGet, "/cities", ""); code != http.StatusOK {
		t.Fatalf("operator list: %d", code)
	}
	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "/cities", aktobeCity},
		{http.MethodGet, "/cities/" + uuid.NewString() + "/zones", ""},
		{http.MethodDelete, "/zones/" + uuid.NewString(), ""},
		{http.MethodGet, "/places", ""},
	} {
		if code, _ := op.do(req.method, req.path, req.body); code != http.StatusForbidden {
			t.Errorf("operator %s %s: %d", req.method, req.path, code)
		}
	}
}

const (
	embiSquare = `{"type":"Polygon","coordinates":[[[58.10,48.80],[58.20,48.80],[58.20,48.86],[58.10,48.86],[58.10,48.80]]]}`
	bowtie     = `{"type":"Polygon","coordinates":[[[58.10,48.80],[58.20,48.86],[58.20,48.80],[58.10,48.86],[58.10,48.80]]]}`
	moscow     = `{"type":"Polygon","coordinates":[[[37.5,55.7],[37.7,55.7],[37.7,55.8],[37.5,55.8],[37.5,55.7]]]}`
)

func TestAdminZones(t *testing.T) {
	pool := testutil.NewDB(t)
	c, _ := newAdminClient(t, pool, auth.RoleSuperAdmin)
	cityID := testutil.CityID(t, pool, "EMB").String()

	code, list := c.do(http.MethodGet, "/cities/"+cityID+"/zones", "")
	if code != http.StatusOK || len(list["items"].([]any)) != 1 {
		t.Fatalf("seed zones: %d %v", code, list)
	}
	seed := list["items"].([]any)[0].(map[string]any)
	if seed["geometry"].(map[string]any)["type"] != "MultiPolygon" || seed["area_km2"].(float64) < 700 {
		t.Fatalf("seed zone = %v", seed["area_km2"])
	}

	code, zone := c.do(http.MethodPost, "/cities/"+cityID+"/zones", `{"name":" Орталық ","geometry":`+embiSquare+`}`)
	if code != http.StatusCreated || zone["name"] != "Орталық" || zone["is_active"] != true ||
		zone["geometry"].(map[string]any)["type"] != "MultiPolygon" {
		t.Fatalf("create zone: %d %v", code, zone)
	}
	zoneID := zone["id"].(string)

	invalid := []struct{ name, body, reason string }{
		{"self-intersecting", `{"name":"x","geometry":` + bowtie + `}`, "invalid_geometry"},
		{"outside kazakhstan", `{"name":"x","geometry":` + moscow + `}`, reasonOutside},
		{"wrong type", `{"name":"x","geometry":{"type":"LineString","coordinates":[[58.1,48.8],[58.2,48.9]]}}`, "unsupported_type"},
		{"crs member", `{"name":"x","geometry":{"type":"Polygon","crs":{"type":"name"},"coordinates":[]}}`, reasonInvalid},
		{"malformed coordinates", `{"name":"x","geometry":{"type":"Polygon","coordinates":[[[58.1]]]}}`, reasonInvalid},
		{"missing", `{"name":"x"}`, reasonRequired},
		{"out of range", `{"name":"x","geometry":{"type":"Polygon","coordinates":[[[200,48.8],[201,48.8],[201,49],[200,48.8]]]}}`, reasonOutOfRange},
	}
	for _, tt := range invalid {
		code, body := c.do(http.MethodPost, "/cities/"+cityID+"/zones", tt.body)
		if code != http.StatusUnprocessableEntity || fieldsOf(body)["geometry"] != tt.reason {
			t.Errorf("%s: %d %v", tt.name, code, body)
		}
	}
	if code, body := c.do(http.MethodPost, "/cities/"+cityID+"/zones", `{"name":"","geometry":`+embiSquare+`}`); code != 422 || fieldsOf(body)["name"] != reasonRequired {
		t.Errorf("empty name: %d %v", code, body)
	}
	if code, _ := c.do(http.MethodPost, "/cities/"+uuid.NewString()+"/zones", `{"name":"x","geometry":`+embiSquare+`}`); code != http.StatusNotFound {
		t.Errorf("unknown city: %d", code)
	}

	code, zone = c.do(http.MethodPatch, "/zones/"+zoneID, `{"is_active":false}`)
	if code != http.StatusOK || zone["is_active"] != false || zone["name"] != "Орталық" {
		t.Fatalf("patch active: %d %v", code, zone)
	}
	before := zone["area_km2"].(float64)
	code, zone = c.do(http.MethodPatch, "/zones/"+zoneID,
		`{"name":"Кіші","geometry":{"type":"Polygon","coordinates":[[[58.12,48.81],[58.16,48.81],[58.16,48.84],[58.12,48.84],[58.12,48.81]]]}}`)
	if code != http.StatusOK || zone["name"] != "Кіші" || zone["area_km2"].(float64) >= before {
		t.Fatalf("patch geometry: %d %v", code, zone)
	}
	if code, body := c.do(http.MethodPatch, "/zones/"+zoneID, `{"geometry":`+bowtie+`}`); code != 422 || fieldsOf(body)["geometry"] != "invalid_geometry" {
		t.Fatalf("patch invalid geometry: %d %v", code, body)
	}

	if code, _ := c.do(http.MethodDelete, "/zones/"+zoneID, ""); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := c.do(http.MethodDelete, "/zones/"+zoneID, ""); code != http.StatusNotFound {
		t.Fatalf("delete again: %d", code)
	}
	if code, _ := c.do(http.MethodPatch, "/zones/"+zoneID, `{"is_active":true}`); code != http.StatusNotFound {
		t.Fatalf("patch deleted: %d", code)
	}
	for action, want := range map[string]int{"zone.create": 1, "zone.update": 2, "zone.delete": 1} {
		if got := auditCount(t, pool, action); got != want {
			t.Errorf("%s audit rows = %d, want %d", action, got, want)
		}
	}
}

func TestAdminPlaces(t *testing.T) {
	pool := testutil.NewDB(t)
	c, _ := newAdminClient(t, pool, auth.RoleSuperAdmin)
	cityID := testutil.CityID(t, pool, "EMB").String()

	code, place := c.do(http.MethodPost, "/places", `{"city_id":"`+cityID+`","name":{"kk":"Ембі автовокзалы","ru":"Автовокзал Эмба","en":"Embi bus station"},
		"aliases":[" вокзал ","автовокзал"],"lat":48.8301,"lon":58.1405}`)
	if code != http.StatusCreated || place["city_id"] != cityID || place["lat"] != 48.8301 || place["is_active"] != true {
		t.Fatalf("create: %d %v", code, place)
	}
	if a := place["aliases"].([]any); len(a) != 2 || a[0] != "вокзал" {
		t.Fatalf("aliases = %v", a)
	}
	id := place["id"].(string)

	code, place = c.do(http.MethodPost, "/places", `{"name":{"kk":"Әкімдік","ru":"Акимат","en":"Akimat"},"lat":48.826,"lon":58.144}`)
	if code != http.StatusCreated || place["city_id"] != nil || len(place["aliases"].([]any)) != 0 {
		t.Fatalf("create without city: %d %v", code, place)
	}

	invalid := []struct {
		name, body string
		fields     map[string]string
	}{
		{"outside kazakhstan", `{"name":{"kk":"a","ru":"a","en":"a"},"lat":55.75,"lon":37.61}`, map[string]string{"location": reasonOutside}},
		{"missing coordinates", `{"name":{"kk":"a","ru":"a","en":"a"},"lat":48.8}`, map[string]string{"location": reasonRequired}},
		{"duplicate aliases", `{"name":{"kk":"a","ru":"a","en":"a"},"aliases":["Базар","базар"],"lat":48.8,"lon":58.1}`, map[string]string{"aliases": reasonDuplicate}},
		{"unknown city", `{"city_id":"` + uuid.NewString() + `","name":{"kk":"a","ru":"a","en":"a"},"lat":48.8,"lon":58.1}`, map[string]string{"city_id": reasonNotFound}},
		{"bad city id", `{"city_id":"x","name":{"kk":"a","ru":"a","en":"a"},"lat":48.8,"lon":58.1}`, map[string]string{"city_id": reasonInvalid}},
		{"missing name", `{"lat":48.8,"lon":58.1}`, map[string]string{"name": reasonRequired}},
	}
	for _, tt := range invalid {
		code, body := c.do(http.MethodPost, "/places", tt.body)
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %v", tt.name, code, body)
		}
		for k, v := range tt.fields {
			if fieldsOf(body)[k] != v {
				t.Errorf("%s: %s = %v, want %s", tt.name, k, fieldsOf(body)[k], v)
			}
		}
	}

	code, place = c.do(http.MethodPatch, "/places/"+id, `{"city_id":null,"aliases":[],"is_active":false,"lon":58.15}`)
	if code != http.StatusOK || place["city_id"] != nil || len(place["aliases"].([]any)) != 0 || place["is_active"] != false ||
		place["lon"] != 58.15 || place["lat"] != 48.8301 {
		t.Fatalf("patch: %d %v", code, place)
	}
	if code, body := c.do(http.MethodPatch, "/places/"+id, `{"lat":41.3}`); code != 422 || fieldsOf(body)["location"] != reasonOutside {
		t.Fatalf("patch outside: %d %v", code, body)
	}

	code, list := c.do(http.MethodGet, "/places?q=АКИМ", "")
	if code != http.StatusOK || list["total"] != float64(1) {
		t.Fatalf("list q: %d %v", code, list)
	}
	code, list = c.do(http.MethodGet, "/places?q=вокзал", "")
	if code != http.StatusOK || list["total"] != float64(1) {
		t.Fatalf("list alias q: %d %v", code, list)
	}
	code, list = c.do(http.MethodGet, "/places?city_id="+cityID, "")
	if code != http.StatusOK || list["total"] != float64(0) {
		t.Fatalf("list city: %d %v", code, list)
	}

	if code, _ := c.do(http.MethodDelete, "/places/"+id, ""); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := c.do(http.MethodDelete, "/places/"+id, ""); code != http.StatusNotFound {
		t.Fatalf("delete again: %d", code)
	}
	for action, want := range map[string]int{"place.create": 2, "place.update": 1, "place.delete": 1} {
		if got := auditCount(t, pool, action); got != want {
			t.Errorf("%s audit rows = %d, want %d", action, got, want)
		}
	}
}
