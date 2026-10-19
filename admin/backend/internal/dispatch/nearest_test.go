package dispatch

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

func TestPreviewETASecondsIsCoarse(t *testing.T) {
	for _, tc := range []struct {
		dist float64
		want int
	}{
		{0, 300}, {100, 300}, {2000, 300}, {2001, 600}, {4500, 600}, {4501, 900}, {-5, 300},
	} {
		if got := PreviewETASeconds(tc.dist); got != tc.want {
			t.Errorf("PreviewETASeconds(%v) = %d, want %d", tc.dist, got, tc.want)
		}
	}
}

func TestSnapETAPickup(t *testing.T) {
	embi := geo.Point{Lat: 48.8264, Lon: 58.1442}
	cell := SnapETAPickup(embi)
	if math.Abs(cell.Lat-embi.Lat) > ETAGridLatDeg/2 || math.Abs(cell.Lon-embi.Lon) > ETAGridLonDeg/2 {
		t.Fatalf("cell %v is not the one containing %v", cell, embi)
	}
	// Anywhere inside the cell gives the same point.
	for _, d := range [][2]float64{{0.0001, 0.0001}, {-0.002, 0.003}, {0.0024, -0.0037}} {
		p := geo.Point{Lat: cell.Lat + d[0], Lon: cell.Lon + d[1]}
		if got := SnapETAPickup(p); got != cell {
			t.Errorf("SnapETAPickup(%v) = %v, want %v", p, got, cell)
		}
	}
	if next := SnapETAPickup(geo.Point{Lat: cell.Lat + ETAGridLatDeg, Lon: cell.Lon}); next == cell {
		t.Fatal("the next cell snaps to the same point")
	}
}

func TestJitteredPosition(t *testing.T) {
	at := geo.Point{Lat: 43.2389, Lon: 76.8897}
	now := time.Date(2026, 10, 19, 21, 10, 0, 0, time.UTC)
	a, b := uuid.New(), uuid.New()

	pa := JitteredPosition(a, at, now)
	if d := geo.DistanceMeters(at, pa); d > ETAJitterM+1 {
		t.Fatalf("offset %.0f m > %v", d, ETAJitterM)
	}
	// Stable within the hour, so repeated previews cannot average it away.
	if again := JitteredPosition(a, at, now.Add(40*time.Minute)); again != pa {
		t.Fatalf("offset changed within the hour: %v vs %v", again, pa)
	}
	// Different per driver and per hour.
	moved, distinct := 0, 0
	for i := range 20 {
		id := uuid.New()
		h0 := JitteredPosition(id, at, now)
		if geo.DistanceMeters(at, h0) > 1 {
			moved++
		}
		if JitteredPosition(id, at, now.Add(time.Duration(i+1)*time.Hour)) != h0 {
			distinct++
		}
	}
	if moved < 19 || distinct < 19 || JitteredPosition(b, at, now) == pa {
		t.Fatalf("offsets are not per driver and hour: moved %d, distinct %d", moved, distinct)
	}
}

// The preview estimate must not be a distance oracle: an attacker who moves
// pickup A in small steps must not find the exact 500 m rings around a
// driver. With the snapped pickup and the shifted driver the estimate is the
// same for every pickup inside one grid cell, and its value changes only at
// cell borders, so the rings it reveals are off by up to a cell plus the
// offset.
func TestPreviewEstimateIsNoDistanceOracle(t *testing.T) {
	driver := uuid.New()
	at := geo.Point{Lat: 48.8264, Lon: 58.1442}
	now := time.Now()
	shifted := JitteredPosition(driver, at, now)
	eta := func(pickup geo.Point) int {
		return PreviewETASeconds(geo.DistanceMeters(SnapETAPickup(pickup), shifted))
	}
	// Walk north from the driver in 10 m steps for 6 km.
	changes := 0
	prev := eta(at)
	for i := 1; i <= 600; i++ {
		p := geo.Point{Lat: at.Lat + float64(i)*10/111_320, Lon: at.Lon}
		cur := eta(p)
		if cur%ETABucketS != 0 {
			t.Fatalf("estimate %d is not in %d-second steps", cur, ETABucketS)
		}
		if cur != prev {
			changes++
			// A change happens only where the snapped pickup changes.
			before := geo.Point{Lat: at.Lat + float64(i-1)*10/111_320, Lon: at.Lon}
			if SnapETAPickup(before) == SnapETAPickup(p) {
				t.Fatalf("estimate changed inside a grid cell at step %d", i)
			}
		}
		prev = cur
	}
	// 6 km at 120 s/km is 12 minutes: at most a few 5-minute steps.
	if changes > 3 {
		t.Fatalf("estimate changed %d times over 6 km", changes)
	}
}
