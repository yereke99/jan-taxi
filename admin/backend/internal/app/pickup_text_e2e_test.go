package app_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// TestTypedPickupTaxiAtClassPrice books taxis the way the passenger app does,
// through the real wiring: pickup A written by hand is sent with the pin's
// coordinates, and the order goes out at the chosen class's price from the
// preview — its recommendation, or for a manual destination (which has none)
// the first allowed fare. Both are accepted, the order is offered to the
// driver near A, and the offer names the typed pickup.
func TestTypedPickupTaxiAtClassPrice(t *testing.T) {
	e := newFlowEnv(t)
	ctx := context.Background()
	driver := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Online: true, Balance: 1000})
	const typed = "Мешіттің жанында, көк қақпа"
	pickup := map[string]any{"lat": 48.8264, "lon": 58.1442, "address": typed}

	for _, tc := range []struct {
		name        string
		destination map[string]any
	}{
		{"geocoded destination", embiPoints[1]},
		{"manual destination", map[string]any{"mode": "manual", "lat": nil, "lon": nil, "address": "Ескі мәдениет үйі"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := e.passenger()
			q := e.must(http.StatusOK, e.call(e.h, http.MethodPost, "/api/v1/routes/preview", map[string]any{
				"category": "taxi", "stops": []map[string]any{pickup, tc.destination},
			}, p.auth), "route preview")
			option := q.body["options"].([]any)[0].(map[string]any)
			fare := option["fare"].(map[string]any)
			price := fare["recommended"]
			if price == nil {
				price = fare["options"].([]any)[0]
			}

			o := e.must(http.StatusCreated, e.call(e.h, http.MethodPost, "/api/v1/orders", map[string]any{
				"quote_id": option["quote_id"], "fare": price, "payment_method": "cash", "idempotency_key": uuid.NewString(),
			}, p.auth), "create order")
			a := o.body["stops"].([]any)[0].(map[string]any)
			if o.body["fare"] != price || a["address"] != typed || a["lat"] != pickup["lat"] || a["lon"] != pickup["lon"] {
				t.Fatalf("order: %s", o.raw)
			}

			var params map[string]any
			for _, row := range e.outbox() {
				if row.template == notifications.TplOrderAvailableDriver && row.userID == driver.UserID && row.params["order_id"] == o.str("id") {
					params = row.params
				}
			}
			if params == nil {
				t.Fatal("the driver near A got no offer")
			}
			msg, err := notifications.RenderFor(ctx, e.pool, notifications.TplOrderAvailableDriver, "kk", params, "https://app.jantaxi.test/driver/")
			if err != nil || !strings.Contains(msg.Text, typed) {
				t.Fatalf("offer without the typed pickup (%v):\n%s", err, msg.Text)
			}
		})
	}
}
