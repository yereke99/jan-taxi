package routing

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/users"
)

// Per-user request budgets.
const (
	DefaultGeoPerMinute     = 60
	DefaultPreviewPerMinute = 30
)

// Limits of GET /geo/search.
const (
	searchMinLen       = 2
	searchMaxLen       = 100
	searchDefaultLimit = 10
	searchMaxLimit     = 20
)

// HandlerConfig configures NewHandler.
type HandlerConfig struct {
	Pool   *pgxpool.Pool
	Router Router
	Places *Places
	// MaxRoutePoints is the MAX_ROUTE_POINTS ceiling (2..5); the admin
	// setting routing.max_points can only lower it.
	MaxRoutePoints   int
	GeoPerMinute     int // DefaultGeoPerMinute when zero
	PreviewPerMinute int // DefaultPreviewPerMinute when zero
	Log              *slog.Logger
}

// Handler serves /geo/search, /geo/reverse and /routes/preview.
type Handler struct {
	pool      *pgxpool.Pool
	router    Router
	places    *Places
	maxPoints int
	geoLimit  *httpx.RateLimiter
	prevLimit *httpx.RateLimiter
	log       *slog.Logger
}

// NewHandler builds the handler.
func NewHandler(cfg HandlerConfig) *Handler {
	h := &Handler{pool: cfg.Pool, router: cfg.Router, places: cfg.Places, maxPoints: cfg.MaxRoutePoints, log: cfg.Log}
	if h.maxPoints < 2 || h.maxPoints > config.MaxRoutePointsLimit {
		h.maxPoints = config.MaxRoutePointsLimit
	}
	geoRate, prevRate := cfg.GeoPerMinute, cfg.PreviewPerMinute
	if geoRate <= 0 {
		geoRate = DefaultGeoPerMinute
	}
	if prevRate <= 0 {
		prevRate = DefaultPreviewPerMinute
	}
	h.geoLimit = httpx.NewRateLimiter(float64(geoRate), max(geoRate/3, 1))
	h.prevLimit = httpx.NewRateLimiter(float64(prevRate), max(prevRate/3, 1))
	if h.places == nil {
		h.places = NewPlaces(cfg.Pool, PlacesConfig{Log: cfg.Log})
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	return h
}

// MountApp registers the geo routes on a router that accepts passenger and
// driver app tokens.
func (h *Handler) MountApp(r chi.Router) {
	limited := r.With(h.geoLimit.Middleware(userKey))
	limited.Get("/geo/search", httpx.H(h.search))
	limited.Get("/geo/reverse", httpx.H(h.reverseGeocode))
}

// MountPassenger registers POST /routes/preview on the passenger router.
func (h *Handler) MountPassenger(r chi.Router) {
	r.With(h.prevLimit.Middleware(userKey)).Post("/routes/preview", httpx.H(h.preview))
}

// userKey rate-limits per authenticated user (per IP as a fallback).
func userKey(r *http.Request) string {
	if p, ok := auth.PrincipalFrom(r.Context()); ok {
		return "user:" + p.UserID.String()
	}
	return "ip:" + httpx.ClientIP(r)
}

// geoPlace is the OpenAPI GeoPlace.
type geoPlace struct {
	Title    string  `json:"title"`
	Subtitle *string `json:"subtitle"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Source   string  `json:"source"`
}

func toGeoPlace(p Place) geoPlace {
	pt := p.Point.Rounded()
	g := geoPlace{Title: p.Title, Lat: pt.Lat, Lon: pt.Lon, Source: p.Source}
	if p.Subtitle != "" {
		s := p.Subtitle
		g.Subtitle = &s
	}
	return g
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) error {
	principal, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	query := r.URL.Query()
	fields := map[string]string{}
	q := textutil.Clean(query.Get("q"))
	switch n := textutil.Len(q); {
	case n == 0:
		fields["q"] = "required"
	case n < searchMinLen:
		fields["q"] = "too_short"
	case n > searchMaxLen:
		fields["q"] = "too_long"
	}
	near, ok := optionalPoint(query.Get("lat"), query.Get("lon"), fields)
	limit, err := httpx.QueryInt(r, "limit", searchDefaultLimit, 1, searchMaxLimit)
	if err != nil {
		fields["limit"] = "invalid"
	}
	lang := langParam(r, fields)
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	if lang == "" {
		if lang, err = users.Language(r.Context(), h.pool, principal.UserID); err != nil {
			return err
		}
	}
	var nearPtr *geo.Point
	if ok {
		nearPtr = &near
	}
	places, err := h.places.Search(r.Context(), q, nearPtr, lang, limit)
	if err != nil {
		return err
	}
	out := make([]geoPlace, 0, len(places))
	for _, p := range places {
		out = append(out, toGeoPlace(p))
	}
	return httpx.OK(w, out)
}

// geoReverseResult is the OpenAPI GeoReverseResult.
type geoReverseResult struct {
	geoPlace
	InServiceArea bool `json:"in_service_area"`
}

func (h *Handler) reverseGeocode(w http.ResponseWriter, r *http.Request) error {
	principal, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	query := r.URL.Query()
	fields := map[string]string{}
	p, ok := optionalPoint(query.Get("lat"), query.Get("lon"), fields)
	if !ok && len(fields) == 0 {
		fields["lat"], fields["lon"] = "required", "required"
	}
	lang := langParam(r, fields)
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	if lang == "" {
		if lang, err = users.Language(r.Context(), h.pool, principal.UserID); err != nil {
			return err
		}
	}
	res, err := h.places.Reverse(r.Context(), p, lang)
	if err != nil {
		return err
	}
	return httpx.OK(w, geoReverseResult{geoPlace: toGeoPlace(res.Place), InServiceArea: res.InServiceArea})
}

// optionalPoint parses a lat/lon query pair; both or neither must be given.
func optionalPoint(latS, lonS string, fields map[string]string) (geo.Point, bool) {
	latS, lonS = strings.TrimSpace(latS), strings.TrimSpace(lonS)
	if latS == "" && lonS == "" {
		return geo.Point{}, false
	}
	var p geo.Point
	var err error
	bad := false
	if latS == "" {
		fields["lat"], bad = "required", true
	} else if p.Lat, err = strconv.ParseFloat(latS, 64); err != nil || math.IsNaN(p.Lat) || p.Lat < -90 || p.Lat > 90 {
		fields["lat"], bad = "invalid", true
	}
	if lonS == "" {
		fields["lon"], bad = "required", true
	} else if p.Lon, err = strconv.ParseFloat(lonS, 64); err != nil || math.IsNaN(p.Lon) || p.Lon < -180 || p.Lon > 180 {
		fields["lon"], bad = "invalid", true
	}
	if bad {
		return geo.Point{}, false
	}
	return p, true
}

// langParam reads ?lang= ("" when absent). An unknown value is a field error.
func langParam(r *http.Request, fields map[string]string) string {
	lang := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang")))
	if lang != "" && !users.ValidLanguage(lang) {
		fields["lang"] = "invalid"
		return ""
	}
	return lang
}
