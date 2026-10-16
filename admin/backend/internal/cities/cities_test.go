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
	if err != nil || len(active) != 2 || active[0].Code != "ALA" || active[1].Code != "EMB" {
		t.Fatalf("ListActive = %v %v", active, err)
	}
}

func TestResolve(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	insertCity(t, pool, "ALA", almaty, true)
	// A city 5 km from Embi without its own zone: points inside Embi's 15 km
	// zone still belong to Embi even when this centre is nearer.
	near := geo.Point{Lat: 48.8264, Lon: 58.2125}
	insertCity(t, pool, "NBR", near, true)

	tests := []struct {
		name string
		p    geo.Point
		want string
	}{
		{"embi centre", embi, "EMB"},
		{"inside embi zone but nearer another centre", near, "EMB"},
		{"no zone, nearest centre", geo.Point{Lat: 43.30, Lon: 76.95}, "ALA"},
		{"far west, nearest centre", geo.Point{Lat: 47.10, Lon: 51.92}, "EMB"},
	}
	for _, tt := range tests {
		c, err := Resolve(ctx, pool, tt.p)
		if err != nil || c.Code != tt.want {
			t.Errorf("%s: got %s (%v), want %s", tt.name, c.Code, err, tt.want)
		}
	}

	// Inactive zones and cities are ignored.
	if _, err := pool.Exec(ctx, `UPDATE service_zones SET is_active = false`); err != nil {
		t.Fatal(err)
	}
	if c, _ := Resolve(ctx, pool, near); c.Code != "NBR" {
		t.Errorf("with zone inactive got %s, want NBR", c.Code)
	}
	if _, err := pool.Exec(ctx, `UPDATE cities SET is_active = false WHERE code = 'NBR'`); err != nil {
		t.Fatal(err)
	}
	if c, _ := Resolve(ctx, pool, near); c.Code != "EMB" {
		t.Errorf("with NBR inactive got %s, want EMB", c.Code)
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
