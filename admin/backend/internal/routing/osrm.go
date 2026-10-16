package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
)

// The OSRM client talks to a self-hosted osrm-routed built from the Kazakhstan
// extract (OSRM_BASE_URL, default http://osrm:5000). The public demo server
// (router.project-osrm.org) has no SLA and forbids heavy use; point
// OSRM_BASE_URL at it only for local development.

// Defaults of the OSRM client.
const (
	DefaultOSRMTimeout   = 5 * time.Second
	DefaultMaxSnapMeters = 1000.0
	osrmCacheTTL         = 10 * time.Minute
	osrmCacheSize        = 2000
	osrmMaxResponse      = 16 << 20
	userAgent            = "JanTaxi-API/1.0"
)

// Route is a driving route through the stops in the given order.
type Route struct {
	DistanceM int
	DurationS int
	Legs      []pricing.Leg   // one per pair of consecutive stops
	Geometry  json.RawMessage // GeoJSON LineString, [lon, lat] positions
	BBox      [4]float64      // [min_lon, min_lat, max_lon, max_lat] of the geometry and the stops
	Waypoints []Waypoint      // one per stop
}

// Waypoint is a stop snapped to the road network.
type Waypoint struct {
	Location      geo.Point
	SnapDistanceM float64
}

// Router computes driving routes. *OSRM implements it.
type Router interface {
	Route(ctx context.Context, stops []geo.Point) (Route, error)
}

// OSRMConfig configures NewOSRM.
type OSRMConfig struct {
	BaseURL       string
	Timeout       time.Duration // per request; DefaultOSRMTimeout when zero
	MaxSnapMeters float64       // stops farther from a road are rejected; DefaultMaxSnapMeters when zero
	CacheTTL      time.Duration // 10 minutes when zero; negative disables the cache
	HTTPClient    *http.Client
}

// OSRM is a client of the OSRM route service.
type OSRM struct {
	base    string
	timeout time.Duration
	maxSnap float64
	client  *http.Client
	cache   *ttlCache[string, Route]
	flight  singleflight.Group
}

// NewOSRM validates cfg and builds the client. It performs no network I/O.
func NewOSRM(cfg OSRMConfig) (*OSRM, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("osrm: invalid base URL %q", cfg.BaseURL)
	}
	c := &OSRM{
		base:    strings.TrimRight(u.String(), "/"),
		timeout: cfg.Timeout,
		maxSnap: cfg.MaxSnapMeters,
		client:  cfg.HTTPClient,
	}
	if c.timeout <= 0 {
		c.timeout = DefaultOSRMTimeout
	}
	if c.maxSnap <= 0 {
		c.maxSnap = DefaultMaxSnapMeters
	}
	if c.client == nil {
		c.client = &http.Client{}
	}
	ttl := cfg.CacheTTL
	if ttl == 0 {
		ttl = osrmCacheTTL
	}
	if ttl > 0 {
		c.cache = newTTLCache[string, Route](ttl, osrmCacheSize)
	}
	return c, nil
}

// Errors returned by Route. All are *httpx.Error.
var errRoutingDown = httpx.NewError(http.StatusServiceUnavailable, "route_unavailable", "routing service unavailable")

// ErrNoRoute is 422 route_unavailable: no road route connects the stops.
func ErrNoRoute(index int) *httpx.Error {
	d := map[string]any{"reason": "no_route"}
	if index >= 0 {
		d["index"] = index
	}
	return httpx.Unprocessable("route_unavailable", "no route between the points").WithDetails(d)
}

// ErrPointNotRoutable is 422 route_unavailable: stop index is too far from any road.
func ErrPointNotRoutable(index int) *httpx.Error {
	return httpx.Unprocessable("route_unavailable", "point is too far from a road").
		WithDetails(map[string]any{"reason": "point_not_routable", "index": index})
}

// IsRoutingUnavailable reports whether err means the routing engine could not
// be reached (503), as opposed to a route that does not exist (422).
func IsRoutingUnavailable(err error) bool {
	var e *httpx.Error
	return errors.As(err, &e) && e.Status == http.StatusServiceUnavailable && e.Code == "route_unavailable"
}

func unavailable(err error) *httpx.Error { return errRoutingDown.WithCause(err) }

// Route returns the driving route through stops in exactly the given order
// (OSRM /route never reorders waypoints). Results are cached by coordinates
// rounded to 6 decimals.
func (c *OSRM) Route(ctx context.Context, stops []geo.Point) (Route, error) {
	if len(stops) < 2 {
		return Route{}, fmt.Errorf("osrm: need at least 2 stops, got %d", len(stops))
	}
	coords := make([]string, len(stops))
	for i, p := range stops {
		if err := p.Valid(); err != nil {
			return Route{}, fmt.Errorf("osrm: stop %d: %w", i, err)
		}
		coords[i] = strconv.FormatFloat(p.Lon, 'f', 6, 64) + "," + strconv.FormatFloat(p.Lat, 'f', 6, 64)
	}
	key := strings.Join(coords, ";")
	if r, ok := c.cache.get(key); ok {
		return r.clone(), nil
	}
	ch := c.flight.DoChan(key, func() (any, error) {
		// Detached from the first caller so one cancelled request does not
		// fail the others sharing this flight; bounded by the timeout.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
		defer cancel()
		r, err := c.fetch(rctx, key, stops)
		if err == nil {
			c.cache.set(key, r)
		}
		return r, err
	})
	select {
	case <-ctx.Done():
		return Route{}, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return Route{}, res.Err
		}
		return res.Val.(Route).clone(), nil
	}
}

func (r Route) clone() Route {
	r.Legs = append([]pricing.Leg(nil), r.Legs...)
	r.Waypoints = append([]Waypoint(nil), r.Waypoints...)
	return r
}

type osrmResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Routes  []struct {
		Distance float64         `json:"distance"`
		Duration float64         `json:"duration"`
		Geometry json.RawMessage `json:"geometry"`
		Legs     []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
		} `json:"legs"`
	} `json:"routes"`
	Waypoints []struct {
		Distance float64    `json:"distance"`
		Location [2]float64 `json:"location"`
	} `json:"waypoints"`
}

// segmentIndexRe extracts the stop index from OSRM's NoSegment message
// "Could not find a matching segment for coordinate N" (0-based).
var segmentIndexRe = regexp.MustCompile(`coordinate (\d+)`)

func (c *OSRM) fetch(ctx context.Context, coords string, stops []geo.Point) (Route, error) {
	radius := strconv.FormatFloat(c.maxSnap, 'f', 0, 64)
	radiuses := strings.TrimSuffix(strings.Repeat(radius+";", len(stops)), ";")
	q := url.Values{}
	q.Set("overview", "full")
	q.Set("geometries", "geojson")
	q.Set("steps", "false")
	q.Set("annotations", "false")
	q.Set("alternatives", "false")
	q.Set("radiuses", radiuses)
	// Coordinates and radiuses use ';' and ',' literally, as OSRM expects.
	endpoint := c.base + "/route/v1/driving/" + coords + "?" + strings.ReplaceAll(q.Encode(), "%3B", ";")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Route{}, unavailable(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.client.Do(req)
	if err != nil {
		// The URL carries the passenger's coordinates; keep it out of logs.
		return Route{}, unavailable(scrubURL(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, osrmMaxResponse+1))
	if err != nil {
		return Route{}, unavailable(err)
	}
	if len(body) > osrmMaxResponse {
		return Route{}, unavailable(errors.New("osrm: response too large"))
	}
	if resp.StatusCode >= 500 {
		return Route{}, unavailable(fmt.Errorf("osrm: HTTP %d", resp.StatusCode))
	}
	var out osrmResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return Route{}, unavailable(fmt.Errorf("osrm: HTTP %d, undecodable body: %w", resp.StatusCode, err))
	}
	switch out.Code {
	case "Ok":
	case "NoRoute":
		return Route{}, ErrNoRoute(-1)
	case "NoSegment":
		if m := segmentIndexRe.FindStringSubmatch(out.Message); m != nil {
			if i, err := strconv.Atoi(m[1]); err == nil && i >= 0 && i < len(stops) {
				return Route{}, ErrPointNotRoutable(i)
			}
		}
		return Route{}, ErrNoRoute(-1)
	default:
		return Route{}, unavailable(fmt.Errorf("osrm: HTTP %d code %q: %s", resp.StatusCode, out.Code, out.Message))
	}
	return c.parse(out, stops)
}

func (c *OSRM) parse(out osrmResponse, stops []geo.Point) (Route, error) {
	if len(out.Routes) == 0 {
		return Route{}, ErrNoRoute(-1)
	}
	rt := out.Routes[0]
	if len(rt.Legs) != len(stops)-1 || len(out.Waypoints) != len(stops) {
		return Route{}, unavailable(fmt.Errorf("osrm: %d legs and %d waypoints for %d stops",
			len(rt.Legs), len(out.Waypoints), len(stops)))
	}
	r := Route{
		DistanceM: roundNonNeg(rt.Distance),
		DurationS: roundNonNeg(rt.Duration),
		Legs:      make([]pricing.Leg, len(rt.Legs)),
		Waypoints: make([]Waypoint, len(out.Waypoints)),
	}
	for i, l := range rt.Legs {
		r.Legs[i] = pricing.Leg{DistanceM: roundNonNeg(l.Distance), DurationS: roundNonNeg(l.Duration)}
	}
	for i, w := range out.Waypoints {
		if w.Distance > c.maxSnap {
			return Route{}, ErrPointNotRoutable(i)
		}
		r.Waypoints[i] = Waypoint{Location: geo.Point{Lat: w.Location[1], Lon: w.Location[0]}, SnapDistanceM: w.Distance}
	}
	geom, bbox, err := normalizeLine(rt.Geometry, stops)
	if err != nil {
		return Route{}, unavailable(err)
	}
	r.Geometry, r.BBox = geom, bbox
	return r, nil
}

type lineString struct {
	Type        string       `json:"type"`
	Coordinates [][2]float64 `json:"coordinates"`
}

// normalizeLine validates a GeoJSON LineString, rounds positions to 6
// decimals and returns it with the bounding box of the line and the stops.
func normalizeLine(raw json.RawMessage, stops []geo.Point) (json.RawMessage, [4]float64, error) {
	var ls struct {
		Type        string      `json:"type"`
		Coordinates [][]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(raw, &ls); err != nil {
		return nil, [4]float64{}, fmt.Errorf("osrm: geometry: %w", err)
	}
	if ls.Type != "LineString" || len(ls.Coordinates) == 0 {
		return nil, [4]float64{}, fmt.Errorf("osrm: unexpected geometry type %q with %d positions", ls.Type, len(ls.Coordinates))
	}
	out := lineString{Type: "LineString", Coordinates: make([][2]float64, 0, max(len(ls.Coordinates), 2))}
	bbox := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	extend := func(lon, lat float64) {
		bbox[0], bbox[1] = math.Min(bbox[0], lon), math.Min(bbox[1], lat)
		bbox[2], bbox[3] = math.Max(bbox[2], lon), math.Max(bbox[3], lat)
	}
	for _, c := range ls.Coordinates {
		if len(c) < 2 {
			return nil, [4]float64{}, errors.New("osrm: geometry position without lon/lat")
		}
		p := geo.Point{Lat: c[1], Lon: c[0]}
		if p.Valid() != nil {
			return nil, [4]float64{}, errors.New("osrm: geometry position out of range")
		}
		p = p.Rounded()
		out.Coordinates = append(out.Coordinates, [2]float64{p.Lon, p.Lat})
		extend(p.Lon, p.Lat)
	}
	if len(out.Coordinates) == 1 { // a LineString needs two positions
		out.Coordinates = append(out.Coordinates, out.Coordinates[0])
	}
	for _, s := range stops {
		p := s.Rounded()
		extend(p.Lon, p.Lat)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, [4]float64{}, err
	}
	return b, bbox, nil
}

func roundNonNeg(v float64) int {
	if v <= 0 || math.IsNaN(v) {
		return 0
	}
	return int(math.Round(v))
}
