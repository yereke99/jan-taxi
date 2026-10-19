package drivers_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
)

func classBody(vehicle map[string]any) map[string]any {
	v := map[string]any{"make": "Toyota", "model": "Alphard", "color": "Қара", "plate": "123ABC04"}
	for k, x := range vehicle {
		v[k] = x
	}
	return map[string]any{"first_name": "Ерлан", "last_name": "Бекенов", "vehicle": v}
}

func vehicleOf(r driverstest.Response) map[string]any {
	v, _ := r.Body["vehicle"].(map[string]any)
	return v
}

func missing(r driverstest.Response) []string {
	var out []string
	for _, f := range r.Body["missing_fields"].([]any) {
		out = append(out, f.(string))
	}
	return out
}

func TestRegistrationVehicleClass(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()

	// Without a class the registration is incomplete and every class field is null.
	res := c.Do(http.MethodPut, pathRegistration, classBody(nil))
	v := vehicleOf(res)
	if res.Code != http.StatusOK || !slices.Contains(missing(res), "vehicle_class") || v["vehicle_class"] != nil ||
		v["seats"] != nil || v["cargo_volume_l"] != nil || v["vehicle_class_name"] != nil {
		t.Fatalf("no class yet: %s", res)
	}

	// Choosing a class fills in its seat and cargo defaults.
	res = c.Do(http.MethodPut, pathRegistration, classBody(map[string]any{"vehicle_class": "minivan"}))
	v = vehicleOf(res)
	if res.Code != http.StatusOK || v["vehicle_class"] != "minivan" || v["seats"] != float64(7) || v["cargo_volume_l"] != float64(800) ||
		v["vehicle_class_name"].(map[string]any)["kk"] != "Минивэн" || slices.Contains(missing(res), "vehicle_class") {
		t.Fatalf("minivan defaults: %s", res)
	}
	// A request without class fields keeps them.
	res = c.Do(http.MethodPut, pathRegistration, classBody(map[string]any{"color": "Ақ"}))
	if v = vehicleOf(res); res.Code != http.StatusOK || v["vehicle_class"] != "minivan" || v["seats"] != float64(7) {
		t.Fatalf("class kept: %s", res)
	}
	// Seats up to the class maximum, explicit cargo volume.
	res = c.Do(http.MethodPut, pathRegistration, classBody(map[string]any{"seats": 8, "cargo_volume_l": 650}))
	if v = vehicleOf(res); res.Code != http.StatusOK || v["seats"] != float64(8) || v["cargo_volume_l"] != float64(650) {
		t.Fatalf("seats and cargo: %s", res)
	}
	for _, tt := range []struct {
		name    string
		vehicle map[string]any
		field   string
		reason  string
	}{
		{"seats above the class maximum", map[string]any{"seats": 9}, "vehicle.seats", "too_large"},
		{"seats above a new class maximum", map[string]any{"vehicle_class": "sedan", "seats": 5}, "vehicle.seats", "too_large"},
		{"no seats", map[string]any{"seats": 0}, "vehicle.seats", "too_small"},
		{"negative cargo", map[string]any{"cargo_volume_l": -1}, "vehicle.cargo_volume_l", "too_small"},
		{"unknown class", map[string]any{"vehicle_class": "rocket"}, "vehicle.vehicle_class", "not_found"},
		{"malformed class", map[string]any{"vehicle_class": "Sedan!"}, "vehicle.vehicle_class", "invalid"},
	} {
		r := c.Do(http.MethodPut, pathRegistration, classBody(tt.vehicle))
		if r.Code != http.StatusUnprocessableEntity || r.Fields()[tt.field] != tt.reason {
			t.Errorf("%s: %s", tt.name, r)
		}
	}
	// A deactivated class cannot be chosen.
	if _, err := env.Pool.Exec(context.Background(), `UPDATE vehicle_classes SET is_active = false WHERE code = 'truck'`); err != nil {
		t.Fatal(err)
	}
	if r := c.Do(http.MethodPut, pathRegistration, classBody(map[string]any{"vehicle_class": "truck"})); r.Code != 422 ||
		r.Fields()["vehicle.vehicle_class"] != "not_allowed" {
		t.Fatalf("inactive class: %s", r)
	}
	// Switching class resets the seats and cargo to the new defaults.
	res = c.Do(http.MethodPut, pathRegistration, classBody(map[string]any{"vehicle_class": "universal"}))
	if v = vehicleOf(res); res.Code != http.StatusOK || v["vehicle_class"] != "universal" || v["seats"] != float64(4) ||
		v["cargo_volume_l"] != float64(600) {
		t.Fatalf("switch class: %s", res)
	}
	if env.Count(`SELECT count(*) FROM vehicles v JOIN driver_profiles d ON d.id = v.driver_id
		WHERE d.user_id = $1 AND v.class_code = 'universal' AND v.seats = 4 AND v.cargo_volume_l = 600`, c.UserID) != 1 {
		t.Fatal("class not stored")
	}
}

func TestVehicleClassThroughModeration(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	driverID := c.CompleteDraft(driverstest.Registration{Plate: "555 ABC 04", Phone: "+77015550199",
		VehicleClass: "microbus", Seats: 18})
	if res := c.Submit(); res.Code != http.StatusOK {
		t.Fatalf("submit: %s", res)
	}
	admin := env.AdminClient(auth.RoleSuperAdmin)

	// The administrator sees class, seats and volume, and the snapshot keeps them.
	res := admin.Do(http.MethodGet, "/api/v1/admin/drivers/"+driverID.String(), nil)
	v := vehicleOf(res)
	if res.Code != http.StatusOK || v["vehicle_class"] != "microbus" || v["seats"] != float64(18) ||
		v["cargo_volume_l"] != float64(1500) || v["vehicle_class_name"].(map[string]any)["ru"] != "Микроавтобус" {
		t.Fatalf("admin detail: %s", res)
	}
	snap := res.Body["applications"].([]any)[0].(map[string]any)["snapshot"].(map[string]any)["vehicle"].(map[string]any)
	if snap["vehicle_class"] != "microbus" || snap["seats"] != float64(18) {
		t.Fatalf("snapshot vehicle: %v", snap)
	}
	list := admin.Do(http.MethodGet, "/api/v1/admin/drivers?q=555ABC04", nil)
	if item := list.Body["items"].([]any)[0].(map[string]any)["vehicle"].(map[string]any); item["vehicle_class"] != "microbus" ||
		item["seats"] != float64(18) {
		t.Fatalf("admin list vehicle: %s", list)
	}

	// vehicle_class is a correction field: unflagged it is locked, flagged it is editable.
	if r := admin.Do(http.MethodPost, "/api/v1/admin/drivers/"+driverID.String()+"/request-corrections", map[string]any{
		"items": []map[string]any{{"field": "vehicle_color", "reason": "missing_info"}}}); r.Code != http.StatusOK {
		t.Fatalf("request corrections: %s", r)
	}
	body := classBody(map[string]any{"make": "Toyota", "model": "Camry", "color": "Сұр", "plate": "555 ABC 04", "seats": 12})
	if r := c.Do(http.MethodPut, pathRegistration, body); r.Code != 422 || r.Fields()["vehicle.seats"] != "not_editable" {
		t.Fatalf("seats changed without a correction: %s", r)
	}
	delete(body["vehicle"].(map[string]any), "seats")
	if r := c.Do(http.MethodPut, pathRegistration, body); r.Code != http.StatusOK || vehicleOf(r)["seats"] != float64(18) {
		t.Fatalf("unchanged class fields pass: %s", r)
	}
	if r := c.Submit(); r.Code != http.StatusOK {
		t.Fatalf("resubmit: %s", r)
	}
	if r := admin.Do(http.MethodPost, "/api/v1/admin/drivers/"+driverID.String()+"/request-corrections", map[string]any{
		"items": []map[string]any{{"field": "vehicle_class", "reason": "other", "comment": "Орын санын тексеріңіз"}}}); r.Code != http.StatusOK {
		t.Fatalf("request class correction: %s", r)
	}
	reg := c.Do(http.MethodGet, pathRegistration, nil)
	if reg.Body["corrections"].([]any)[0].(map[string]any)["field"] != "vehicle_class" {
		t.Fatalf("driver sees the class correction: %s", reg)
	}
	body["vehicle"].(map[string]any)["vehicle_class"] = "minivan"
	if r := c.Do(http.MethodPut, pathRegistration, body); r.Code != http.StatusOK || vehicleOf(r)["vehicle_class"] != "minivan" ||
		vehicleOf(r)["seats"] != float64(7) {
		t.Fatalf("fix class: %s", r)
	}
	if r := c.Submit(); r.Code != http.StatusOK {
		t.Fatalf("resubmit: %s", r)
	}
	if r := admin.Do(http.MethodPost, "/api/v1/admin/drivers/"+driverID.String()+"/approve", map[string]any{}); r.Code != http.StatusOK {
		t.Fatalf("approve: %s", r)
	}
	profile := c.Do(http.MethodGet, "/api/v1/driver/profile", nil)
	if v := vehicleOf(profile); profile.Code != http.StatusOK || v["vehicle_class"] != "minivan" || v["seats"] != float64(7) ||
		v["cargo_volume_l"] != float64(800) {
		t.Fatalf("driver profile vehicle: %s", profile)
	}
}
