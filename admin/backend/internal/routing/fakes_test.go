package routing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// fakeOSRM imitates osrm-routed's /route/v1/driving endpoint: one leg per
// pair of points (haversine × 1.3, 10 m/s), the input points as geometry and
// a 7.5 m snap distance unless overridden.
type fakeOSRM struct {
	srv *httptest.Server

	mu      sync.Mutex
	calls   []fakeCall
	snap    map[int]float64
	code    string // non-empty: answer HTTP 400 with this OSRM code
	message string
	status  int // non-zero: answer with this HTTP status and a non-JSON body
	delay   time.Duration
	legs    int // non-zero: number of legs to return (malformed answers)
}

type fakeCall struct {
	coords string
	query  map[string]string
}

func newFakeOSRM(t *testing.T) *fakeOSRM {
	t.Helper()
	f := &fakeOSRM{snap: map[int]float64{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOSRM) URL() string { return f.srv.URL }

func (f *fakeOSRM) set(fn func(f *fakeOSRM)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeOSRM) callLog() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeCall(nil), f.calls...)
}

func (f *fakeOSRM) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	coords, ok := strings.CutPrefix(r.URL.Path, "/route/v1/driving/")
	// OSRM lists use literal ';', which net/url's parser rejects, so the raw
	// query is split by hand.
	q := map[string]string{}
	for _, kv := range strings.Split(r.URL.RawQuery, "&") {
		k, v, _ := strings.Cut(kv, "=")
		if uv, err := url.QueryUnescape(v); err == nil {
			q[k] = uv
		}
	}
	f.calls = append(f.calls, fakeCall{coords: coords, query: q})
	code, message, status, delay, legsOverride := f.code, f.message, f.status, f.delay, f.legs
	snap := map[int]float64{}
	for k, v := range f.snap {
		snap[k] = v
	}
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if code != "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
		return
	}
	var pts [][2]float64
	for _, c := range strings.Split(coords, ";") {
		lon, lat, _ := strings.Cut(c, ",")
		x, _ := strconv.ParseFloat(lon, 64)
		y, _ := strconv.ParseFloat(lat, 64)
		pts = append(pts, [2]float64{x, y})
	}
	type leg struct {
		Distance float64 `json:"distance"`
		Duration float64 `json:"duration"`
		Summary  string  `json:"summary"`
		Weight   float64 `json:"weight"`
	}
	var legs []leg
	total := 0.0
	for i := 1; i < len(pts); i++ {
		d := geo.DistanceMeters(geo.Point{Lat: pts[i-1][1], Lon: pts[i-1][0]}, geo.Point{Lat: pts[i][1], Lon: pts[i][0]}) * 1.3
		legs = append(legs, leg{Distance: d, Duration: d / 10, Weight: d / 10})
		total += d
	}
	if legsOverride > 0 {
		legs = legs[:min(legsOverride, len(legs))]
	}
	type wp struct {
		Hint     string     `json:"hint"`
		Distance float64    `json:"distance"`
		Name     string     `json:"name"`
		Location [2]float64 `json:"location"`
	}
	var wps []wp
	for i, p := range pts {
		d := 7.5
		if v, ok := snap[i]; ok {
			d = v
		}
		wps = append(wps, wp{Hint: "h", Distance: d, Name: "көше", Location: [2]float64{p[0] + 0.00001, p[1]}})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code": "Ok",
		"routes": []any{map[string]any{
			"distance": total, "duration": total / 10, "weight": total / 10, "weight_name": "routability",
			"geometry": map[string]any{"type": "LineString", "coordinates": pts},
			"legs":     legs,
		}},
		"waypoints": wps,
	})
}

// fakeGeocoder is a scripted Geocoder that counts calls.
type fakeGeocoder struct {
	mu      sync.Mutex
	search  []Place
	reverse *Place
	err     error
	delay   time.Duration
	// reverseDelay holds every reverse answer back (until the context ends).
	reverseDelay time.Duration
	searches     int
	reverses     int
}

func (g *fakeGeocoder) Search(ctx context.Context, _ string, _ *geo.Point, _ string, _ int) ([]Place, error) {
	g.mu.Lock()
	g.searches++
	res, err, delay := append([]Place(nil), g.search...), g.err, g.delay
	g.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return res, err
}

func (g *fakeGeocoder) Reverse(ctx context.Context, _ geo.Point, _ string) (*Place, error) {
	g.mu.Lock()
	g.reverses++
	res, err, delay := copyPlace(g.reverse), g.err, g.reverseDelay
	g.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return res, err
}

func (g *fakeGeocoder) counts() (int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.searches, g.reverses
}

func asHTTP(t *testing.T, err error) *httpx.Error {
	t.Helper()
	var e *httpx.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *httpx.Error, got %T: %v", err, err)
	}
	return e
}

// Points used across tests.
var (
	embi        = geo.Point{Lat: 48.8264, Lon: 58.1442}
	embiStation = geo.Point{Lat: 48.8291, Lon: 58.1504}
	embiBazaar  = geo.Point{Lat: 48.8247, Lon: 58.1418}
	embiSchool  = geo.Point{Lat: 48.8302, Lon: 58.1390}
	embiAkimat  = geo.Point{Lat: 48.8230, Lon: 58.1475}
	embiPark    = geo.Point{Lat: 48.8211, Lon: 58.1520}
	orenburg    = geo.Point{Lat: 51.7682, Lon: 55.0970}
	aktobe      = geo.Point{Lat: 50.2839, Lon: 57.1670}
)
