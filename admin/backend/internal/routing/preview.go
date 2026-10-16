package routing

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// Text limits of a stop (OpenAPI StopInput).
const (
	maxAddressLen = 200
	maxDetailsLen = textutil.MaxFreeText
)

// PaymentMethods are the ways a passenger can pay the driver.
var PaymentMethods = []string{"cash", "bank_transfer"}

// retryAfterSeconds is sent with 503 route_unavailable.
const retryAfterSeconds = "5"

type previewRequest struct {
	Category *string     `json:"category"`
	Stops    []stopInput `json:"stops"`
}

type stopInput struct {
	Lat            *float64 `json:"lat"`
	Lon            *float64 `json:"lon"`
	Address        *string  `json:"address"`
	AddressDetails *string  `json:"address_details"`
}

// quoteStop is the OpenAPI QuoteStop.
type quoteStop struct {
	Seq            int     `json:"seq"`
	Kind           string  `json:"kind"`
	Lat            float64 `json:"lat"`
	Lon            float64 `json:"lon"`
	Address        string  `json:"address"`
	AddressDetails *string `json:"address_details"`
}

type cityRef struct {
	ID   uuid.UUID   `json:"id"`
	Code string      `json:"code"`
	Name cities.Name `json:"name"`
}

// routeQuote is the OpenAPI RouteQuote.
type routeQuote struct {
	QuoteID       uuid.UUID       `json:"quote_id"`
	ExpiresAt     time.Time       `json:"expires_at"`
	Category      string          `json:"category"`
	City          cityRef         `json:"city"`
	Stops         []quoteStop     `json:"stops"`
	DistanceM     int             `json:"distance_m"`
	DurationS     int             `json:"duration_s"`
	Legs          []pricing.Leg   `json:"legs"`
	Geometry      json.RawMessage `json:"geometry"`
	BBox          [4]float64      `json:"bbox"`
	Fare          pricing.Fare    `json:"fare"`
	Currency      string          `json:"currency"`
	CommissionBps int             `json:"commission_bps"`
	PaymentMethod []string        `json:"payment_methods"`
}

// StopKind returns pickup / intermediate / destination for stop i of n.
func StopKind(i, n int) string {
	switch {
	case i == 0:
		return "pickup"
	case i == n-1:
		return "destination"
	default:
		return "intermediate"
	}
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) error {
	principal, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	var req previewRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	maxPoints := min(h.maxPoints, settings.MaxRoutePoints(ctx, h.pool), 5)
	category, stops, err := req.validate(maxPoints)
	if err != nil {
		return err
	}
	pts := make([]geo.Point, len(stops))
	for i, s := range stops {
		pts[i] = geo.Point{Lat: s.Lat, Lon: s.Lon}
	}
	if err := geo.RequireInServiceArea(ctx, h.pool, "stops", pts...); err != nil {
		return err
	}
	city, err := cities.Resolve(ctx, h.pool, pts[0])
	if err != nil {
		return err
	}
	tariff, err := pricing.ActiveTariff(ctx, h.pool, city.ID, category)
	if err != nil {
		return err
	}
	route, err := h.router.Route(ctx, pts)
	if err != nil {
		if IsRoutingUnavailable(err) {
			w.Header().Set("Retry-After", retryAfterSeconds)
		}
		return err
	}
	fare := pricing.Compute(tariff, route.DistanceM, route.DurationS, len(stops))
	quote, err := pricing.CreateQuote(ctx, h.pool, pricing.NewQuote{
		UserID: principal.UserID, CityID: city.ID, Tariff: tariff, Stops: stops,
		DistanceM: route.DistanceM, DurationS: route.DurationS, Legs: route.Legs,
		Geometry: route.Geometry, BBox: route.BBox, Fare: fare,
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, buildQuote(quote, city, fare))
}

func buildQuote(q pricing.StoredQuote, city cities.City, fare pricing.Fare) routeQuote {
	out := routeQuote{
		QuoteID: q.ID, ExpiresAt: q.ExpiresAt.UTC(), Category: q.Category,
		City:      cityRef{ID: city.ID, Code: city.Code, Name: city.Name},
		Stops:     make([]quoteStop, len(q.Stops)),
		DistanceM: q.DistanceM, DurationS: q.DurationS, Legs: q.Legs, Geometry: q.Geometry, BBox: q.BBox,
		Fare: fare, Currency: pricing.Currency, CommissionBps: q.CommissionBps, PaymentMethod: PaymentMethods,
	}
	for i, s := range q.Stops {
		qs := quoteStop{Seq: i, Kind: StopKind(i, len(q.Stops)), Lat: s.Lat, Lon: s.Lon, Address: s.Address}
		if s.AddressDetails != "" {
			d := s.AddressDetails
			qs.AddressDetails = &d
		}
		out.Stops[i] = qs
	}
	return out
}

// validate checks the request shape and texts and returns the category and
// cleaned stops (coordinates rounded to 6 decimals). The Kazakhstan check
// runs afterwards in one query.
func (req previewRequest) validate(maxPoints int) (string, []pricing.QuoteStop, error) {
	fields := map[string]string{}
	category := ""
	switch {
	case req.Category == nil || *req.Category == "":
		fields["category"] = "required"
	case !pricing.ValidCategory(*req.Category):
		fields["category"] = "invalid"
	default:
		category = *req.Category
	}
	switch {
	case req.Stops == nil:
		fields["stops"] = "required"
	case len(req.Stops) < 2:
		fields["stops"] = "too_few"
	case len(req.Stops) > maxPoints:
		fields["stops"] = "too_many"
	}
	if len(fields) > 0 {
		e := httpx.Validation(fields)
		if fields["stops"] == "too_many" {
			e = e.WithDetails(map[string]any{"max": maxPoints})
		}
		return "", nil, e
	}

	stops := make([]pricing.QuoteStop, len(req.Stops))
	for i, in := range req.Stops {
		p := "stops." + strconv.Itoa(i) + "."
		s := &stops[i]
		s.Lat = coordinate(fields, p+"lat", in.Lat, 90)
		s.Lon = coordinate(fields, p+"lon", in.Lon, 180)
		if in.Address == nil {
			fields[p+"address"] = "required"
		} else if a, err := textutil.CleanRequired(*in.Address, maxAddressLen); err != nil {
			fields[p+"address"] = textutil.Reason(err)
		} else {
			s.Address = a
		}
		if in.AddressDetails != nil {
			d, err := textutil.CleanText(*in.AddressDetails, maxDetailsLen)
			if err != nil {
				fields[p+"address_details"] = textutil.Reason(err)
			}
			s.AddressDetails = d
		}
	}
	if len(fields) > 0 {
		return "", nil, httpx.Validation(fields)
	}
	for i := 1; i < len(stops); i++ {
		if stops[i].Lat == stops[i-1].Lat && stops[i].Lon == stops[i-1].Lon {
			fields["stops."+strconv.Itoa(i)] = "duplicate"
		}
	}
	if len(fields) > 0 {
		return "", nil, httpx.Validation(fields)
	}
	return category, stops, nil
}

// coordinate validates one coordinate in [-limit, limit] and rounds it to
// 6 decimals.
func coordinate(fields map[string]string, key string, v *float64, limit float64) float64 {
	switch {
	case v == nil:
		fields[key] = "required"
	case math.IsNaN(*v) || *v < -limit || *v > limit:
		fields[key] = "invalid"
	default:
		return math.Round(*v*1e6) / 1e6
	}
	return 0
}
