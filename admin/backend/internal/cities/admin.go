package cities

import (
	"errors"
	"math"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// AdminHandler serves the admin endpoints for cities, zones and places.
type AdminHandler struct {
	pool *pgxpool.Pool
}

// NewAdminHandler builds the admin handler.
func NewAdminHandler(pool *pgxpool.Pool) *AdminHandler { return &AdminHandler{pool: pool} }

// MountAdmin registers the routes on the admin router (which already requires
// an admin session); each route adds its own permission.
func (h *AdminHandler) MountAdmin(r chi.Router) {
	r.With(auth.Permit(auth.PermDashboardView)).Get("/cities", httpx.H(h.listCities))
	r.Group(func(r chi.Router) {
		r.Use(auth.Permit(auth.PermConfigManage))
		r.Post("/cities", httpx.H(h.createCity))
		r.Patch("/cities/{id}", httpx.H(h.updateCity))

		r.Get("/cities/{id}/zones", httpx.H(h.listZones))
		r.Post("/cities/{id}/zones", httpx.H(h.createZone))
		r.Patch("/zones/{id}", httpx.H(h.updateZone))
		r.Delete("/zones/{id}", httpx.H(h.deleteZone))

		r.Get("/places", httpx.H(h.listPlaces))
		r.Post("/places", httpx.H(h.createPlace))
		r.Patch("/places/{id}", httpx.H(h.updatePlace))
		r.Delete("/places/{id}", httpx.H(h.deletePlace))
	})
}

// Validation reason codes used by this package.
const (
	reasonRequired   = "required"
	reasonInvalid    = "invalid"
	reasonTooLong    = "too_long"
	reasonOutOfRange = "out_of_range"
	reasonOutside    = "outside_service_area"
	reasonImmutable  = "immutable"
	reasonNotFound   = "not_found"
	reasonDuplicate  = "duplicate"
	reasonTooMany    = "too_many"
)

// maxNameLen matches the places name check constraint (120 code points).
const maxNameLen = 120

// cleanLabel normalises a required short text (name, alias).
func cleanLabel(s string) (string, string) {
	out, err := textutil.CleanText(s, maxNameLen)
	switch {
	case errors.Is(err, textutil.ErrTooLong):
		return "", reasonTooLong
	case err != nil:
		return "", reasonInvalid
	case out == "":
		return "", reasonRequired
	}
	return out, ""
}

// cleanNames validates all three translations of a name, reporting problems
// under prefix.kk / prefix.ru / prefix.en.
func cleanNames(n *Name, prefix string, fields map[string]string) Name {
	if n == nil {
		fields[prefix] = reasonRequired
		return Name{}
	}
	var out Name
	for _, f := range []struct {
		lang string
		in   string
		out  *string
	}{{"kk", n.KK, &out.KK}, {"ru", n.RU, &out.RU}, {"en", n.EN, &out.EN}} {
		v, reason := cleanLabel(f.in)
		if reason != "" {
			fields[prefix+"."+f.lang] = reason
		}
		*f.out = v
	}
	return out
}

// pointInput is the request shape {"lat":…,"lon":…}.
type pointInput struct {
	Lat *float64 `json:"lat"`
	Lon *float64 `json:"lon"`
}

func (p *pointInput) point() (geo.Point, bool) {
	if p == nil || p.Lat == nil || p.Lon == nil {
		return geo.Point{}, false
	}
	pt := geo.Point{Lat: *p.Lat, Lon: *p.Lon}
	return pt, validPoint(pt)
}

func inRange(v, lo, hi int) bool { return v >= lo && v <= hi }

func finiteIn(v, lo, hi float64) bool { return !math.IsNaN(v) && v >= lo && v <= hi }
