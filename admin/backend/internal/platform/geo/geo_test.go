package geo

import (
	"context"
	"errors"
	"math"
	"net/http"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

var (
	embi      = Point{Lat: 48.8264, Lon: 58.1442}
	almaty    = Point{Lat: 43.2389, Lon: 76.8897}
	saryagash = Point{Lat: 41.4556, Lon: 69.1667}
	baikonur  = Point{Lat: 45.6167, Lon: 63.3167}
	astana    = Point{Lat: 51.1694, Lon: 71.4491}
	aktobe    = Point{Lat: 50.2839, Lon: 57.1670}
	orenburg  = Point{Lat: 51.7682, Lon: 55.0969}
	tashkent  = Point{Lat: 41.2995, Lon: 69.2401}
	bishkek   = Point{Lat: 42.8746, Lon: 74.5698}
	moscow    = Point{Lat: 55.7558, Lon: 37.6173}
	urumqi    = Point{Lat: 43.8256, Lon: 87.6168}
)

func TestValid(t *testing.T) {
	tests := []struct {
		p  Point
		ok bool
	}{
		{embi, true},
		{Point{90, 180}, true},
		{Point{-90, -180}, true},
		{Point{90.0001, 0}, false},
		{Point{0, -180.5}, false},
		{Point{math.NaN(), 0}, false},
		{Point{0, math.Inf(1)}, false},
	}
	for _, tc := range tests {
		if err := tc.p.Valid(); (err == nil) != tc.ok {
			t.Errorf("Valid(%v) = %v, want ok=%v", tc.p, err, tc.ok)
		}
	}
}

func TestDistanceMeters(t *testing.T) {
	tests := []struct {
		name      string
		a, b      Point
		want, tol float64
	}{
		{"same point", embi, embi, 0, 0.001},
		{"one degree of latitude", Point{48, 58}, Point{49, 58}, 111195, 50},
		{"almaty to astana", almaty, astana, 970_000, 15_000},
		{"embi to aktobe", embi, aktobe, 172_000, 5_000},
		{"antipodes", Point{0, 0}, Point{0, 180}, math.Pi * earthRadiusM, 1},
	}
	for _, tc := range tests {
		got := DistanceMeters(tc.a, tc.b)
		if math.Abs(got-tc.want) > tc.tol {
			t.Errorf("%s: got %.0f m, want %.0f±%.0f", tc.name, got, tc.want, tc.tol)
		}
		if back := DistanceMeters(tc.b, tc.a); math.Abs(back-got) > 1e-6 {
			t.Errorf("%s: distance not symmetric", tc.name)
		}
	}
}

func TestRounded(t *testing.T) {
	p := Point{Lat: 48.82641234, Lon: 58.14429999}.Rounded()
	if p.Lat != 48.826412 || p.Lon != 58.1443 {
		t.Fatalf("Rounded = %v", p)
	}
}

func TestInServiceArea(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	tests := []struct {
		name   string
		p      Point
		inside bool
	}{
		{"Embi", embi, true},
		{"Almaty", almaty, true},
		{"Saryagash (border town)", saryagash, true},
		{"Baikonur (leased, still KZ)", baikonur, true},
		{"Astana", astana, true},
		{"Orenburg", orenburg, false},
		{"Tashkent", tashkent, false},
		{"Bishkek", bishkek, false},
		{"Moscow", moscow, false},
		{"Urumqi", urumqi, false},
		{"null island", Point{0, 0}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := InServiceArea(ctx, pool, tc.p)
			if err != nil {
				t.Fatalf("InServiceArea: %v", err)
			}
			if got != tc.inside {
				t.Fatalf("InServiceArea(%v) = %v, want %v", tc.p, got, tc.inside)
			}
		})
	}
	if _, err := InServiceArea(ctx, pool, Point{Lat: 200}); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("invalid point: %v", err)
	}
}

func TestRequireInServiceArea(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()

	if err := RequireInServiceArea(ctx, pool, "stops", embi, almaty, saryagash, baikonur); err != nil {
		t.Fatalf("all inside: %v", err)
	}
	if err := RequireInServiceArea(ctx, pool, "stops"); err != nil {
		t.Fatalf("no points: %v", err)
	}

	tests := []struct {
		name  string
		pts   []Point
		code  string
		index int
	}{
		{"second stop in Orenburg", []Point{embi, orenburg, almaty}, "outside_service_area", 1},
		{"last stop in Tashkent", []Point{embi, almaty, tashkent}, "outside_service_area", 2},
		{"first stop in Moscow (bbox fast path)", []Point{moscow, embi}, "outside_service_area", 0},
		{"Bishkek", []Point{bishkek}, "outside_service_area", 0},
		{"invalid coordinate", []Point{embi, {Lat: math.NaN()}}, "validation_failed", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := RequireInServiceArea(ctx, pool, "stops", tc.pts...)
			var he *httpx.Error
			if !errors.As(err, &he) {
				t.Fatalf("want *httpx.Error, got %v", err)
			}
			if he.Status != http.StatusUnprocessableEntity || he.Code != tc.code {
				t.Fatalf("got %d %s, want 422 %s", he.Status, he.Code, tc.code)
			}
			if he.Details["field"] != "stops" || he.Details["index"] != tc.index {
				t.Fatalf("details = %v, want field=stops index=%d", he.Details, tc.index)
			}
		})
	}
}
