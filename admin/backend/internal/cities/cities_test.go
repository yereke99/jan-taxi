package cities

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

var (
	embi   = geo.Point{Lat: 48.8264, Lon: 58.1442}
	almaty = geo.Point{Lat: 43.2389, Lon: 76.8897}
)

func insertCity(t *testing.T, pool *pgxpool.Pool, code string, p geo.Point, active bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO cities (code, name_kk, name_ru, name_en, center_lat, center_lon, is_active)
		VALUES ($1, $1, $1, $1, $2, $3, $4)`, code, p.Lat, p.Lon, active); err != nil {
		t.Fatalf("insert city %s: %v", code, err)
	}
}

func TestGetters(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	c, err := GetByCode(ctx, pool, " emb ")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != (Name{KK: "Ембі", RU: "Эмба", EN: "Embi"}) || c.Center != embi || c.DefaultZoom != 14 ||
		c.Timezone != "Asia/Aqtobe" || !c.IsActive || c.DispatchRadiusM != 50000 || c.SearchTimeoutS != 900 {
		t.Fatalf("seed city = %+v", c)
	}
	if got, err := Get(ctx, pool, c.ID); err != nil || got.Code != "EMB" {
		t.Fatalf("Get = %+v %v", got, err)
	}
	if _, err := GetByCode(ctx, pool, "XXX"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	insertCity(t, pool, "ALA", almaty, true)
	insertCity(t, pool, "OFF", almaty, false)
	active, err := ListActive(ctx, pool)
	if err != nil || len(active) != 3 || active[0].Code != "ALA" || active[1].Code != "EMB" || active[2].Code != "KAZ" {
		t.Fatalf("ListActive = %v %v", active, err)
	}
	kaz, err := GetByCode(ctx, pool, DefaultRegionCode)
	if err != nil || !kaz.IsDefaultRegion || !kaz.IsActive || kaz.Name != (Name{KK: "Қазақстан", RU: "Казахстан", EN: "Kazakhstan"}) ||
		kaz.Center != (geo.Point{Lat: 51.1694, Lon: 71.4491}) {
		t.Fatalf("default region = %+v %v", kaz, err)
	}
	if c.IsDefaultRegion {
		t.Fatal("EMB marked as default region")
	}
}

func TestResolve(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	// A city 5 km from Embi without its own zone: it never wins by distance.
	near := geo.Point{Lat: 48.8264, Lon: 58.2125}
	insertCity(t, pool, "NBR", near, true)

	tests := []struct {
		name string
		p    geo.Point
		want string
	}{
		{"embi centre", embi, "EMB"},
		{"inside embi zone, nearer another centre", near, "EMB"},
		{"almaty (no city zone)", almaty, DefaultRegionCode},
		{"far west, no city zone", geo.Point{Lat: 47.10, Lon: 51.92}, DefaultRegionCode},
		{"astana", geo.Point{Lat: 51.1694, Lon: 71.4491}, DefaultRegionCode},
	}
	for _, tt := range tests {
		c, err := Resolve(ctx, pool, tt.p)
		if err != nil || c.Code != tt.want {
			t.Errorf("%s: got %s (%v), want %s", tt.name, c.Code, err, tt.want)
		}
	}

	// A configured Almaty zone wins over the nationwide one; the smallest of
	// overlapping zones decides.
	insertCity(t, pool, "ALA", almaty, true)
	if _, err := pool.Exec(ctx, `
		INSERT INTO service_zones (city_id, name, geom)
		SELECT id, 'Алматы', ST_Multi(ST_Buffer(center, 20000)::geometry) FROM cities WHERE code = 'ALA'`); err != nil {
		t.Fatal(err)
	}
	if c, err := Resolve(ctx, pool, almaty); err != nil || c.Code != "ALA" {
		t.Errorf("almaty with a city zone: %s %v", c.Code, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO service_zones (city_id, name, geom)
		SELECT id, 'Ембі ауданы', ST_Multi(ST_Buffer(center, 60000)::geometry) FROM cities WHERE code = 'NBR'`); err != nil {
		t.Fatal(err)
	}
	if c, _ := Resolve(ctx, pool, near); c.Code != "EMB" {
		t.Errorf("smaller EMB zone: got %s, want EMB", c.Code)
	}

	// Inactive zones and cities are ignored.
	if _, err := pool.Exec(ctx, `UPDATE service_zones SET is_active = false
		WHERE city_id = (SELECT id FROM cities WHERE code = 'EMB')`); err != nil {
		t.Fatal(err)
	}
	if c, _ := Resolve(ctx, pool, near); c.Code != "NBR" {
		t.Errorf("with the EMB zone inactive got %s, want NBR", c.Code)
	}
	if _, err := pool.Exec(ctx, `UPDATE cities SET is_active = false WHERE code = 'NBR'`); err != nil {
		t.Fatal(err)
	}
	if c, _ := Resolve(ctx, pool, near); c.Code != DefaultRegionCode {
		t.Errorf("with NBR inactive got %s, want KAZ", c.Code)
	}
	// Without its zone the default region is still the fallback.
	if _, err := pool.Exec(ctx, `DELETE FROM service_zones
		WHERE city_id = (SELECT id FROM cities WHERE code = 'KAZ')`); err != nil {
		t.Fatal(err)
	}
	if c, _ := Resolve(ctx, pool, geo.Point{Lat: 47.10, Lon: 51.92}); c.Code != DefaultRegionCode {
		t.Errorf("without the KAZ zone got %s, want KAZ", c.Code)
	}
	if _, err := Resolve(ctx, pool, geo.Point{Lat: 91, Lon: 0}); !errors.Is(err, ErrInvalidPoint) {
		t.Errorf("invalid point err = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE cities SET is_active = false`); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ctx, pool, embi); !errors.Is(err, ErrNoActiveCity) {
		t.Errorf("no active city err = %v", err)
	}
}

func TestDefaultRegionSeed(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	kaz, err := DefaultRegion(ctx, pool)
	if err != nil || kaz.Code != DefaultRegionCode {
		t.Fatalf("DefaultRegion = %+v %v", kaz, err)
	}
	if r := kaz.Ref(); r.Code != "KAZ" || r.Name.KK != "Қазақстан" || r.Name.RU != "Казахстан" || r.Name.EN != "Kazakhstan" {
		t.Fatalf("Ref = %+v", r)
	}
	// One zone: the Kazakhstan service area.
	zones, err := ListZones(ctx, pool, kaz.ID)
	if err != nil || len(zones) != 1 || !zones[0].IsActive || zones[0].AreaKM2 < 2_500_000 {
		t.Fatalf("zones = %d %v", len(zones), err)
	}
	// Every active Embi tariff exists for KAZ with the same prices.
	var missing, total int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE k.id IS NULL), count(*)
		FROM tariffs e
		JOIN cities ec ON ec.id = e.city_id AND ec.code = 'EMB'
		LEFT JOIN tariffs k ON k.city_id = $1 AND k.is_active AND k.category = e.category
		     AND k.vehicle_class = e.vehicle_class AND k.min_fare = e.min_fare AND k.max_fare = e.max_fare
		     AND k.fare_step = e.fare_step AND k.commission_bps = e.commission_bps
		WHERE e.is_active`, kaz.ID).Scan(&missing, &total); err != nil {
		t.Fatal(err)
	}
	if missing != 0 || total < 3 {
		t.Fatalf("KAZ tariffs: %d of %d missing", missing, total)
	}
	// A second default region is impossible.
	if _, err := pool.Exec(ctx, `UPDATE cities SET is_default_region = true WHERE code = 'EMB'`); err == nil {
		t.Fatal("two default regions accepted")
	}
}

func TestDefault(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	insertCity(t, pool, "ALA", almaty, true)
	for _, ref := range []string{"EMB", "emb", "Embi", "EMBI", "", "Nowhere"} {
		c, err := Default(ctx, pool, ref)
		if err != nil || c.Code != "EMB" {
			t.Errorf("Default(%q) = %s %v", ref, c.Code, err)
		}
	}
	if c, _ := Default(ctx, pool, "ala"); c.Code != "ALA" {
		t.Errorf("Default(ala) = %s", c.Code)
	}
	// An inactive default falls back to another active city.
	if _, err := pool.Exec(ctx, `UPDATE cities SET is_active = false WHERE code = 'EMB'`); err != nil {
		t.Fatal(err)
	}
	if c, _ := Default(ctx, pool, "EMB"); c.Code != "ALA" {
		t.Errorf("fallback = %s", c.Code)
	}
	if _, err := pool.Exec(ctx, `UPDATE cities SET is_active = false`); err != nil {
		t.Fatal(err)
	}
	if _, err := Default(ctx, pool, "EMB"); !errors.Is(err, ErrNoActiveCity) {
		t.Errorf("no city err = %v", err)
	}
}
