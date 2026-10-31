package orders_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
)

// Pickup A written by the passenger (a place the map search does not know)
// is an ordinary stop at the pin's or GPS coordinates with the typed text as
// its address. The order is dispatched by radius from those coordinates, and
// the driver bot offer, the driver Mini App and the admin panel all show the
// typed text as the pickup.
func TestTypedPickupTextReachesDrivers(t *testing.T) {
	const typed = "Мешіттің жанында, көк қақпа"
	pickup := pricing.QuoteStop{Lat: 48.8264, Lon: 58.1442, Address: typed}
	for _, tc := range []struct {
		name  string
		stops []pricing.QuoteStop
	}{
		{"geocoded destination", []pricing.QuoteStop{pickup, embiStops[1]}},
		{"manual destination", []pricing.QuoteStop{pickup, {Mode: "manual", Address: "Ескі мәдениет үйі"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			near := e.driverAt("EMB", 1000, 48.827, 58.1442)
			far := e.driverAt("EMB", 1000, 49.1, 58.1442)
			pid, pc := e.passenger()
			q := typedPickupQuote(t, e, pid, tc.stops)

			// A taxi is ordered at the class's price: the quote's first allowed fare.
			res := e.do(pc, http.MethodPost, "/orders", taxiBody(q, q.MinFare))
			e.expect(res, http.StatusCreated, "")
			var o orders.Order
			res.decode(t, &o)
			if o.Fare != int64(q.MinFare) || o.Stops[0].Address != typed || o.Stops[0].Lat != pickup.Lat || o.Stops[0].Lon != pickup.Lon {
				t.Fatalf("passenger order: fare %d, pickup %+v", o.Fare, o.Stops[0])
			}
			if e.count(`SELECT count(*) FROM order_stops WHERE order_id = $1 AND seq = 0 AND mode = 'geocoded'
				AND lat = 48.8264 AND lon = 58.1442 AND address = $2`, o.ID, typed) != 1 {
				t.Fatal("pickup not stored with its coordinates and the typed text")
			}
			if e.count(`SELECT count(*) FROM orders WHERE id = $1 AND pickup_lat = 48.8264 AND pickup_lon = 58.1442`, o.ID) != 1 {
				t.Fatal("order pickup point lost")
			}

			// Dispatch measures from A's coordinates, not from any text.
			if e.offers(o.ID, near.DriverID) != 1 || e.offers(o.ID, far.DriverID) != 0 {
				t.Fatalf("offers: near %d, far %d", e.offers(o.ID, near.DriverID), e.offers(o.ID, far.DriverID))
			}

			// The driver bot offer names the typed place as the pickup.
			params := offerParams(t, e, o.ID, near.DriverID)
			for _, lang := range []string{"kk", "ru", "en"} {
				msg, err := notifications.RenderFor(ctx, e.pool, notifications.TplOrderAvailableDriver, lang, params, "https://app.jantaxi.test/driver/")
				if err != nil {
					t.Fatalf("render %s: %v", lang, err)
				}
				if !strings.Contains(msg.Text, typed) {
					t.Fatalf("%s offer without the typed pickup:\n%s", lang, msg.Text)
				}
			}

			// The driver Mini App: the available-order card and the order detail.
			listed := false
			for _, c := range e.available(near).Items {
				if c.ID != o.ID {
					continue
				}
				listed = true
				if len(c.Stops) == 0 || c.Stops[0].Address != typed || c.Stops[0].Lat != pickup.Lat || c.Stops[0].Lon != pickup.Lon {
					t.Fatalf("driver card pickup: %+v", c.Stops)
				}
			}
			if !listed {
				t.Fatal("order missing from the near driver's list")
			}
			for _, view := range []response{
				e.do(near.c, http.MethodGet, path("/driver/orders/%s", o.ID), nil),
				e.do(e.operator(), http.MethodGet, path("/admin/orders/%s", o.ID), nil),
			} {
				e.expect(view, http.StatusOK, "")
				var body struct {
					Stops []struct {
						Kind    string   `json:"kind"`
						Mode    string   `json:"mode"`
						Address string   `json:"address"`
						Lat     *float64 `json:"lat"`
						Lon     *float64 `json:"lon"`
					} `json:"stops"`
				}
				view.decode(t, &body)
				a := body.Stops[0]
				if a.Kind != "pickup" || a.Mode != "geocoded" || a.Address != typed || a.Lat == nil || *a.Lat != pickup.Lat || a.Lon == nil || *a.Lon != pickup.Lon {
					t.Fatalf("pickup view: %+v (%s)", a, view.Body)
				}
			}
		})
	}
}

// typedPickupQuote stores the quote POST /routes/preview gives for stops: a
// priced route for geocoded stops, the administrator's tariff range when a
// stop after A is manual.
func typedPickupQuote(t *testing.T, e *env, userID uuid.UUID, stops []pricing.QuoteStop) pricing.StoredQuote {
	t.Helper()
	if !pricing.Manual(stops) {
		return e.quote(userID, "taxi", stops)
	}
	tr, err := pricing.ActiveTariff(context.Background(), e.pool, e.cityID, "taxi", "sedan")
	if err != nil {
		t.Fatal(err)
	}
	q, err := pricing.CreateQuote(context.Background(), e.pool, pricing.NewQuote{UserID: userID, CityID: e.cityID, Tariff: tr,
		Stops: stops, Geometry: json.RawMessage(`null`),
		Fare: pricing.Fare{Recommended: tr.MinFare, Min: tr.MinFare, Max: tr.MaxFare, Step: tr.FareStep, Options: pricing.Options(tr.MinFare, tr.MaxFare, tr.FareStep)}})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// offerParams returns the params of the offer queued for the driver, decoded
// as the notification worker decodes them.
func offerParams(t *testing.T, e *env, orderID, driverID uuid.UUID) map[string]any {
	t.Helper()
	var raw []byte
	if err := e.pool.QueryRow(context.Background(), `
		SELECT params FROM notification_outbox WHERE template = $1 AND dedupe_key = $2`,
		notifications.TplOrderAvailableDriver, "order_available:"+orderID.String()+":"+driverID.String()).Scan(&raw); err != nil {
		t.Fatalf("offer notification: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var params map[string]any
	if err := dec.Decode(&params); err != nil {
		t.Fatal(err)
	}
	return params
}
