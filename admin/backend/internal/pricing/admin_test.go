package pricing

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestTariffVersioning(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	c, adminID := newAdminClient(t, pool, auth.RoleSuperAdmin)
	cityID := testutil.CityID(t, pool, "EMB")
	v1, err := ActiveTariff(ctx, pool, cityID, CategoryTaxi, "sedan")
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := testutil.CreateUser(t, pool)
	oldQuote := createTestQuote(t, pool, userID)

	body := fmt.Sprintf(`{"city_id":%q,"category":"taxi","min_fare":600,"max_fare":2000,"fare_step":100,
		"base_fare":400,"per_km":100,"per_minute":10,"extra_stop_fee":150,
		"extra_fees":[{"code":"winter","name":{"kk":"Қысқы үстеме","ru":"Зимняя надбавка","en":"Winter surcharge"},"amount":100}],
		"commission_bps":1200}`, cityID)
	code, v2 := c.do(http.MethodPost, "/tariffs", body)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, v2)
	}
	createdBy, _ := v2["created_by"].(map[string]any)
	fees, _ := v2["extra_fees"].([]any)
	if v2["version"] != float64(2) || v2["is_active"] != true || v2["min_fare"] != float64(600) || v2["max_fare"] != float64(2000) ||
		v2["fare_step"] != float64(100) || v2["per_km"] != float64(100) || v2["commission_bps"] != float64(1200) ||
		v2["category"] != "taxi" || v2["city_id"] != cityID.String() || createdBy["id"] != adminID.String() ||
		createdBy["full_name"] != "Тариф Әкімші" || len(fees) != 1 {
		t.Fatalf("v2 = %v", v2)
	}
	fee := fees[0].(map[string]any)
	if fee["code"] != "winter" || fee["amount"] != float64(100) || fee["name"].(map[string]any)["kk"] != "Қысқы үстеме" {
		t.Fatalf("fee = %v", fee)
	}

	// v1 is deactivated, v2 is the active one, other categories untouched.
	old, err := GetTariff(ctx, pool, v1.ID)
	if err != nil || old.IsActive {
		t.Fatalf("v1 after publish: %v %+v", err, old)
	}
	active, err := ActiveTariff(ctx, pool, cityID, CategoryTaxi, "sedan")
	if err != nil || active.ID.String() != v2["id"] || active.Version != 2 {
		t.Fatalf("active = %v %+v", err, active)
	}
	if p, err := ActiveTariff(ctx, pool, cityID, CategoryParcel, "sedan"); err != nil || p.Version != 1 {
		t.Fatalf("parcel = %v %+v", err, p)
	}
	// 400 + ceil(100*2.5)=250 + ceil(10*400/60)=67 + 150 + 100 = 967 -> 1000
	if f := Compute(active, 2500, 400, 3); f.Min != 1000 || f.Max != 2000 {
		t.Fatalf("priced with v2: %+v", f)
	}

	// A quote priced before keeps its tariff version and fares.
	q, err := load(t, pool, oldQuote.ID, userID)
	if err != nil || q.TariffID != v1.ID || q.MinFare != 500 || q.CommissionBps != 1000 {
		t.Fatalf("old quote = %v %+v", err, q)
	}

	var auditBefore, auditAfter []byte
	if err := pool.QueryRow(ctx, `SELECT before, after FROM admin_audit_logs WHERE action = 'tariff.create' AND entity_id = $1`,
		v2["id"]).Scan(&auditBefore, &auditAfter); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if len(auditBefore) == 0 || len(auditAfter) == 0 {
		t.Fatalf("audit before/after empty: %s / %s", auditBefore, auditAfter)
	}

	// Lists.
	code, list := c.do(http.MethodGet, "/tariffs?city_id="+cityID.String()+"&vehicle_class=sedan", "")
	if items := list["items"].([]any); code != http.StatusOK || len(items) != 3 {
		t.Fatalf("active list: %d %v", code, list)
	}
	code, list = c.do(http.MethodGet, "/tariffs?city_id="+cityID.String()+"&include_inactive=true&vehicle_class=sedan", "")
	if items := list["items"].([]any); code != http.StatusOK || len(items) != 4 {
		t.Fatalf("full list: %d %v", code, list)
	}
	// Across cities the nationwide region (KAZ, migration 00009) has its own copy.
	code, list = c.do(http.MethodGet, "/tariffs?category=taxi&include_inactive=1&vehicle_class=sedan", "")
	if items := list["items"].([]any); code != http.StatusOK || len(items) != 3 {
		t.Fatalf("taxi versions of every city: %d %v", code, list)
	}
	code, list = c.do(http.MethodGet, "/tariffs?city_id="+cityID.String()+"&category=taxi&include_inactive=1&vehicle_class=sedan", "")
	items := list["items"].([]any)
	if code != http.StatusOK || len(items) != 2 || items[0].(map[string]any)["version"] != float64(2) ||
		items[1].(map[string]any)["version"] != float64(1) {
		t.Fatalf("taxi versions newest first: %d %v", code, list)
	}
	for _, bad := range []string{"/tariffs?category=bus", "/tariffs?include_inactive=maybe", "/tariffs?city_id=xyz", "/tariffs?vehicle_class=Bus"} {
		if code, body := c.do(http.MethodGet, bad, ""); code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %v", bad, code, body)
		}
	}

	code, got := c.do(http.MethodGet, "/tariffs/"+v1.ID.String(), "")
	if code != http.StatusOK || got["version"] != float64(1) || got["is_active"] != false || got["created_by"] != nil {
		t.Fatalf("get v1: %d %v", code, got)
	}
	if code, _ := c.do(http.MethodGet, "/tariffs/"+uuid.NewString(), ""); code != http.StatusNotFound {
		t.Fatalf("unknown tariff: %d", code)
	}
	if code, _ := c.do(http.MethodGet, "/tariffs/not-a-uuid", ""); code != http.StatusNotFound {
		t.Fatalf("malformed id: %d", code)
	}

	// commission_bps defaults to the commission.default_bps setting.
	code, v3 := c.do(http.MethodPost, "/tariffs", fmt.Sprintf(`{"city_id":%q,"category":"taxi","min_fare":500,"max_fare":1500,"fare_step":50}`, cityID))
	if code != http.StatusCreated || v3["version"] != float64(3) || v3["commission_bps"] != float64(1000) || v3["base_fare"] != float64(0) {
		t.Fatalf("v3: %d %v", code, v3)
	}
}

func TestTariffValidation(t *testing.T) {
	pool := testutil.NewDB(t)
	c, _ := newAdminClient(t, pool, auth.RoleSuperAdmin)
	city := testutil.CityID(t, pool, "EMB").String()
	base := func(extra string) string {
		return fmt.Sprintf(`{"city_id":%q,"category":"parcel",%s}`, city, extra)
	}
	name := `"name":{"kk":"Түнгі","ru":"Ночной","en":"Night"}`
	tests := []struct {
		name   string
		body   string
		fields map[string]string
	}{
		{"missing everything", `{}`, map[string]string{"city_id": "required", "category": "required", "min_fare": "required", "max_fare": "required", "fare_step": "required"}},
		{"bad ids", `{"city_id":"x","category":"bus","min_fare":500,"max_fare":1500,"fare_step":50}`, map[string]string{"city_id": "invalid", "category": "invalid"}},
		{"not on step", base(`"min_fare":520,"max_fare":1530,"fare_step":50`), map[string]string{"min_fare": "invalid", "max_fare": "invalid"}},
		{"max below min", base(`"min_fare":1500,"max_fare":500,"fare_step":50`), map[string]string{"max_fare": "too_small"}},
		{"ranges", base(`"min_fare":0,"max_fare":20000000,"fare_step":0,"base_fare":-1,"per_km":-1,"per_minute":-1,"extra_stop_fee":-1,"commission_bps":5001`),
			map[string]string{"min_fare": "too_small", "max_fare": "too_large", "fare_step": "too_small", "base_fare": "too_small",
				"per_km": "too_small", "per_minute": "too_small", "extra_stop_fee": "too_small", "commission_bps": "too_large"}},
		{"negative commission", base(`"min_fare":500,"max_fare":1500,"fare_step":50,"commission_bps":-1`), map[string]string{"commission_bps": "too_small"}},
		{"too many options", base(`"min_fare":1,"max_fare":100000,"fare_step":1`), map[string]string{"fare_step": "too_small"}},
		{"fees", base(`"min_fare":500,"max_fare":1500,"fare_step":50,"extra_fees":[
			{"code":"Night!",` + name + `,"amount":100},
			{"code":"ok",` + name + `,"amount":0},
			{"code":"ok",` + name + `,"amount":100},
			{"code":"x","name":{"kk":"","ru":"Р","en":"E"},"amount":100},
			{"amount":100}]`),
			map[string]string{"extra_fees.0.code": "invalid", "extra_fees.1.amount": "too_small", "extra_fees.2.code": "duplicate",
				"extra_fees.3.name.kk": "required", "extra_fees.4.code": "required", "extra_fees.4.name": "required"}},
		{"too many fees", base(`"min_fare":500,"max_fare":1500,"fare_step":50,"extra_fees":[` + repeatFee(11) + `]`), map[string]string{"extra_fees": "too_many"}},
	}
	for _, tt := range tests {
		code, body := c.do(http.MethodPost, "/tariffs", tt.body)
		ec, fields := errOf(body)
		if code != http.StatusUnprocessableEntity || ec != "validation_failed" {
			t.Fatalf("%s: %d %v", tt.name, code, body)
		}
		for k, v := range tt.fields {
			if fields[k] != v {
				t.Errorf("%s: fields[%s] = %v, want %s (all %v)", tt.name, k, fields[k], v, fields)
			}
		}
	}
	if code, body := c.do(http.MethodPost, "/tariffs", `{"city_id":"`+city+`","unknown":1}`); code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d %v", code, body)
	}
	unknownCity := fmt.Sprintf(`{"city_id":%q,"category":"taxi","min_fare":500,"max_fare":1500,"fare_step":50}`, uuid.New())
	if code, body := c.do(http.MethodPost, "/tariffs", unknownCity); code != http.StatusNotFound {
		t.Fatalf("unknown city: %d %v", code, body)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM tariffs WHERE vehicle_class = 'sedan' AND city_id = $1`,
		city).Scan(&n); err != nil || n != 3 {
		t.Fatalf("tariffs after rejected requests = %d, %v", n, err)
	}
}

func repeatFee(n int) string {
	s := ""
	for i := range n {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf(`{"code":"f%d","name":{"kk":"К","ru":"Р","en":"E"},"amount":10}`, i)
	}
	return s
}

func TestTariffPermissions(t *testing.T) {
	pool := testutil.NewDB(t)
	city := testutil.CityID(t, pool, "EMB").String()
	body := fmt.Sprintf(`{"city_id":%q,"category":"taxi","min_fare":500,"max_fare":1500,"fare_step":50,"commission_bps":1000}`, city)
	for _, role := range []auth.Role{auth.RoleOperator, auth.RoleDriverModerator, auth.RoleFinanceManager, auth.RoleAuditor} {
		c, _ := newAdminClient(t, pool, role)
		if code, _ := c.do(http.MethodGet, "/tariffs", ""); code != http.StatusOK {
			t.Errorf("%s list: %d", role, code)
		}
		if code, _ := c.do(http.MethodPost, "/tariffs", body); code != http.StatusForbidden {
			t.Errorf("%s create: %d", role, code)
		}
	}
}

func TestTariffConcurrentPublish(t *testing.T) {
	pool := testutil.NewDB(t)
	c, _ := newAdminClient(t, pool, auth.RoleSuperAdmin)
	city := testutil.CityID(t, pool, "EMB")
	body := fmt.Sprintf(`{"city_id":%q,"category":"other","min_fare":500,"max_fare":1500,"fare_step":50,"commission_bps":1000}`, city)
	const n = 6
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _ = c.do(http.MethodPost, "/tariffs", body)
		}()
	}
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusCreated {
			t.Fatalf("request %d: %d", i, code)
		}
	}
	var versions, active, maxV int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(DISTINCT version), count(*) FILTER (WHERE is_active), max(version)
		FROM tariffs WHERE city_id = $1 AND category = 'other' AND vehicle_class = 'sedan'`, city).Scan(&versions, &active, &maxV); err != nil {
		t.Fatal(err)
	}
	if versions != n+1 || active != 1 || maxV != n+1 {
		t.Fatalf("versions=%d active=%d max=%d", versions, active, maxV)
	}
}
