package drivers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

var (
	embiPoint    = map[string]any{"lat": 48.8264, "lon": 58.1442}
	almatyPoint  = map[string]any{"lat": 43.2389, "lon": 76.8897}
	orenburgSpot = map[string]any{"lat": 51.7682, "lon": 55.0970}
)

// registerAt saves a first draft with the device location loc (nil: none)
// and returns the driver code and city code.
func registerAt(t *testing.T, c *driverstest.Client, loc map[string]any) (string, string) {
	t.Helper()
	body := registrationBody("Ерлан", "Бекенов", "", "", "", "")
	if loc != nil {
		body["location"] = loc
	}
	res := c.Do(http.MethodPut, pathRegistration, body)
	if res.Code != http.StatusOK {
		t.Fatalf("save at %v: %s", loc, res)
	}
	code, _ := res.Body["code"].(string)
	city, _ := res.Body["city"].(map[string]any)
	cityCode, _ := city["code"].(string)
	return code, cityCode
}

func TestRegistrationCityFromLocation(t *testing.T) {
	env := driverstest.New(t)

	// Outside every configured city the nationwide region numbers the driver.
	if code, city := registerAt(t, env.DriverClient(), almatyPoint); code != "KAZ001" || city != "KAZ" {
		t.Fatalf("almaty driver: %s in %s", code, city)
	}
	if code, city := registerAt(t, env.DriverClient(), almatyPoint); code != "KAZ002" || city != "KAZ" {
		t.Fatalf("second almaty driver: %s in %s", code, city)
	}
	// Inside Embi's zone: Embi.
	embiDriver := env.DriverClient()
	if code, city := registerAt(t, embiDriver, embiPoint); code != "EMB001" || city != "EMB" {
		t.Fatalf("embi driver: %s in %s", code, city)
	}
	// Unknown or foreign location: the configured default city.
	if code, city := registerAt(t, env.DriverClient(), nil); code != "EMB002" || city != "EMB" {
		t.Fatalf("driver without location: %s in %s", code, city)
	}
	if code, city := registerAt(t, env.DriverClient(), orenburgSpot); code != "EMB003" || city != "EMB" {
		t.Fatalf("driver outside Kazakhstan: %s in %s", code, city)
	}
	// Existing drivers keep their city and code wherever they are.
	if code, city := registerAt(t, embiDriver, almatyPoint); code != "EMB001" || city != "EMB" {
		t.Fatalf("existing driver moved: %s in %s", code, city)
	}

	// A configured Almaty zone wins over the nationwide region.
	ctx := context.Background()
	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO cities (code, name_kk, name_ru, name_en, center_lat, center_lon, timezone)
		VALUES ('ALA', 'Алматы', 'Алматы', 'Almaty', 43.2389, 76.8897, 'Asia/Almaty');
		INSERT INTO service_zones (city_id, name, geom)
		SELECT id, 'Алматы', ST_Multi(ST_Buffer(center, 25000)::geometry) FROM cities WHERE code = 'ALA'`); err != nil {
		t.Fatal(err)
	}
	if code, city := registerAt(t, env.DriverClient(), almatyPoint); code != "ALA001" || city != "ALA" {
		t.Fatalf("driver in a configured Almaty: %s in %s", code, city)
	}
	c, err := drivers.RegistrationCity(ctx, env.Pool, "EMB", &geo.Point{Lat: 47.10, Lon: 51.92})
	if err != nil || c.Code != "KAZ" {
		t.Fatalf("RegistrationCity(Atyrau region) = %s %v", c.Code, err)
	}
	if c, err := drivers.RegistrationCity(ctx, env.Pool, "EMB", nil); err != nil || c.Code != "EMB" {
		t.Fatalf("RegistrationCity(nil) = %s %v", c.Code, err)
	}

	// A malformed location is rejected before anything is stored.
	bad := env.DriverClient()
	for _, loc := range []map[string]any{{"lat": 91.0, "lon": 10.0}, {"lat": 43.2}, {}} {
		body := registrationBody("Ерлан", "Бекенов", "", "", "", "")
		body["location"] = loc
		if res := bad.Do(http.MethodPut, pathRegistration, body); res.Code != http.StatusUnprocessableEntity ||
			res.Fields()["location"] != "invalid" {
			t.Fatalf("location %v: %s", loc, res)
		}
	}
	if env.Count(`SELECT count(*) FROM driver_profiles WHERE user_id = $1`, bad.UserID) != 0 {
		t.Fatal("invalid location created a profile")
	}
}
