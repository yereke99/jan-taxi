package drivers_test

import (
	"bytes"
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
		{"bad plate and name", registrationBody("Ерлан2", "Бекенов", "Toyota", "Camry", "Ақ", "ABC 123"),
			map[string]string{"first_name": "invalid", "vehicle.plate": "invalid"}},
		{"too long", registrationBody(strings.Repeat("ә", 61), "Б", strings.Repeat("x", 41), "M", strings.Repeat("c", 31), "123ABC04"),
			map[string]string{"first_name": "too_long", "vehicle.make": "too_long", "vehicle.color": "too_long"}},
		{"IIN of 11 digits", map[string]any{"iin": "90010130001"}, map[string]string{"iin": "invalid"}},
		{"IIN of 13 digits", map[string]any{"iin": "9001013000170"}, map[string]string{"iin": "invalid"}},
		{"IIN with a letter", map[string]any{"iin": "90010130001a"}, map[string]string{"iin": "invalid"}},
		{"IIN with a wrong check digit", map[string]any{"iin": "900101300018"}, map[string]string{"iin": "checksum"}},
		{"patronymic", map[string]any{"patronymic": "Ұлы1"}, map[string]string{"patronymic": "invalid"}},
		{"long patronymic", map[string]any{"patronymic": strings.Repeat("а", 61)}, map[string]string{"patronymic": "too_long"}},
	}
	for _, tt := range cases {
		res := c.Do(http.MethodPut, pathRegistration, tt.body)
		if res.Code != http.StatusUnprocessableEntity || res.ErrorCode() != "validation_failed" {
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
	if res.Body["iin"] != nil || res.Body["patronymic"] != "" {
		t.Fatalf("identity before it was entered: %s", res)
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
		{`{"plate":"D 123 ABC"}`, true, true, "D123ABC", "D 123 ABC"},
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

// TestRegistrationKeepsWhatIsNotSent: every key of PUT /driver/registration
// is optional, so an older app version that does not know a field never
// blanks it, and its identity object still carries the IIN.
func TestRegistrationKeepsWhatIsNotSent(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	iin := "000301500128"
	res := c.Do(http.MethodPut, pathRegistration, map[string]any{
		"first_name": "Айгүл", "last_name": "Сәрсенова", "patronymic": "Нұрланқызы", "iin": " 0003 0150 0128 ",
		"vehicle": map[string]any{"make": "Kia", "model": "Rio", "color": "Ақ", "plate": "045 кма 04", "vehicle_class": "sedan"},
	})
	if res.Code != http.StatusOK || res.Body["iin"] != iin || res.Body["patronymic"] != "Нұрланқызы" || vehicleOf(res)["plate"] != "045 KMA 04" {
		t.Fatalf("full save: %s", res)
	}
	// An older app sends names and vehicle only: the IIN and patronymic stay.
	res = c.Do(http.MethodPut, pathRegistration, registrationBody("Айгүл", "Сәрсенова", "Kia", "Rio", "Қара", "045 кма 04"))
	if res.Code != http.StatusOK || res.Body["iin"] != iin || res.Body["patronymic"] != "Нұрланқызы" || vehicleOf(res)["color"] != "Қара" {
		t.Fatalf("older app save: %s", res)
	}
	// Page 1 alone keeps the vehicle; page 2 alone keeps the person.
	res = c.Do(http.MethodPut, pathRegistration, map[string]any{"first_name": "Айгүл", "last_name": "Сәрсен"})
	if v := vehicleOf(res); res.Code != http.StatusOK || v["make"] != "Kia" || v["vehicle_class"] != "sedan" || res.Body["iin"] != iin {
		t.Fatalf("person only: %s", res)
	}
	res = c.Do(http.MethodPut, pathRegistration, map[string]any{"vehicle": map[string]any{"vehicle_class": "minivan"}})
	if v := vehicleOf(res); res.Code != http.StatusOK || v["vehicle_class"] != "minivan" || v["model"] != "Rio" || res.Body["last_name"] != "Сәрсен" {
		t.Fatalf("vehicle only: %s", res)
	}
	// The identity object of older versions: its IIN and patronymic count, its
	// document fields are ignored; an empty IIN there clears nothing.
	other := "900101300017"
	res = c.Do(http.MethodPut, pathRegistration, map[string]any{"identity": map[string]any{
		"document_type": "id_card", "iin": other, "document_number": "123456789", "patronymic": "",
	}})
	if res.Code != http.StatusOK || res.Body["iin"] != other || res.Body["patronymic"] != "Нұрланқызы" {
		t.Fatalf("identity object: %s", res)
	}
	res = c.Do(http.MethodPut, pathRegistration, map[string]any{"identity": map[string]any{"iin": "", "document_number": ""}})
	if res.Code != http.StatusOK || res.Body["iin"] != other {
		t.Fatalf("empty identity object: %s", res)
	}
	// An empty string clears a field while drafting.
	res = c.Do(http.MethodPut, pathRegistration, map[string]any{"iin": "", "patronymic": ""})
	if res.Code != http.StatusOK || res.Body["iin"] != nil || res.Body["patronymic"] != "" {
		t.Fatalf("clear identity: %s", res)
	}
	if n := env.Count(`SELECT count(*) FROM driver_profiles WHERE user_id = $1 AND iin IS NULL`, c.UserID); n != 1 {
		t.Fatal("cleared IIN stored as a value")
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
	// Only the profile photo is uploaded now: ID scans and vehicle photos are refused.
	invalid := []struct {
		kind string
		data []byte
		code int
		want string
	}{
		{"id_front", driverstest.JPEG(t, 10, 10, 1), 422, "validation_failed"},
		{"id_back", driverstest.JPEG(t, 10, 10, 1), 422, "validation_failed"},
		{"vehicle_main", driverstest.JPEG(t, 10, 10, 1), 422, "validation_failed"},
		{"vehicle_extra", driverstest.JPEG(t, 10, 10, 1), 422, "validation_failed"},
		{"passport", driverstest.JPEG(t, 10, 10, 1), 422, "validation_failed"},
		{"", driverstest.JPEG(t, 10, 10, 1), 422, "validation_failed"},
		{"avatar", nil, 422, "validation_failed"},
		{"avatar", []byte("not an image at all"), 422, "unsupported_file_type"},
		{"avatar", []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n"), 422, "unsupported_file_type"},
		{"avatar", append([]byte{0xFF, 0xD8, 0xFF}, bytes.Repeat([]byte{0}, int(drivers.MaxAvatarBytes))...), 413, "file_too_large"},
	}
	for _, tt := range invalid {
		if res := c.Upload(tt.kind, tt.data); res.Code != tt.code || res.ErrorCode() != tt.want {
			t.Fatalf("upload %q: %s", tt.kind, res)
		}
	}
	if n := env.Count(`SELECT count(*) FROM files WHERE owner_user_id = $1 AND purpose <> 'driver_avatar'`, c.UserID); n != 0 {
		t.Fatalf("%d documents or vehicle photos stored", n)
	}
	if res := c.Do(http.MethodPost, pathRegistration+"/files", `{"kind":"avatar"}`); res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("json upload: %s", res)
	}
	if res := c.Do(http.MethodDelete, pathRegistration+"/files/"+first, nil); res.Code == http.StatusNoContent {
		t.Fatalf("vehicle photo removal still mounted: %s", res)
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
	// A driver approved before the IIN was asked for has none and keeps working.
	if v, ok := res.Body["iin_masked"]; !ok || v != nil || res.Body["patronymic"] != "" {
		t.Fatalf("profile without IIN: %s", res)
	}
	if res := dc.Do(http.MethodPatch, "/api/v1/driver/profile", `{}`); res.Code != http.StatusOK {
		t.Fatalf("empty patch: %s", res)
	}
	if res := dc.Do(http.MethodPatch, "/api/v1/driver/profile", `{"first_name":"Жаңа","vehicle":{},"iin":"900101300017"}`); res.Code != 422 ||
		res.Fields()["first_name"] != "not_editable" || res.Fields()["vehicle"] != "not_editable" || res.Fields()["iin"] != "not_editable" {
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

// TestProfileIIN: a driver without an IIN adds it once from the profile;
// the answer and every later read show it masked.
func TestProfileIIN(t *testing.T) {
	env := driverstest.New(t)
	const path = "/api/v1/driver/profile/iin"
	d := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true})
	dc := env.AppClient(d.UserID, auth.AppDriver)
	if res := env.DriverClient().Do(http.MethodPut, path, map[string]any{"iin": "900101300017"}); res.Code != http.StatusForbidden ||
		res.ErrorCode() != "driver_not_registered" {
		t.Fatalf("without a profile: %s", res)
	}
	for _, tt := range []struct{ body, reason string }{
		{`{}`, "required"},
		{`{"iin":" "}`, "required"},
		{`{"iin":"90010130001"}`, "invalid"},
		{`{"iin":"9001013000170"}`, "invalid"},
		{`{"iin":"900101300018"}`, "checksum"},
	} {
		if res := dc.Do(http.MethodPut, path, tt.body); res.Code != 422 || res.Fields()["iin"] != tt.reason {
			t.Fatalf("%s: %s", tt.body, res)
		}
	}
	// Another live driver's IIN is refused.
	held := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{})
	if _, err := env.Pool.Exec(context.Background(), `UPDATE driver_profiles SET iin = '880808500671' WHERE id = $1`, held.DriverID); err != nil {
		t.Fatal(err)
	}
	if res := dc.Do(http.MethodPut, path, map[string]any{"iin": "880808500671"}); res.Code != http.StatusConflict || res.ErrorCode() != "iin_taken" {
		t.Fatalf("taken IIN: %s", res)
	}
	res := dc.Do(http.MethodPut, path, map[string]any{"iin": "000301500128"})
	if res.Code != http.StatusOK || res.Body["iin_masked"] != "********0128" || strings.Contains(string(res.Raw), "000301500128") {
		t.Fatalf("store IIN: %s", res)
	}
	if n := env.Count(`SELECT count(*) FROM driver_profiles WHERE id = $1 AND iin = '000301500128'`, d.DriverID); n != 1 {
		t.Fatal("IIN not stored with its leading zeros")
	}
	// Repeating it is harmless; changing it is not the driver's call.
	if res := dc.Do(http.MethodPut, path, map[string]any{"iin": "000301500128"}); res.Code != http.StatusOK {
		t.Fatalf("same IIN again: %s", res)
	}
	if res := dc.Do(http.MethodPut, path, map[string]any{"iin": "900101300017"}); res.Code != http.StatusConflict ||
		res.Details()["reason"] != "iin_already_set" {
		t.Fatalf("change IIN: %s", res)
	}
	if res := dc.Do(http.MethodGet, "/api/v1/driver/profile", nil); res.Body["iin_masked"] != "********0128" || strings.Contains(string(res.Raw), "000301500128") {
		t.Fatalf("profile after: %s", res)
	}
}
