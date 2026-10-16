package routing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

func newTestOSRM(t *testing.T, f *fakeOSRM, cfg OSRMConfig) *OSRM {
	t.Helper()
	cfg.BaseURL = f.URL() + "/"
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = -1
	}
	c, err := NewOSRM(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func coordString(pts ...geo.Point) string {
	parts := make([]string, len(pts))
	for i, p := range pts {
		parts[i] = strconv.FormatFloat(p.Lon, 'f', 6, 64) + "," + strconv.FormatFloat(p.Lat, 'f', 6, 64)
	}
	return strings.Join(parts, ";")
}

func TestOSRMRoutes(t *testing.T) {
	f := newFakeOSRM(t)
	c := newTestOSRM(t, f, OSRMConfig{})
	ctx := context.Background()

	for _, stops := range [][]geo.Point{
		{embi, embiStation},
		{embi, embiStation, embiBazaar},
		{embi, embiStation, embiBazaar, embiSchool, aktobe},
	} {
		t.Run(strconv.Itoa(len(stops))+" points", func(t *testing.T) {
			r, err := c.Route(ctx, stops)
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			calls := f.callLog()
			last := calls[len(calls)-1]
			if last.coords != coordString(stops...) {
				t.Fatalf("OSRM saw %q, want stops in the given order %q", last.coords, coordString(stops...))
			}
			want := map[string]string{"overview": "full", "geometries": "geojson", "steps": "false", "annotations": "false",
				"alternatives": "false", "radiuses": strings.TrimSuffix(strings.Repeat("1000;", len(stops)), ";")}
			for k, v := range want {
				if last.query[k] != v {
					t.Errorf("query %s = %q, want %q", k, last.query[k], v)
				}
			}
			if len(r.Legs) != len(stops)-1 || len(r.Waypoints) != len(stops) {
				t.Fatalf("legs=%d waypoints=%d", len(r.Legs), len(r.Waypoints))
			}
			sum := 0
			for i, l := range r.Legs {
				d := geo.DistanceMeters(stops[i], stops[i+1]) * 1.3
				if diff := float64(l.DistanceM) - d; diff > 0.5 || diff < -0.5 {
					t.Errorf("leg %d distance %d, want %.1f", i, l.DistanceM, d)
				}
				sum += l.DistanceM
			}
			if diff := r.DistanceM - sum; diff > len(stops) || diff < -len(stops) {
				t.Errorf("total %d vs legs %d", r.DistanceM, sum)
			}
			if r.DurationS <= 0 || r.Waypoints[0].SnapDistanceM != 7.5 {
				t.Errorf("duration %d snap %v", r.DurationS, r.Waypoints[0].SnapDistanceM)
			}
			var ls struct {
				Type        string       `json:"type"`
				Coordinates [][2]float64 `json:"coordinates"`
			}
			if err := json.Unmarshal(r.Geometry, &ls); err != nil || ls.Type != "LineString" || len(ls.Coordinates) != len(stops) {
				t.Fatalf("geometry %s: %v", r.Geometry, err)
			}
			for _, s := range stops {
				if s.Lon < r.BBox[0] || s.Lat < r.BBox[1] || s.Lon > r.BBox[2] || s.Lat > r.BBox[3] {
					t.Fatalf("stop %v outside bbox %v", s, r.BBox)
				}
			}
			if r.BBox[0] > r.BBox[2] || r.BBox[1] > r.BBox[3] {
				t.Fatalf("bbox order %v", r.BBox)
			}
		})
	}
}

func TestOSRMBBox(t *testing.T) {
	geom := json.RawMessage(`{"type":"LineString","coordinates":[[58.1,48.8],[58.3,48.7],[58.2,48.9]]}`)
	line, bbox, err := normalizeLine(geom, []geo.Point{{Lat: 48.85, Lon: 58.05}})
	if err != nil {
		t.Fatal(err)
	}
	if bbox != [4]float64{58.05, 48.7, 58.3, 48.9} {
		t.Fatalf("bbox = %v", bbox)
	}
	if string(line) != `{"type":"LineString","coordinates":[[58.1,48.8],[58.3,48.7],[58.2,48.9]]}` {
		t.Fatalf("line = %s", line)
	}
	// A single position (zero-length route) still yields a valid LineString.
	line, _, err = normalizeLine(json.RawMessage(`{"type":"LineString","coordinates":[[58.1442004,48.8264]]}`), nil)
	if err != nil || string(line) != `{"type":"LineString","coordinates":[[58.1442,48.8264],[58.1442,48.8264]]}` {
		t.Fatalf("single point: %s %v", line, err)
	}
	for _, bad := range []string{`{"type":"Point","coordinates":[1,2]}`, `{"type":"LineString","coordinates":[]}`,
		`{"type":"LineString","coordinates":[[1]]}`, `{"type":"LineString","coordinates":[[200,10],[1,1]]}`, `[]`} {
		if _, _, err := normalizeLine(json.RawMessage(bad), nil); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestOSRMErrors(t *testing.T) {
	ctx := context.Background()
	stops := []geo.Point{embi, embiStation, embiBazaar}

	tests := []struct {
		name       string
		setup      func(f *fakeOSRM)
		cfg        OSRMConfig
		status     int
		reason     string
		index      any
		unavailble bool
	}{
		{"no route", func(f *fakeOSRM) { f.code, f.message = "NoRoute", "No route found between points" }, OSRMConfig{}, 422, "no_route", nil, false},
		{"no segment", func(f *fakeOSRM) {
			f.code, f.message = "NoSegment", "Could not find a matching segment for coordinate 2"
		}, OSRMConfig{}, 422, "point_not_routable", 2, false},
		{"no segment without index", func(f *fakeOSRM) { f.code, f.message = "NoSegment", "Could not find a matching segment" }, OSRMConfig{}, 422, "no_route", nil, false},
		{"snapped too far", func(f *fakeOSRM) { f.snap[1] = 1500 }, OSRMConfig{}, 422, "point_not_routable", 1, false},
		{"custom snap limit", func(f *fakeOSRM) { f.snap[2] = 300 }, OSRMConfig{MaxSnapMeters: 250}, 422, "point_not_routable", 2, false},
		{"invalid query", func(f *fakeOSRM) { f.code, f.message = "InvalidQuery", "Query string malformed" }, OSRMConfig{}, 503, "", nil, true},
		{"bad gateway", func(f *fakeOSRM) { f.status = http.StatusBadGateway }, OSRMConfig{}, 503, "", nil, true},
		{"malformed legs", func(f *fakeOSRM) { f.legs = 1 }, OSRMConfig{}, 503, "", nil, true},
		{"timeout", func(f *fakeOSRM) { f.delay = 500 * time.Millisecond }, OSRMConfig{Timeout: 50 * time.Millisecond}, 503, "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeOSRM(t)
			f.set(tt.setup)
			c := newTestOSRM(t, f, tt.cfg)
			start := time.Now()
			_, err := c.Route(ctx, stops)
			e := asHTTP(t, err)
			if e.Status != tt.status || e.Code != "route_unavailable" {
				t.Fatalf("got %d %s: %v", e.Status, e.Code, err)
			}
			if IsRoutingUnavailable(err) != tt.unavailble {
				t.Fatalf("IsRoutingUnavailable = %v", !tt.unavailble)
			}
			if tt.reason != "" && e.Details["reason"] != tt.reason {
				t.Fatalf("reason = %v", e.Details)
			}
			if tt.index != nil && e.Details["index"] != tt.index {
				t.Fatalf("index = %v", e.Details)
			}
			if tt.index == nil && tt.status == 422 {
				if _, ok := e.Details["index"]; ok {
					t.Fatalf("unexpected index: %v", e.Details)
				}
			}
			if tt.cfg.Timeout > 0 && time.Since(start) > 400*time.Millisecond {
				t.Fatalf("timeout not enforced: %v", time.Since(start))
			}
		})
	}

	// Unreachable server.
	f := newFakeOSRM(t)
	c := newTestOSRM(t, f, OSRMConfig{})
	f.srv.Close()
	_, err := c.Route(ctx, stops)
	if !IsRoutingUnavailable(err) {
		t.Fatalf("closed server: %v", err)
	}
	if strings.Contains(err.Error(), "58.144200") {
		t.Fatalf("error leaks the stop coordinates into logs: %v", err)
	}
}

func TestOSRMCacheAndInput(t *testing.T) {
	f := newFakeOSRM(t)
	c := newTestOSRM(t, f, OSRMConfig{CacheTTL: time.Minute})
	ctx := context.Background()
	r1, err := c.Route(ctx, []geo.Point{embi, embiStation})
	if err != nil {
		t.Fatal(err)
	}
	// Sub-micro-degree differences hit the same entry (6-decimal key).
	r2, err := c.Route(ctx, []geo.Point{{Lat: embi.Lat + 1e-8, Lon: embi.Lon}, embiStation})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.callLog()); n != 1 || r1.DistanceM != r2.DistanceM {
		t.Fatalf("calls = %d", n)
	}
	r2.Legs[0].DistanceM = -1 // callers get copies
	r3, _ := c.Route(ctx, []geo.Point{embi, embiStation})
	if r3.Legs[0].DistanceM < 0 {
		t.Fatal("cached route was mutated through a returned slice")
	}
	// Reversed order is a different route.
	if _, err := c.Route(ctx, []geo.Point{embiStation, embi}); err != nil || len(f.callLog()) != 2 {
		t.Fatalf("reversed: %v, calls %d", err, len(f.callLog()))
	}
	// Errors are not cached.
	f.set(func(f *fakeOSRM) { f.code = "NoRoute" })
	if _, err := c.Route(ctx, []geo.Point{embi, embiBazaar}); err == nil {
		t.Fatal("expected NoRoute")
	}
	f.set(func(f *fakeOSRM) { f.code = "" })
	if _, err := c.Route(ctx, []geo.Point{embi, embiBazaar}); err != nil {
		t.Fatalf("after recovery: %v", err)
	}

	if _, err := c.Route(ctx, []geo.Point{embi}); err == nil {
		t.Fatal("one stop accepted")
	}
	if _, err := c.Route(ctx, []geo.Point{embi, {Lat: 91, Lon: 0}}); err == nil {
		t.Fatal("invalid stop accepted")
	}
	for _, bad := range []string{"", "osrm:5000", "ftp://x", "http://"} {
		if _, err := NewOSRM(OSRMConfig{BaseURL: bad}); err == nil {
			t.Errorf("base URL %q accepted", bad)
		}
	}
}

func TestOSRMConcurrentCallsShareOneRequest(t *testing.T) {
	f := newFakeOSRM(t)
	f.set(func(f *fakeOSRM) { f.delay = 100 * time.Millisecond })
	c := newTestOSRM(t, f, OSRMConfig{})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Route(context.Background(), []geo.Point{embi, embiAkimat})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := len(f.callLog()); n != 1 {
		t.Fatalf("concurrent identical routes made %d requests", n)
	}

	// A caller that gives up returns promptly with its context error.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Route(ctx, []geo.Point{embi, embiPark}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled caller: %v", err)
	}
}
