package drivers_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

const pathRegistration = "/api/v1/driver/registration"

func registrationBody(first, last, make_, model, color, plate string) map[string]any {
	return map[string]any{"first_name": first, "last_name": last,
		"vehicle": map[string]any{"make": make_, "model": model, "color": color, "plate": plate}}
}

func TestRegistrationValidation(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	cases := []struct {
		name   string
		body   any
		fields map[string]string
	}{
		{"missing everything", `{}`, map[string]string{"first_name": "required", "last_name": "required", "vehicle": "required"}},
		{"bad plate and name", registrationBody("Ерлан2", "Бекенов", "Toyota", "Camry", "Ақ", "ABC 123"),
			map[string]string{"first_name": "invalid", "vehicle.plate": "invalid"}},
		{"too long", registrationBody(strings.Repeat("ә", 61), "Б", strings.Repeat("x", 41), "M", strings.Repeat("c", 31), "123ABC04"),
			map[string]string{"first_name": "too_long", "vehicle.make": "too_long", "vehicle.color": "too_long"}},
		{"vehicle fields missing", `{"first_name":"А","last_name":"Б","vehicle":{}}`,
			map[string]string{"vehicle.make": "required", "vehicle.model": "required", "vehicle.color": "required", "vehicle.plate": "required"}},
	}
	for _, tt := range cases {
		res := c.Do(http.MethodPut, pathRegistration, tt.body)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %s", tt.name, res)
		}
		for k, v := range tt.fields {
			if res.Fields()[k] != v {
				t.Errorf("%s: %s = %v, want %s (%v)", tt.name, k, res.Fields()[k], v, res.Fields())
			}
		}
	}
	if res := c.Do(http.MethodPut, pathRegistration, `{"first_name":"А","last_name":"Б","vehicle":{},"telegram_id":1}`); res.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %s", res)
	}
	if env.Count(`SELECT count(*) FROM driver_profiles WHERE user_id = $1`, c.UserID) != 0 {
		t.Fatal("invalid input created a profile")
	}
	// Drafts may be partial; names keep Kazakh letters and hyphens.
	res := c.Do(http.MethodPut, pathRegistration, registrationBody("  Әлі-Ахмет ", "О'Нил", "", "", "", ""))
	if res.Code != http.StatusOK || res.Body["first_name"] != "Әлі-Ахмет" || res.Body["status"] != "draft" || res.Body["city"].(map[string]any)["code"] != "EMB" {
		t.Fatalf("partial draft: %s", res)
	}
	if env.Count(`SELECT count(*) FROM wallets w JOIN driver_profiles d ON d.id = w.driver_id WHERE d.user_id = $1`, c.UserID) != 1 {
		t.Fatal("wallet not created with the profile")
	}

	checks := []struct {
		body              string
		valid, available  any
		normalized, input any
	}{
		{`{}`, nil, nil, nil, nil},
		{`{"plate":"12"}`, false, nil, nil, "12"},
		{`{"plate":"045 кма 04"}`, true, true, "045KMA04", "045 кма 04"},
	}
	for _, tt := range checks {
		res := c.Do(http.MethodPost, pathRegistration+"/check", tt.body)
		p, _ := res.Body["plate"].(map[string]any)
		if res.Code != http.StatusOK || (tt.valid == nil) != (p == nil) {
			t.Fatalf("check %s: %s", tt.body, res)
		}
		if p != nil && (p["valid"] != tt.valid || p["available"] != tt.available || p["normalized"] != tt.normalized || p["input"] != tt.input) {
			t.Fatalf("check %s: %v", tt.body, p)
		}
	}
}

func TestDriverCodesAreSequentialUnderConcurrency(t *testing.T) {
	env := driverstest.New(t)
	const n = 8
	clients := make([]*driverstest.Client, n)
	for i := range clients {
		clients[i] = env.DriverClient()
	}
	codes := make([]string, n)
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := c.Do(http.MethodPut, pathRegistration, registrationBody("Ерлан", "Бекенов", "", "", "", ""))
			if res.Code == http.StatusOK {
				codes[i], _ = res.Body["code"].(string)
			}
		}()
	}
	wg.Wait()
	sort.Strings(codes)
	for i, code := range codes {
		if want := fmt.Sprintf("EMB%03d", i+1); code != want {
			t.Fatalf("codes = %v", codes)
		}
	}
	// A repeated first save by the same user does not create a second profile.
	res := clients[0].Do(http.MethodPut, pathRegistration, registrationBody("Ерлан", "Бекенов", "Kia", "", "", ""))
	if res.Code != http.StatusOK || env.Count(`SELECT count(*) FROM driver_profiles WHERE user_id = $1`, clients[0].UserID) != 1 {
		t.Fatalf("second save: %s", res)
	}
}

func TestUploads(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	if res := c.Upload("avatar", driverstest.JPEG(t, 20, 20, 1)); res.Code != http.StatusForbidden || res.ErrorCode() != "driver_not_registered" {
		t.Fatalf("upload before profile: %s", res)
	}
	if res := c.Do(http.MethodPut, pathRegistration, registrationBody("Ерлан", "Бекенов", "", "", "", "")); res.Code != http.StatusOK {
		t.Fatalf("save: %s", res)
	}
	res := c.Upload("avatar", driverstest.JPEG(t, 64, 48, 1))
	if res.Code != http.StatusCreated || res.Body["kind"] != "avatar" || res.Body["width"] != float64(64) ||
		res.Body["url"] != "/api/v1/driver/files/"+res.Body["id"].(string) {
		t.Fatalf("avatar: %s", res)
	}
	first := res.Body["id"].(string)
	res = c.Upload("avatar", driverstest.JPEG(t, 32, 32, 2))
	if res.Code != http.StatusCreated {
		t.Fatalf("replace avatar: %s", res)
	}
	reg := c.Do(http.MethodGet, pathRegistration, nil)
	if reg.Body["avatar"].(map[string]any)["id"] == first {
		t.Fatal("avatar not replaced")
	}
	invalid := []struct {
		kind string
		data []byte
		code int
		want string
	}{
		{"passport", driverstest.JPEG(t, 10, 10, 1), 422, "validation_failed"},
		{"", driverstest.JPEG(t, 10, 10, 1), 422, "validation_failed"},
		{"id_front", nil, 422, "validation_failed"},
		{"id_front", []byte("not an image at all"), 422, "unsupported_file_type"},
	}
	for _, tt := range invalid {
		if res := c.Upload(tt.kind, tt.data); res.Code != tt.code || res.ErrorCode() != tt.want {
			t.Fatalf("upload %q: %s", tt.kind, res)
		}
	}
	if res := c.Do(http.MethodPost, pathRegistration+"/files", `{"kind":"avatar"}`); res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("json upload: %s", res)
	}

	var extras []string
	for i := range 4 {
		res := c.Upload("vehicle_extra", driverstest.JPEG(t, 16, 16, uint8(10+i)))
		if res.Code != http.StatusCreated {
			t.Fatalf("extra %d: %s", i, res)
		}
		extras = append(extras, res.Body["id"].(string))
	}
	if res := c.Upload("vehicle_extra", driverstest.JPEG(t, 16, 16, 99)); res.Code != 422 || res.Fields()["kind"] != "too_many" {
		t.Fatalf("fifth extra: %s", res)
	}
	main := c.Upload("vehicle_main", driverstest.JPEG(t, 16, 16, 50))
	if res := c.Do(http.MethodDelete, pathRegistration+"/files/"+main.Body["id"].(string), nil); res.Code != http.StatusConflict ||
		res.Details()["reason"] != "not_removable" {
		t.Fatalf("delete main photo: %s", res)
	}
	if res := c.Do(http.MethodDelete, pathRegistration+"/files/"+extras[0], nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete extra: %s", res)
	}
	if res := c.Do(http.MethodDelete, pathRegistration+"/files/"+extras[0], nil); res.Code != http.StatusNotFound {
		t.Fatalf("delete extra twice: %s", res)
	}
	if res := c.Do(http.MethodGet, "/api/v1/driver/files/"+extras[0], nil); res.Code != http.StatusNotFound {
		t.Fatalf("deleted draft photo still served: %s", res)
	}
	if res := c.Upload("vehicle_extra", driverstest.JPEG(t, 16, 16, 98)); res.Code != http.StatusCreated {
		t.Fatalf("extra after delete: %s", res)
	}
	photos := c.Do(http.MethodGet, pathRegistration, nil).Body["vehicle"].(map[string]any)["photos"].([]any)
	if len(photos) != 5 || photos[0].(map[string]any)["kind"] != "main" {
		t.Fatalf("photos: %v", photos)
	}
}

func TestApplyVerifiedPhone(t *testing.T) {
	env := driverstest.New(t)
	ctx := context.Background()
	c := env.DriverClient()
	if err := env.VerifyPhone(c.UserID, "12345"); err == nil {
		t.Fatal("invalid phone accepted")
	}
	// Verified before the profile exists: copied when the profile is created.
	if err := env.VerifyPhone(c.UserID, "+7 (707) 123-45-67"); err != nil {
		t.Fatal(err)
	}
	res := c.Do(http.MethodPut, pathRegistration, registrationBody("Ерлан", "Бекенов", "", "", "", ""))
	if res.Body["phone"] != "+77071234567" || res.Body["phone_verified"] != true {
		t.Fatalf("phone on new profile: %s", res)
	}
	// A draft takes a newly verified number.
	if err := env.VerifyPhone(c.UserID, "87071234568"); err != nil {
		t.Fatal(err)
	}
	if res := c.Do(http.MethodGet, pathRegistration, nil); res.Body["phone"] != "+77071234568" {
		t.Fatalf("draft phone: %s", res)
	}
	// An approved driver's number belongs to them only.
	d := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{})
	other, _ := testutil.CreateUser(t, env.Pool)
	err := env.VerifyPhone(other, d.Phone)
	if err == nil || !strings.Contains(err.Error(), "phone_taken") {
		t.Fatalf("taken phone: %v", err)
	}
	// The owner may re-verify; an approved profile keeps its moderated phone.
	if err := env.VerifyPhone(d.UserID, "+77751112233"); err != nil {
		t.Fatal(err)
	}
	if env.Count(`SELECT count(*) FROM driver_profiles WHERE id = $1 AND phone_e164 = $2`, d.DriverID, d.Phone) != 1 {
		t.Fatal("approved profile phone changed")
	}
	if id, err := drivers.IDForUser(ctx, env.Pool, d.UserID); err != nil || id != d.DriverID {
		t.Fatalf("IDForUser: %v %v", id, err)
	}
	if _, err := drivers.IDForUser(ctx, env.Pool, uuid.New()); err == nil {
		t.Fatal("IDForUser for unknown user")
	}
}

func TestDriverProfile(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	if res := c.Do(http.MethodGet, "/api/v1/driver/profile", nil); res.Code != http.StatusForbidden || res.ErrorCode() != "driver_not_registered" {
		t.Fatalf("profile before registration: %s", res)
	}
	d := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true})
	dc := env.AppClient(d.UserID, auth.AppDriver)
	res := dc.Do(http.MethodGet, "/api/v1/driver/profile", nil)
	if res.Code != http.StatusOK || res.Body["code"] != d.Code || res.Body["status"] != "approved" || res.Body["is_online"] != true ||
		res.Body["vehicle"].(map[string]any)["make"] != "Toyota" || res.Body["rating"] != nil {
		t.Fatalf("profile: %s", res)
	}
	if res := dc.Do(http.MethodPatch, "/api/v1/driver/profile", `{}`); res.Code != http.StatusOK {
		t.Fatalf("empty patch: %s", res)
	}
	if res := dc.Do(http.MethodPatch, "/api/v1/driver/profile", `{"first_name":"Жаңа","vehicle":{}}`); res.Code != 422 ||
		res.Fields()["first_name"] != "not_editable" || res.Fields()["vehicle"] != "not_editable" {
		t.Fatalf("moderated patch: %s", res)
	}
	if res := dc.Do(http.MethodPatch, "/api/v1/driver/profile", `{"nickname":"x"}`); res.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %s", res)
	}
	// Passenger tokens never reach driver routes (403 forbidden: wrong app).
	if res := env.AppClient(d.UserID, auth.AppPassenger).Do(http.MethodGet, "/api/v1/driver/profile", nil); res.Code != http.StatusForbidden || res.ErrorCode() != "forbidden" {
		t.Fatalf("passenger token on driver route: %s", res)
	}
}
