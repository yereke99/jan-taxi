package pricing

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestTariffsPerVehicleClass(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	cityID := testutil.CityID(t, pool, "EMB")

	// Seeded defaults (§21.2): step 50, 10% commission, taxi only for passenger classes.
	for _, tt := range []struct {
		category, class string
		min, max        int
	}{
		{"taxi", "universal", 600, 1800}, {"taxi", "minivan", 1000, 3000}, {"taxi", "microbus", 2000, 6000},
		{"parcel", "cargo_van", 2000, 8000}, {"other", "truck", 5000, 20000}, {"parcel", "sedan", 500, 1500},
	} {
		tr, err := ActiveTariff(ctx, pool, cityID, tt.category, tt.class)
		if err != nil || tr.VehicleClass != tt.class || tr.MinFare != tt.min || tr.MaxFare != tt.max || tr.FareStep != 50 ||
			tr.CommissionBps != 1000 || tr.Version != 1 {
			t.Errorf("%s/%s = %+v %v", tt.category, tt.class, tr, err)
		}
	}
	for _, class := range []string{"cargo_van", "truck"} {
		if _, err := ActiveTariff(ctx, pool, cityID, CategoryTaxi, class); err != ErrNoActiveTariff {
			t.Errorf("taxi %s tariff: %v", class, err)
		}
	}
	byClass, err := ActiveTariffs(ctx, pool, cityID, CategoryTaxi)
	if err != nil || len(byClass) != 4 || byClass["minivan"].MinFare != 1000 {
		t.Fatalf("ActiveTariffs = %v %v", byClass, err)
	}

	c, _ := newAdminClient(t, pool, auth.RoleSuperAdmin)
	// A new minivan version leaves the sedan tariff alone.
	code, created := c.do(http.MethodPost, "/tariffs", fmt.Sprintf(
		`{"city_id":%q,"category":"taxi","vehicle_class":"minivan","min_fare":1200,"max_fare":3600,"fare_step":100}`, cityID))
	if code != http.StatusCreated || created["vehicle_class"] != "minivan" || created["version"] != float64(2) {
		t.Fatalf("minivan v2: %d %v", code, created)
	}
	if tr, _ := ActiveTariff(ctx, pool, cityID, CategoryTaxi, "minivan"); tr.Version != 2 || tr.MinFare != 1200 {
		t.Fatalf("active minivan = %+v", tr)
	}
	if tr, _ := ActiveTariff(ctx, pool, cityID, CategoryTaxi, "sedan"); tr.Version != 1 || !tr.IsActive {
		t.Fatalf("sedan touched = %+v", tr)
	}
	// Without vehicle_class a tariff is a sedan tariff.
	code, created = c.do(http.MethodPost, "/tariffs", fmt.Sprintf(
		`{"city_id":%q,"category":"taxi","min_fare":500,"max_fare":1500,"fare_step":50}`, cityID))
	if code != http.StatusCreated || created["vehicle_class"] != "sedan" || created["version"] != float64(2) {
		t.Fatalf("default class: %d %v", code, created)
	}
	for _, tt := range []struct{ body, reason string }{
		{`"category":"taxi","vehicle_class":"truck"`, "not_allowed"},
		{`"category":"parcel","vehicle_class":"spaceship"`, "not_found"},
		{`"category":"parcel","vehicle_class":"Big Van"`, "invalid"},
		{`"category":"parcel","vehicle_class":""`, "required"},
	} {
		code, out := c.do(http.MethodPost, "/tariffs", fmt.Sprintf(`{"city_id":%q,%s,"min_fare":500,"max_fare":1500,"fare_step":50}`, cityID, tt.body))
		if _, fields := errOf(out); code != http.StatusUnprocessableEntity || fields["vehicle_class"] != tt.reason {
			t.Errorf("%s: %d %v", tt.body, code, out)
		}
	}
	code, list := c.do(http.MethodGet, "/tariffs?city_id="+cityID.String()+"&category=taxi&vehicle_class=minivan&include_inactive=true", "")
	if items := list["items"].([]any); code != http.StatusOK || len(items) != 2 || items[0].(map[string]any)["version"] != float64(2) {
		t.Fatalf("minivan history: %d %v", code, list)
	}
	code, list = c.do(http.MethodGet, "/tariffs?city_id="+cityID.String()+"&category=parcel", "")
	var classes []string
	for _, it := range list["items"].([]any) {
		classes = append(classes, it.(map[string]any)["vehicle_class"].(string))
	}
	if code != http.StatusOK || fmt.Sprint(classes) != "[sedan universal minivan microbus cargo_van truck]" {
		t.Fatalf("parcel tariffs in class order: %d %v", code, classes)
	}
}
