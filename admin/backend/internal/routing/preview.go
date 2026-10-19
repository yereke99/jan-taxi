package routing

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicleclasses"
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
	// PassengerCount (taxi, 1..15, default 1) and CargoSize (parcel/other,
	// s|m|l|xl, default s) decide which vehicle classes are offered (§21.3).
	PassengerCount *int64  `json:"passenger_count"`
	CargoSize      *string `json:"cargo_size"`
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

// routeQuote is the OpenAPI RouteQuote: the route once plus one option per
// vehicle class able to carry the request. The top-level quote_id,
// expires_at, vehicle_class, fare and commission_bps repeat the first option
// for clients that do not read options.
type routeQuote struct {
	QuoteID        uuid.UUID `json:"quote_id"`
	ExpiresAt      time.Time `json:"expires_at"`
	Category       string    `json:"category"`
	VehicleClass   string    `json:"vehicle_class"`
	PassengerCount *int      `json:"passenger_count"`
	CargoSize      *string   `json:"cargo_size"`
	City           cityRef   `json:"city"`
	// Locality is the settlement of the pickup from reverse geocoding (nil
	// when unknown); TariffRegion is the city or nationwide region whose
	// tariff priced the quote.
	Locality      *string         `json:"locality"`
	TariffRegion  cities.Region   `json:"tariff_region"`
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
	Options       []classOption   `json:"options"`
}

// classOption is the OpenAPI RouteQuoteOption: the quote of one vehicle class.
type classOption struct {
	VehicleClass  string              `json:"vehicle_class"`
	Name          vehicleclasses.Name `json:"name"`
	Examples      string              `json:"examples"`
	Icon          string              `json:"icon"`
	SeatsMax      int                 `json:"seats_max"`
	CargoVolumeL  int                 `json:"cargo_volume_l"`
	QuoteID       uuid.UUID           `json:"quote_id"`
	ExpiresAt     time.Time           `json:"expires_at"`
	Fare          pricing.Fare        `json:"fare"`
	CommissionBps int                 `json:"commission_bps"`
	// TariffRegion is the city or nationwide region whose tariff priced
	// this class (a zone city without a tariff for it falls back to KAZ).
	TariffRegion cities.Region `json:"tariff_region"`
	// EtaS is a coarse pickup estimate in whole 5-minute steps
	// (dispatch.PreviewETAs), nil without a fitting driver nearby.
	EtaS *int `json:"eta_s"`
}

// pricedClass is a vehicle class able to carry the request with the tariff
// that prices it and the region that tariff belongs to.
type pricedClass struct {
	vehicleclasses.Class
	Tariff pricing.Tariff
	Region cities.City
}

// previewInput is a validated preview request.
type previewInput struct {
	Category string
	Stops    []pricing.QuoteStop
	Load     pricing.Load // without VehicleClass
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
	in, err := req.validate(maxPoints)
	if err != nil {
		return err
	}
	stops := in.Stops
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
	classes, err := h.capableClasses(ctx, city, in)
	if err != nil {
		return err
	}
	// The locality of the pickup is looked up once, while the route is built.
	localityCh := make(chan *string, 1)
	go func() { localityCh <- h.places.Locality(ctx, pts[0]) }()
	route, err := h.router.Route(ctx, pts)
	if err != nil {
		if IsRoutingUnavailable(err) {
			w.Header().Set("Retry-After", retryAfterSeconds)
		}
		return err
	}
	codes := make([]string, len(classes))
	for i, c := range classes {
		codes[i] = c.Code
	}
	etas, err := dispatch.PreviewETAs(ctx, h.pool, pts[0], codes, dispatchLoad(in.Load), principal.UserID)
	if err != nil {
		return err
	}

	locality := <-localityCh

	quotes := make([]pricing.StoredQuote, len(classes))
	fares := make([]pricing.Fare, len(classes))
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		for i, c := range classes {
			fares[i] = pricing.Compute(c.Tariff, route.DistanceM, route.DurationS, len(stops))
			q, err := pricing.CreateQuote(ctx, tx, pricing.NewQuote{
				UserID: principal.UserID, CityID: c.Region.ID, Tariff: c.Tariff, Load: in.Load, Stops: stops,
				DistanceM: route.DistanceM, DurationS: route.DurationS, Legs: route.Legs,
				Geometry: route.Geometry, BBox: route.BBox, Fare: fares[i], Locality: locality,
			})
			if err != nil {
				return err
			}
			quotes[i] = q
		}
		return nil
	})
	if err != nil {
		return err
	}
	out := buildQuote(quotes[0], classes[0].Region, fares[0])
	out.Options = make([]classOption, len(classes))
	for i, c := range classes {
		out.Options[i] = classOption{
			VehicleClass: c.Code, Name: c.Name, Examples: c.Examples, Icon: c.Icon, SeatsMax: c.SeatsMax,
			CargoVolumeL: c.CargoVolumeLDefault, QuoteID: quotes[i].ID, ExpiresAt: quotes[i].ExpiresAt.UTC(),
			Fare: fares[i], CommissionBps: quotes[i].CommissionBps, TariffRegion: c.Region.Ref(),
		}
		if eta, ok := etas[c.Code]; ok {
			out.Options[i].EtaS = &eta
		}
	}
	return httpx.OK(w, out)
}

// capableClasses returns the active vehicle classes, in display order, that
// can carry the request and have an active tariff for the category, each with
// its tariff. The tariff comes from city (the region whose zone covers the
// pickup); a class or category that city has no active tariff for is priced
// by the nationwide default region (KAZ), as it is everywhere outside the
// city's zones, so drawing a city zone never switches a service off. No
// class able to carry the load is 422 on passenger_count (taxi) or
// cargo_size; classes that could carry it but have no tariff anywhere is
// ErrNoActiveTariff.
func (h *Handler) capableClasses(ctx context.Context, city cities.City, in previewInput) ([]pricedClass, error) {
	all, err := vehicleclasses.List(ctx, h.pool, true)
	if err != nil {
		return nil, err
	}
	tariffs, err := pricing.ActiveTariffs(ctx, h.pool, city.ID, in.Category)
	if err != nil {
		return nil, err
	}
	load := dispatchLoad(in.Load)
	var carriers []vehicleclasses.Class
	missing := false
	for _, c := range all {
		if c.CanCarry(in.Category, load.PassengerCount, load.CargoVolumeL) {
			carriers = append(carriers, c)
			_, ok := tariffs[c.Code]
			missing = missing || !ok
		}
	}
	var (
		base   map[string]pricing.Tariff
		region cities.City
	)
	if missing && !city.IsDefaultRegion {
		region, err = cities.DefaultRegion(ctx, h.pool)
		switch {
		case err == nil:
			if base, err = pricing.ActiveTariffs(ctx, h.pool, region.ID, in.Category); err != nil {
				return nil, err
			}
		case !errors.Is(err, cities.ErrNoActiveCity): // no nationwide region: the city's tariffs only
			return nil, err
		}
	}
	priced := []pricedClass{}
	for _, c := range carriers {
		if t, ok := tariffs[c.Code]; ok {
			priced = append(priced, pricedClass{Class: c, Tariff: t, Region: city})
		} else if t, ok := base[c.Code]; ok {
			priced = append(priced, pricedClass{Class: c, Tariff: t, Region: region})
		}
	}
	switch {
	case len(carriers) == 0 && in.Category == pricing.CategoryTaxi:
		return nil, httpx.Validation(map[string]string{"passenger_count": "too_large"})
	case len(carriers) == 0:
		return nil, httpx.Validation(map[string]string{"cargo_size": "too_large"})
	case len(priced) == 0:
		return nil, pricing.ErrNoActiveTariff
	}
	return priced, nil
}

func dispatchLoad(l pricing.Load) dispatch.Load {
	out := dispatch.Load{CargoVolumeL: l.CargoVolumeL}
	if l.PassengerCount != nil {
		out.PassengerCount = *l.PassengerCount
	}
	return out
}

func buildQuote(q pricing.StoredQuote, city cities.City, fare pricing.Fare) routeQuote {
	out := routeQuote{
		QuoteID: q.ID, ExpiresAt: q.ExpiresAt.UTC(), Category: q.Category, VehicleClass: q.VehicleClass,
		PassengerCount: q.PassengerCount, CargoSize: q.CargoSize,
		City:     cityRef{ID: city.ID, Code: city.Code, Name: city.Name},
		Locality: q.Locality, TariffRegion: city.Ref(),
		Stops:     make([]quoteStop, len(q.Stops)),
		DistanceM: q.DistanceM, DurationS: q.DurationS, Legs: q.Legs, Geometry: q.Geometry, BBox: q.BBox,
		Fare: fare, Currency: pricing.Currency, CommissionBps: q.CommissionBps, PaymentMethod: PaymentMethods,
		Options: []classOption{},
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

// validate checks the request shape and texts and returns the category,
// cleaned stops (coordinates rounded to 6 decimals) and the load. The
// Kazakhstan check runs afterwards in one query.
func (req previewRequest) validate(maxPoints int) (previewInput, error) {
	in, err := req.validateRoute(maxPoints)
	if err != nil {
		return previewInput{}, err
	}
	fields := map[string]string{}
	if in.Category == pricing.CategoryTaxi {
		n := int64(1)
		switch {
		case req.PassengerCount != nil && *req.PassengerCount < 1:
			fields["passenger_count"] = "too_small"
		case req.PassengerCount != nil && *req.PassengerCount > vehicleclasses.MaxPassengerCount:
			fields["passenger_count"] = "too_large"
		case req.PassengerCount != nil:
			n = *req.PassengerCount
		}
		if req.CargoSize != nil {
			fields["cargo_size"] = "not_allowed"
		}
		count := int(n)
		in.Load.PassengerCount = &count
	} else {
		size := vehicleclasses.DefaultCargoSize
		if req.CargoSize != nil {
			size = strings.TrimSpace(*req.CargoSize)
		}
		if vol, ok := vehicleclasses.CargoVolumeL(size); ok {
			in.Load.CargoSize, in.Load.CargoVolumeL = &size, vol
		} else {
			fields["cargo_size"] = "invalid"
		}
		if req.PassengerCount != nil {
			fields["passenger_count"] = "not_allowed"
		}
	}
	if len(fields) > 0 {
		return previewInput{}, httpx.Validation(fields)
	}
	return in, nil
}

func (req previewRequest) validateRoute(maxPoints int) (previewInput, error) {
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
		return previewInput{}, e
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
		return previewInput{}, httpx.Validation(fields)
	}
	for i := 1; i < len(stops); i++ {
		if stops[i].Lat == stops[i-1].Lat && stops[i].Lon == stops[i-1].Lon {
			fields["stops."+strconv.Itoa(i)] = "duplicate"
		}
	}
	if len(fields) > 0 {
		return previewInput{}, httpx.Validation(fields)
	}
	return previewInput{Category: category, Stops: stops}, nil
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
