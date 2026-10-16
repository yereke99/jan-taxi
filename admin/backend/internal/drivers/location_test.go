package drivers_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

const pathLocation = "/api/v1/driver/location"

func TestLocationReports(t *testing.T) {
	env := driverstest.New(t)
	ctx := context.Background()
	d := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true, Balance: 100})
	c := env.AppClient(d.UserID, auth.AppDriver)

	// Moscow is outside Kazakhstan.
	if res := c.Do(http.MethodPost, pathLocation, `{"lat":55.7558,"lon":37.6173}`); res.Code != http.StatusUnprocessableEntity ||
		res.ErrorCode() != "outside_service_area" {
		t.Fatalf("outside KZ: %s", res)
	}
	future := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	invalid := []struct{ body, field string }{
		{`{"lon":58.1}`, "lat"},
		{`{"lat":91,"lon":58.1}`, "lat"},
		{`{"lat":48.8,"lon":58.1,"heading":400}`, "heading"},
		{`{"lat":48.8,"lon":58.1,"accuracy_m":-1}`, "accuracy_m"},
		{`{"lat":48.8,"lon":58.1,"recorded_at":"` + future + `"}`, "recorded_at"},
		{`{"lat":48.8,"lon":58.1,"recorded_at":"yesterday"}`, "recorded_at"},
	}
	for _, tt := range invalid {
		// A fresh driver per case keeps the per-driver rate limit out of the way.
		other := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true})
		res := env.AppClient(other.UserID, auth.AppDriver).Do(http.MethodPost, pathLocation, tt.body)
		if res.Code != http.StatusUnprocessableEntity || res.Fields()[tt.field] == nil {
			t.Fatalf("%s: %s", tt.body, res)
		}
	}

	if res := c.Do(http.MethodPost, pathLocation, `{"lat":48.8301,"lon":58.1501,"accuracy_m":8,"heading":90,"speed_mps":5}`); res.Code != http.StatusNoContent {
		t.Fatalf("report: %s", res)
	}
	var lat float64
	var source string
	if err := env.Pool.QueryRow(ctx, `SELECT lat, source FROM driver_locations WHERE driver_id = $1`, d.DriverID).Scan(&lat, &source); err != nil {
		t.Fatal(err)
	}
	if lat != 48.8301 || source != "miniapp" {
		t.Fatalf("stored %v %s", lat, source)
	}
	// An older fix than the stored one is ignored.
	old := drivers.LocationInput{Lat: 48.9, Lon: 58.2, RecordedAt: time.Now().Add(-time.Hour)}
	if err := drivers.RecordLocation(ctx, env.Pool, d.DriverID, old, drivers.SourceBotLive); err != nil {
		t.Fatal(err)
	}
	if env.Count(`SELECT count(*) FROM driver_locations WHERE driver_id = $1 AND lat = 48.8301`, d.DriverID) != 1 {
		t.Fatal("older fix overwrote the newer one")
	}
	// No trip track without an active order; with one, every fix is kept.
	if env.Count(`SELECT count(*) FROM driver_location_history WHERE driver_id = $1`, d.DriverID) != 0 {
		t.Fatal("history written without an active order")
	}
	passenger, _ := testutil.CreateUser(t, env.Pool)
	order := env.CreateOrder(passenger, d.DriverID, "accepted")
	if err := drivers.RecordLocation(ctx, env.Pool, d.DriverID, drivers.LocationInput{Lat: 48.831, Lon: 58.151}, drivers.SourceBotLive); err != nil {
		t.Fatal(err)
	}
	if env.Count(`SELECT count(*) FROM driver_location_history WHERE driver_id = $1 AND order_id = $2`, d.DriverID, order) != 1 {
		t.Fatal("trip track not written")
	}

	// Only approved drivers report locations.
	pending := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Status: "pending"})
	if res := env.AppClient(pending.UserID, auth.AppDriver).Do(http.MethodPost, pathLocation, `{"lat":48.8,"lon":58.1}`); res.Code != http.StatusForbidden {
		t.Fatalf("pending driver location: %s", res)
	}

	// One report per 2 seconds (small burst allowed).
	limited := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true})
	lc := env.AppClient(limited.UserID, auth.AppDriver)
	codes := []int{}
	for range 5 {
		codes = append(codes, lc.Do(http.MethodPost, pathLocation, `{"lat":48.8,"lon":58.1}`).Code)
	}
	if codes[0] != http.StatusNoContent || codes[4] != http.StatusTooManyRequests {
		t.Fatalf("rate limit codes: %v", codes)
	}
}

func TestAdminLiveLocations(t *testing.T) {
	env := driverstest.New(t)
	ctx := context.Background()
	online := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true})
	offline := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{})
	for _, d := range []testutil.DriverFixture{online, offline} {
		if err := drivers.RecordLocation(ctx, env.Pool, d.DriverID, drivers.LocationInput{Lat: 48.83, Lon: 58.15}, drivers.SourceMiniApp); err != nil {
			t.Fatal(err)
		}
	}
	admin := env.AdminClient(auth.RoleAuditor)
	res := admin.Do(http.MethodGet, "/api/v1/admin/drivers/locations?city_id="+testutil.CityID(t, env.Pool, "EMB").String(), nil)
	items, _ := res.Body["items"].([]any)
	if res.Code != http.StatusOK || len(items) != 1 {
		t.Fatalf("locations: %s", res)
	}
	it := items[0].(map[string]any)
	if it["driver"].(map[string]any)["code"] != online.Code || it["location"].(map[string]any)["age_s"] == nil {
		t.Fatalf("location item: %v", it)
	}
	if res := admin.Do(http.MethodGet, "/api/v1/admin/drivers/locations?city_id=x", nil); res.Code != 422 {
		t.Fatalf("bad city: %s", res)
	}
}
