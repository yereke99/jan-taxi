package commissionareas

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
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

type adminClient struct {
	t *testing.T
	h http.Handler
}

func newAdminClient(t *testing.T, pool *pgxpool.Pool, role auth.Role, g Geocoder) adminClient {
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
	NewAdminHandler(pool, g).MountAdmin(r)
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

// fakeGeocoder answers the map search from fixed lists.
type fakeGeocoder struct {
	places      []Place
	settlements []Settlement
}

func (f fakeGeocoder) SearchPlaces(context.Context, string, string, int) ([]Place, error) {
	return f.places, nil
}

func (f fakeGeocoder) SettlementsNear(context.Context, geo.Point, float64, int) ([]Settlement, error) {
	return f.settlements, nil
}

// Villages near Embi: Ауыл-1 at 48.83 N, Ауыл-2 about 3.3 km north of it.
const (
	village1 = `{"name":"Ауыл-1","commission_bps":500,"shape":"circle","center":{"lat":48.83,"lon":58.15},"radius_m":2000}`
	village2 = `{"name":"Ауыл-2","commission_bps":1000,"shape":"circle","center":{"lat":48.86,"lon":58.15},"radius_m":3000}`
	boundary = `{"name":"Шекара","commission_bps":700,"shape":"polygon","center":{"lat":48.95,"lon":58.35},
		"boundary":{"type":"Polygon","coordinates":[[[58.3,48.9],[58.4,48.9],[58.4,49.0],[58.3,49.0],[58.3,48.9]]]}}`
)

func TestAdminCommissionAreas(t *testing.T) {
	pool := testutil.NewDB(t)
	c := newAdminClient(t, pool, auth.RoleSuperAdmin, fakeGeocoder{})
	ctx := context.Background()

	status, a1 := c.do(http.MethodPost, "/commission-areas", village1)
	if status != http.StatusCreated || a1["name"] != "Ауыл-1" || a1["commission_bps"] != 500.0 || a1["shape"] != "circle" ||
		a1["radius_m"] != 2000.0 || a1["boundary"] != nil || a1["is_active"] != true {
		t.Fatalf("create circle: %d %v", status, a1)
	}
	if status, _ := c.do(http.MethodPost, "/commission-areas", village2); status != http.StatusCreated {
		t.Fatalf("create second: %d", status)
	}
	status, poly := c.do(http.MethodPost, "/commission-areas", boundary)
	if status != http.StatusCreated || poly["radius_m"] != nil || poly["boundary"] == nil {
		t.Fatalf("create polygon: %d %v", status, poly)
	}
	if auditCount(t, pool, "commission_area.create") != 3 {
		t.Fatal("create not audited")
	}

	for _, tc := range []struct {
		body  string
		field string
		want  string
	}{
		{`{}`, "name", "required"},
		{`{"name":"x","shape":"circle","center":{"lat":48.8,"lon":58.1},"radius_m":1000}`, "commission_bps", "required"},
		{`{"name":"x","commission_bps":5001,"shape":"circle","center":{"lat":48.8,"lon":58.1},"radius_m":1000}`, "commission_bps", "out_of_range"},
		{`{"name":"x","commission_bps":500,"shape":"square","center":{"lat":48.8,"lon":58.1}}`, "shape", "invalid"},
		{`{"name":"x","commission_bps":500,"shape":"circle","center":{"lat":55.75,"lon":37.6},"radius_m":1000}`, "center", "outside_service_area"},
		{`{"name":"x","commission_bps":500,"shape":"circle","center":{"lat":48.8,"lon":58.1}}`, "radius_m", "required"},
		{`{"name":"x","commission_bps":500,"shape":"circle","center":{"lat":48.8,"lon":58.1},"radius_m":100}`, "radius_m", "out_of_range"},
		{`{"name":"x","commission_bps":500,"shape":"polygon","center":{"lat":48.8,"lon":58.1}}`, "boundary", "required"},
		{`{"name":"ауыл-1","commission_bps":500,"shape":"circle","center":{"lat":48.8,"lon":58.1},"radius_m":1000}`, "name", "duplicate"},
	} {
		status, body := c.do(http.MethodPost, "/commission-areas", tc.body)
		if status != http.StatusUnprocessableEntity || fieldsOf(body)[tc.field] != tc.want {
			t.Errorf("%s: %d %v, want %s=%s", tc.body, status, body, tc.field, tc.want)
		}
	}

	// Which village a point belongs to: inside both circles the nearer
	// centre wins; outside every area there is none.
	for _, tc := range []struct {
		lat, lon float64
		want     any
	}{
		{48.840, 58.15, "Ауыл-1"}, // 1.1 km from Ауыл-1, 2.2 km from Ауыл-2: both cover it
		{48.855, 58.15, "Ауыл-2"}, // outside Ауыл-1's 2 km
		{48.950, 58.35, "Шекара"}, // inside the drawn boundary
		{48.500, 58.15, nil},      // nowhere
	} {
		ref, _, err := Resolve(ctx, pool, geo.Point{Lat: tc.lat, Lon: tc.lon})
		if err != nil {
			t.Fatal(err)
		}
		var got any
		if ref != nil {
			got = ref.Name
		}
		if got != tc.want {
			t.Errorf("resolve %.3f,%.3f = %v, want %v", tc.lat, tc.lon, got, tc.want)
		}
	}
	status, res := c.do(http.MethodGet, "/commission-areas/resolve?lat=48.84&lon=58.15", "")
	area, _ := res["area"].(map[string]any)
	if status != http.StatusOK || area["name"] != "Ауыл-1" || area["commission_bps"] != 500.0 || res["distance_m"].(float64) < 1000 {
		t.Fatalf("resolve endpoint: %d %v", status, res)
	}
	if status, res := c.do(http.MethodGet, "/commission-areas/resolve?lat=48.5&lon=58.15", ""); status != http.StatusOK || res["area"] != nil || res["distance_m"] != nil {
		t.Fatalf("resolve outside: %d %v", status, res)
	}
	if status, res := c.do(http.MethodGet, "/commission-areas/resolve?lat=x&lon=58.15", ""); status != http.StatusUnprocessableEntity || fieldsOf(res)["lat"] != "invalid" {
		t.Fatalf("resolve bad lat: %d %v", status, res)
	}

	// A deactivated village no longer applies; patching keeps other fields.
	id := a1["id"].(string)
	status, upd := c.do(http.MethodPatch, "/commission-areas/"+id, `{"is_active":false,"commission_bps":750}`)
	if status != http.StatusOK || upd["is_active"] != false || upd["commission_bps"] != 750.0 || upd["radius_m"] != 2000.0 || upd["name"] != "Ауыл-1" {
		t.Fatalf("patch: %d %v", status, upd)
	}
	if ref, _, _ := Resolve(ctx, pool, geo.Point{Lat: 48.84, Lon: 58.15}); ref == nil || ref.Name != "Ауыл-2" {
		t.Fatalf("inactive area still applies: %+v", ref)
	}
	// Switching to a boundary needs the boundary.
	if status, body := c.do(http.MethodPatch, "/commission-areas/"+id, `{"shape":"polygon"}`); status != http.StatusUnprocessableEntity || fieldsOf(body)["boundary"] != "required" {
		t.Fatalf("shape switch without boundary: %d %v", status, body)
	}
	if auditCount(t, pool, "commission_area.update") != 1 {
		t.Fatal("update not audited")
	}

	status, list := c.do(http.MethodGet, "/commission-areas", "")
	items, _ := list["items"].([]any)
	if status != http.StatusOK || len(items) != 3 || items[0].(map[string]any)["name"] != "Ауыл-1" {
		t.Fatalf("list: %d %v", status, list)
	}

	if status, _ := c.do(http.MethodDelete, "/commission-areas/"+id, ""); status != http.StatusNoContent {
		t.Fatalf("delete: %d", status)
	}
	if status, _ := c.do(http.MethodDelete, "/commission-areas/"+id, ""); status != http.StatusNotFound {
		t.Fatalf("delete again: %d", status)
	}
	if auditCount(t, pool, "commission_area.delete") != 1 {
		t.Fatal("delete not audited")
	}
}

// The proposal combines the map data with the villages already configured.
func TestAdminSuggestAndSearch(t *testing.T) {
	pool := testutil.NewDB(t)
	g := fakeGeocoder{
		places: []Place{{Title: "Шұбарқұдық", Subtitle: "Темір ауданы", Lat: 49.1449, Lon: 56.4893}},
		settlements: []Settlement{
			{Name: "Ауыл", Kind: "village", Point: geo.Point{Lat: 48.83, Lon: 58.15}},
		},
	}
	c := newAdminClient(t, pool, auth.RoleSuperAdmin, g)
	if status, _ := c.do(http.MethodPost, "/commission-areas", village2); status != http.StatusCreated {
		t.Fatal("create")
	}

	// Ауыл is a village mapped as a point (2 km typical reach); the
	// configured Ауыл-2 is 3.3 km away, so the circle stops at 1.7 km.
	status, s := c.do(http.MethodGet, "/commission-areas/suggest?lat=48.83&lon=58.15", "")
	nb, _ := s["neighbor"].(map[string]any)
	st, _ := s["settlement"].(map[string]any)
	if status != http.StatusOK || s["basis"] != BasisType || st["name"] != "Ауыл" || st["kind"] != "village" ||
		nb["name"] != "Ауыл-2" || nb["source"] != "area" || s["radius_m"] != 1700.0 {
		t.Fatalf("suggest: %d %v", status, s)
	}

	status, found := c.do(http.MethodGet, "/commission-areas/search?q=Шұбар", "")
	items, _ := found["items"].([]any)
	if status != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["title"] != "Шұбарқұдық" {
		t.Fatalf("search: %d %v", status, found)
	}
	if status, body := c.do(http.MethodGet, "/commission-areas/search?q=Ш", ""); status != http.StatusUnprocessableEntity || fieldsOf(body)["q"] != "too_short" {
		t.Fatalf("short search: %d %v", status, body)
	}
}

// Commission areas are configuration: an operator without config.manage
// cannot read or change them.
func TestAdminCommissionAreasNeedConfigPermission(t *testing.T) {
	pool := testutil.NewDB(t)
	c := newAdminClient(t, pool, auth.RoleOperator, nil)
	if status, _ := c.do(http.MethodGet, "/commission-areas", ""); status != http.StatusForbidden {
		t.Fatalf("list as operator: %d", status)
	}
	if status, _ := c.do(http.MethodPost, "/commission-areas", village1); status != http.StatusForbidden {
		t.Fatalf("create as operator: %d", status)
	}
}

// A village being drawn is checked against the database: a taken name, the
// villages that already hold its point, the villages it overlaps and the
// nearest one; the village being edited is never its own duplicate.
func TestAdminCheckVillage(t *testing.T) {
	pool := testutil.NewDB(t)
	c := newAdminClient(t, pool, auth.RoleSuperAdmin, nil)
	_, a1 := c.do(http.MethodPost, "/commission-areas", village1)
	c.do(http.MethodPost, "/commission-areas", village2)
	c.do(http.MethodPost, "/commission-areas", boundary)
	id1 := a1["id"].(string)

	names := func(v any) []string {
		var out []string
		list, _ := v.([]any)
		for _, x := range list {
			out = append(out, x.(map[string]any)["name"].(string))
		}
		return out
	}
	check := func(body string) map[string]any {
		t.Helper()
		status, res := c.do(http.MethodPost, "/commission-areas/check", body)
		if status != http.StatusOK {
			t.Fatalf("check %s: %d %v", body, status, res)
		}
		return res
	}

	// The name is taken whatever its case; the edited village does not count.
	res := check(`{"name":"  ауыл-1 "}`)
	if taken, _ := res["name_taken"].(map[string]any); taken == nil || taken["name"] != "Ауыл-1" || taken["is_active"] != true {
		t.Fatalf("name taken: %v", res)
	}
	if res := check(`{"name":"Ауыл-1","exclude_id":"` + id1 + `"}`); res["name_taken"] != nil {
		t.Fatalf("own name: %v", res)
	}

	// A point inside both circles: both hold it, nearest first.
	res = check(`{"center":{"lat":48.84,"lon":58.15}}`)
	if got := names(res["covering"]); len(got) != 2 || got[0] != "Ауыл-1" || got[1] != "Ауыл-2" {
		t.Fatalf("covering: %v", res)
	}
	if n, _ := res["nearest"].(map[string]any); n == nil || n["name"] != "Ауыл-1" || n["distance_m"].(float64) < 1000 {
		t.Fatalf("nearest: %v", res)
	}

	// A 2 km circle 3.3 km south of Ауыл-1 overlaps it without holding its point.
	res = check(`{"shape":"circle","center":{"lat":48.80,"lon":58.15},"radius_m":2000}`)
	if got := names(res["overlapping"]); len(got) != 1 || got[0] != "Ауыл-1" || len(names(res["covering"])) != 0 {
		t.Fatalf("circle overlap: %v", res)
	}
	// A drawn boundary next to Шекара overlaps it.
	res = check(`{"shape":"polygon","boundary":{"type":"Polygon","coordinates":[[[58.38,48.95],[58.45,48.95],[58.45,49.05],[58.38,49.05],[58.38,48.95]]]}}`)
	if got := names(res["overlapping"]); len(got) != 1 || got[0] != "Шекара" {
		t.Fatalf("boundary overlap: %v", res)
	}

	// A free place: nothing taken, nothing overlapped, the nearest village named.
	res = check(`{"name":"Жаңа ауыл","shape":"circle","center":{"lat":48.5,"lon":58.15},"radius_m":1000}`)
	if res["name_taken"] != nil || len(names(res["covering"])) != 0 || len(names(res["overlapping"])) != 0 {
		t.Fatalf("free place: %v", res)
	}
	if n, _ := res["nearest"].(map[string]any); n == nil || n["name"] != "Ауыл-1" || n["distance_m"].(float64) < 30000 {
		t.Fatalf("free place nearest: %v", res)
	}

	for _, tc := range []struct{ body, field, want string }{
		{`{"shape":"circle","center":{"lat":48.5,"lon":58.15},"radius_m":100}`, "radius_m", "out_of_range"},
		{`{"shape":"square"}`, "shape", "invalid"},
		{`{"center":{"lat":95,"lon":58.15}}`, "center", "invalid"},
	} {
		if status, body := c.do(http.MethodPost, "/commission-areas/check", tc.body); status != http.StatusUnprocessableEntity || fieldsOf(body)[tc.field] != tc.want {
			t.Errorf("%s: %d %v", tc.body, status, body)
		}
	}
}
