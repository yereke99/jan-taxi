package routing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

func TestManualDestinationValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid final", `{"category":"taxi","stops":[{"lat":48.8,"lon":58.1,"address":"A"},{"mode":"manual","lat":null,"lon":null,"address":"Ескі мәдениет үйі"}]}`, true},
		{"missing pickup", `{"category":"taxi","stops":[{"address":"A"},{"mode":"manual","address":"B"}]}`, false},
		{"manual pickup", `{"category":"taxi","stops":[{"mode":"manual","address":"A"},{"lat":48.8,"lon":58.1,"address":"B"}]}`, false},
		{"manual intermediate", `{"category":"taxi","stops":[{"lat":48.8,"lon":58.1,"address":"A"},{"mode":"manual","address":"via"},{"mode":"manual","address":"B"}]}`, true},
		{"mixed manual coords", `{"category":"taxi","stops":[{"lat":48.8,"lon":58.1,"address":"A"},{"mode":"manual","lat":48.9,"address":"B"}]}`, false},
		{"empty manual", `{"category":"taxi","stops":[{"lat":48.8,"lon":58.1,"address":"A"},{"mode":"manual","address":"  "}]}`, false},
		{"known via", `{"category":"taxi","stops":[{"lat":48.8,"lon":58.1,"address":"A"},{"lat":48.9,"lon":58.1,"address":"via"},{"mode":"manual","address":"B"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req previewRequest
			if e := json.Unmarshal([]byte(tc.body), &req); e != nil {
				t.Fatal(e)
			}
			_, e := req.validate(5)
			if (e == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, e)
			}
		})
	}
}

// The passenger's own words for pickup A keep A's coordinates and are marked
// typed; the mark means nothing on a manual stop (always the passenger's words).
func TestTypedAddressValidation(t *testing.T) {
	var req previewRequest
	body := `{"category":"taxi","stops":[{"lat":48.8,"lon":58.1,"address":"Мешіттің жанында","address_typed":true},
		{"mode":"manual","address":"Ескі мәдениет үйі","address_typed":true},{"lat":48.9,"lon":58.1,"address":"Вокзал"}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	in, err := req.validate(5)
	if err != nil {
		t.Fatal(err)
	}
	if !in.Stops[0].AddressTyped || in.Stops[1].AddressTyped || in.Stops[2].AddressTyped {
		t.Fatalf("typed marks: %+v", in.Stops)
	}
}

// A service-area error names the stop's position in the request: manual stops
// have no point and are skipped by the check, so its index is mapped back.
func TestStopIndexErrorMapsToRequestPosition(t *testing.T) {
	ptStop := []int{0, 2, 3} // stop 1 is manual
	err := stopIndexError(geo.OutsideServiceArea("stops", 1), ptStop)
	var he *httpx.Error
	if !errors.As(err, &he) || he.Code != "outside_service_area" || he.Details["index"] != 2 || he.Details["field"] != "stops" {
		t.Fatalf("mapped error: %+v", err)
	}
	if err := stopIndexError(geo.OutsideServiceArea("stops", 0), ptStop); !errors.As(err, &he) || he.Details["index"] != 0 {
		t.Fatalf("unchanged index: %+v", err)
	}
}

func TestManualPreviewUsesAdminTariffAndNoInventedRoute(t *testing.T) {
	e := newTestEnv(t, nil)
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO app_settings(key,value) VALUES ('orders.manual_destination_enabled','false')`); err != nil {
		t.Fatal(err)
	}
	token, _ := e.signIn("passenger")
	body := `{"category":"taxi","stops":[{"lat":48.8264,"lon":58.1442,"address":"A"},{"mode":"manual","lat":null,"lon":null,"address":"Ескі мәдениет үйі"}]}`
	status, _ := e.do("POST", "/api/v1/routes/preview", token, body)
	if status != 422 {
		t.Fatalf("disabled manual preview status=%d", status)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO app_settings(key,value) VALUES ('orders.manual_destination_enabled','true'),('orders.manual_destination_pricing','"passenger_tariff_bounds"') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE tariffs SET min_fare=650,max_fare=1250,fare_step=50 WHERE vehicle_class='sedan' AND is_active`); err != nil {
		t.Fatal(err)
	}
	status, q := e.do("POST", "/api/v1/routes/preview", token, body)
	if status != 200 {
		t.Fatalf("manual preview=%d %v", status, q)
	}
	if len(e.osrm.callLog()) != 0 {
		t.Fatal("manual B sent to OSRM")
	}
	if q["distance_m"] != nil || q["duration_s"] != nil || q["geometry"] != nil || q["bbox"] != nil {
		t.Fatalf("fabricated route: %v", q)
	}
	f := q["fare"].(map[string]any)
	if f["min"] != float64(650) || f["max"] != float64(1250) || f["step"] != float64(50) || f["recommended"] != nil {
		t.Fatalf("administrator tariff not honored: %v", f)
	}
	stop := q["stops"].([]any)[1].(map[string]any)
	if stop["lat"] != nil || stop["lon"] != nil {
		t.Fatalf("invented B: %v", stop)
	}
}

func TestFivePointManualPreviewPreservesKnownCoordinates(t *testing.T) {
	// The Mini App sends this localized label when GPS has no street name.
	for _, body := range []string{
		`{"category":"taxi","stops":[{"lat":48.8264,"lon":58.1442,"address":"Точка на карте"},{"mode":"manual","address":"У клуба"},{"lat":48.827,"lon":58.145,"address":"Школа"},{"mode":"manual","address":"У магазина"},{"lat":48.828,"lon":58.146,"address":"Дом"}]}`,
		`{"category":"taxi","stops":[{"lat":48.8264,"lon":58.1442,"address":"Точка на карте"},{"mode":"manual","address":"У клуба"},{"mode":"manual","address":"У школы"},{"mode":"manual","address":"У магазина"},{"mode":"manual","address":"Дом у озера"}]}`,
	} {
		e := newTestEnv(t, nil)
		token, _ := e.signIn("passenger")
		status, q := e.do("POST", "/api/v1/routes/preview", token, body)
		if status != 200 {
			t.Fatalf("five-point preview=%d %v", status, q)
		}
		if len(e.osrm.callLog()) != 0 || q["distance_m"] != nil || q["duration_s"] != nil {
			t.Fatal("manual route sent to OSRM or given invented metrics")
		}
		var input struct{ Stops []map[string]any }
		if err := json.Unmarshal([]byte(body), &input); err != nil {
			t.Fatal(err)
		}
		out := q["stops"].([]any)
		if len(out) != 5 {
			t.Fatal("route stops lost")
		}
		for i, original := range input.Stops {
			stop := out[i].(map[string]any)
			if stop["address"] != original["address"] || stop["lat"] != original["lat"] || stop["lon"] != original["lon"] {
				t.Fatalf("stop %d changed text or coordinates", i)
			}
		}
	}
}
