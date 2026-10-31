package routing

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
)

// typedPickup is how the passenger app sends pickup A written by hand: an
// ordinary stop with the pin's (or GPS) coordinates and the passenger's text,
// cleaned like every stop address.
const typedPickup = "Мешіттің жанында, көк қақпа"

func typedPickupJSON(lat, lon float64) string {
	return fmt.Sprintf(`{"lat":%v,"lon":%v,"address":"  Мешіттің жанында,\n көк  қақпа "}`, lat, lon)
}

// storedQuote reads a quote back the way POST /orders does.
func storedQuote(t *testing.T, env *testEnv, quoteID any, userID uuid.UUID) pricing.StoredQuote {
	t.Helper()
	ctx := context.Background()
	tx, err := env.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := uuid.Parse(fmt.Sprint(quoteID))
	if err != nil {
		t.Fatalf("quote id %v: %v", quoteID, err)
	}
	q, err := pricing.LoadQuoteForUpdate(ctx, tx, id, userID)
	if err != nil {
		t.Fatalf("load quote: %v", err)
	}
	return q
}

// Pickup A in the passenger's own words keeps A's coordinates: the route and
// the fare come from them as for any address, and the quote (hence the order
// and every driver) carries the typed text as the pickup address.
func TestPreviewTypedPickupWithGeocodedDestination(t *testing.T) {
	env := newTestEnv(t, nil)
	token, userID := env.signIn("passenger")
	status, q := env.do(http.MethodPost, "/api/v1/routes/preview", token,
		previewBody("taxi", typedPickupJSON(embi.Lat, embi.Lon), stopJSON(embiStation, "Теміржол вокзалы")))
	if status != http.StatusOK {
		t.Fatalf("preview: %d %v", status, q)
	}
	if calls := env.osrm.callLog(); len(calls) != 1 || calls[0].coords != coordString(embi, embiStation) {
		t.Fatalf("OSRM calls = %+v", calls)
	}
	a := q["stops"].([]any)[0].(map[string]any)
	if a["mode"] != "geocoded" || a["kind"] != "pickup" || a["address"] != typedPickup || a["lat"] != embi.Lat || a["lon"] != embi.Lon {
		t.Fatalf("pickup = %v", a)
	}
	fare := q["fare"].(map[string]any)
	if _, manual := q["pricing_mode"]; manual || q["distance_m"] == nil || fare["recommended"] == nil {
		t.Fatalf("a typed pickup must not turn the route into a manual one: %v", q)
	}
	sq := storedQuote(t, env, q["quote_id"], userID)
	if sq.Stops[0].Mode != "geocoded" || sq.Stops[0].Address != typedPickup || sq.Stops[0].Lat != embi.Lat || sq.Stops[0].Lon != embi.Lon {
		t.Fatalf("stored pickup = %+v", sq.Stops[0])
	}
	if err := sq.ValidateFare(int(fare["recommended"].(float64))); err != nil {
		t.Fatalf("recommended fare refused: %v", err)
	}
}

// With a manual destination too, only B goes without coordinates: no route is
// invented, the fare range is the administrator's tariff, and A keeps its
// point and the typed text. The first allowed fare (the price the passenger
// app shows for the class and sends for a taxi) is accepted.
func TestPreviewTypedPickupWithManualDestination(t *testing.T) {
	env := newTestEnv(t, nil)
	token, userID := env.signIn("passenger")
	body := previewBody("taxi", typedPickupJSON(embi.Lat, embi.Lon), `{"mode":"manual","lat":null,"lon":null,"address":"Ескі мәдениет үйі"}`)
	status, q := env.do(http.MethodPost, "/api/v1/routes/preview", token, body)
	if status != http.StatusOK {
		t.Fatalf("preview: %d %v", status, q)
	}
	if len(env.osrm.callLog()) != 0 || q["distance_m"] != nil || q["pricing_mode"] != "passenger_tariff_bounds" {
		t.Fatalf("manual route: %v", q)
	}
	stops := q["stops"].([]any)
	a, b := stops[0].(map[string]any), stops[1].(map[string]any)
	if a["mode"] != "geocoded" || a["address"] != typedPickup || a["lat"] != embi.Lat || a["lon"] != embi.Lon {
		t.Fatalf("pickup = %v", a)
	}
	if b["mode"] != "manual" || b["lat"] != nil || b["lon"] != nil {
		t.Fatalf("destination = %v", b)
	}
	for _, o := range q["options"].([]any) {
		opt := o.(map[string]any)
		fare := opt["fare"].(map[string]any)
		price := int(fare["options"].([]any)[0].(float64))
		if fare["recommended"] != nil || price != int(fare["min"].(float64)) {
			t.Fatalf("%s fare = %v", opt["vehicle_class"], fare)
		}
		sq := storedQuote(t, env, opt["quote_id"], userID)
		if sq.Stops[0].Address != typedPickup || sq.Stops[0].Lat != embi.Lat || sq.Stops[1].Mode != "manual" {
			t.Fatalf("%s stored stops = %+v", opt["vehicle_class"], sq.Stops)
		}
		if err := sq.ValidateFare(price); err != nil {
			t.Fatalf("%s: the class price %d is refused: %v", opt["vehicle_class"], price, err)
		}
	}
}

// The typed text never replaces A's point: A still needs valid coordinates
// inside Kazakhstan, and the text follows the address rules of every stop.
func TestPreviewTypedPickupValidation(t *testing.T) {
	env := newTestEnv(t, nil)
	token, _ := env.signIn("passenger")
	b := stopJSON(embiStation, "Теміржол вокзалы")
	for _, tc := range []struct {
		name, body, code, field, reason string
		index                           any
	}{
		{"outside Kazakhstan", previewBody("taxi", typedPickupJSON(orenburg.Lat, orenburg.Lon), b), "outside_service_area", "", "", float64(0)},
		{"no coordinates", previewBody("taxi", `{"address":"`+typedPickup+`"}`, b), "validation_failed", "stops.0.lat", "required", nil},
		{"manual without coordinates", previewBody("taxi", `{"mode":"manual","address":"`+typedPickup+`"}`, b), "validation_failed", "stops.0.mode", "pickup_coordinates_required", nil},
		{"too long", previewBody("taxi", stopJSON(embi, strings.Repeat("ә", 201)), b), "validation_failed", "stops.0.address", "too_long", nil},
	} {
		status, out := env.do(http.MethodPost, "/api/v1/routes/preview", token, tc.body)
		code, fields, details := errInfo(out)
		if status != http.StatusUnprocessableEntity || code != tc.code {
			t.Fatalf("%s: %d %v", tc.name, status, out)
		}
		if tc.field != "" && fields[tc.field] != tc.reason {
			t.Fatalf("%s: fields = %v", tc.name, fields)
		}
		if tc.index != nil && details["index"] != tc.index {
			t.Fatalf("%s: details = %v", tc.name, details)
		}
	}
	if n := len(env.osrm.callLog()); n != 0 {
		t.Fatalf("refused previews reached OSRM %d times", n)
	}
}
