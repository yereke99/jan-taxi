package moderation_test

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
)

const (
	pathRegistration = "/api/v1/driver/registration"
	pathRegister     = "/api/v1/driver/register"
)

func adminPath(id uuid.UUID, action string) string {
	if action == "" {
		return "/api/v1/admin/drivers/" + id.String()
	}
	return "/api/v1/admin/drivers/" + id.String() + "/" + action
}

func strs(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func TestCompleteRegistrationAndApproval(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()

	res := c.Do(http.MethodGet, pathRegistration, nil)
	if res.Code != http.StatusOK || res.Body["status"] != "draft" || res.Body["id"] != nil || res.Body["code"] != nil ||
		res.Body["can_submit"] != false || res.Body["editable"] != true {
		t.Fatalf("empty registration: %s", res)
	}
	if got := strs(res.Body["missing_fields"]); len(got) != 11 {
		t.Fatalf("missing fields before start: %v", got)
	}

	driverID := c.CompleteDraft(driverstest.Registration{Plate: "123 АВС 04", Phone: "8 701 555 12 34"})
	res = c.Do(http.MethodGet, pathRegistration, nil)
	if res.Body["can_submit"] != true || len(strs(res.Body["missing_fields"])) != 0 || res.Body["phone"] != "+77015551234" {
		t.Fatalf("complete draft: %s", res)
	}
	if !regexp.MustCompile(`^EMB\d{3,}$`).MatchString(fmt.Sprint(res.Body["code"])) {
		t.Fatalf("driver code: %v", res.Body["code"])
	}
	vehicle := res.Body["vehicle"].(map[string]any)
	if vehicle["plate"] != "123 ABC 04" || vehicle["plate_normalized"] != "123ABC04" || len(vehicle["photos"].([]any)) != 1 {
		t.Fatalf("vehicle: %v", vehicle)
	}

	// Consent is mandatory on the first submission.
	if res := c.Do(http.MethodPost, pathRegister, nil); res.Code != http.StatusUnprocessableEntity || res.Fields()["pd_consent"] != "must_be_true" {
		t.Fatalf("submit without consent: %s", res)
	}
	if res := c.Do(http.MethodPost, pathRegister, map[string]any{"pd_consent": true}); res.Code != 422 || res.Fields()["pd_consent_version"] != "required" {
		t.Fatalf("submit without consent version: %s", res)
	}
	res = c.Submit()
	if res.Code != http.StatusOK || res.Body["status"] != "pending" || res.Body["editable"] != false || res.Body["pd_consent_version"] != "2026-10" {
		t.Fatalf("submit: %s", res)
	}
	if n := env.Count(`SELECT count(*) FROM vehicles WHERE driver_id = $1 AND status = 'claimed'`, driverID); n != 1 {
		t.Fatalf("vehicle not claimed")
	}
	if n := env.Count(`SELECT count(*) FROM driver_applications WHERE driver_id = $1 AND round = 1 AND status = 'pending'
		AND jsonb_array_length(snapshot->'file_ids') = 4 AND snapshot->>'phone' = '+77015551234'`, driverID); n != 1 {
		t.Fatalf("application snapshot missing")
	}
	if n := env.Count(`SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'moderation_submitted'`, c.UserID); n != 1 {
		t.Fatalf("moderation_submitted not queued")
	}
	if n := env.Count(`SELECT count(*) FROM driver_profiles WHERE id = $1 AND pd_consent_at IS NOT NULL`, driverID); n != 1 {
		t.Fatalf("consent not stored")
	}
	// Submitted data is locked.
	if res := c.Do(http.MethodPut, pathRegistration, map[string]any{"first_name": "A", "last_name": "B",
		"vehicle": map[string]any{"make": "", "model": "", "color": "", "plate": ""}}); res.Code != http.StatusConflict ||
		res.Details()["reason"] != "registration_locked" {
		t.Fatalf("edit while pending: %s", res)
	}
	if res := c.Upload("avatar", driverstest.JPEG(t, 20, 20, 9)); res.Code != http.StatusConflict {
		t.Fatalf("upload while pending: %s", res)
	}
	if res := c.Submit(); res.Code != http.StatusConflict || res.ErrorCode() != "invalid_transition" {
		t.Fatalf("double submit: %s", res)
	}

	// Only drivers.moderate may decide.
	for _, role := range []auth.Role{auth.RoleOperator, auth.RoleFinanceManager, auth.RoleAuditor} {
		if res := env.AdminClient(role).Do(http.MethodPost, adminPath(driverID, "approve"), nil); res.Code != http.StatusForbidden {
			t.Fatalf("%s approve: %s", role, res)
		}
	}
	mod := env.AdminClient(auth.RoleDriverModerator)
	res = mod.Do(http.MethodPost, adminPath(driverID, "approve"), map[string]any{"comment": "Барлығы дұрыс"})
	if res.Code != http.StatusOK || res.Body["status"] != "approved" || res.Body["approved_at"] == nil || res.Body["wallet"] == nil {
		t.Fatalf("approve: %s", res)
	}
	apps := res.Body["applications"].([]any)
	if len(apps) != 1 || apps[0].(map[string]any)["status"] != "approved" || apps[0].(map[string]any)["reviewed_by"] == nil {
		t.Fatalf("applications: %v", apps)
	}
	if res := mod.Do(http.MethodPost, adminPath(driverID, "approve"), nil); res.Code != http.StatusConflict || res.ErrorCode() != "invalid_transition" {
		t.Fatalf("approve twice: %s", res)
	}
	if n := env.Count(`SELECT count(*) FROM admin_audit_logs WHERE action = 'driver.approve' AND entity_id = $1`, driverID.String()); n != 1 {
		t.Fatalf("approve not audited")
	}
	if n := env.Count(`SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'moderation_approved'`, c.UserID); n != 1 {
		t.Fatalf("moderation_approved not queued")
	}
	if n := env.Count(`SELECT count(*) FROM driver_moderation_events WHERE driver_id = $1`, driverID); n != 2 {
		t.Fatalf("events: %d", n)
	}
	res = c.Do(http.MethodGet, pathRegistration, nil)
	if res.Body["status"] != "approved" || len(res.Body["history"].([]any)) != 2 {
		t.Fatalf("driver view after approval: %s", res)
	}
}

func TestSubmitIncompleteListsMissingFields(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	res := c.Do(http.MethodPut, pathRegistration, map[string]any{"first_name": "Айгүл", "last_name": "",
		"vehicle": map[string]any{"make": "Lada", "model": "", "color": "", "plate": ""}})
	if res.Code != http.StatusOK {
		t.Fatalf("save: %s", res)
	}
	res = c.Submit()
	if res.Code != http.StatusUnprocessableEntity || res.ErrorCode() != "registration_incomplete" {
		t.Fatalf("submit incomplete: %s", res)
	}
	want := []string{"avatar", "last_name", "phone", "id_front", "id_back", "vehicle_class", "vehicle_make_model", "vehicle_color", "vehicle_plate", "vehicle_photos"}
	if got := strs(res.Details()["missing_fields"]); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("missing_fields = %v, want %v", got, want)
	}
	if n := env.Count(`SELECT count(*) FROM driver_applications`); n != 0 {
		t.Fatal("application created for incomplete registration")
	}
}

func TestSubmitRequiresVerifiedPhone(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	c.CompleteDraft(driverstest.Registration{Plate: "777KZA02"})
	if res := c.Submit(); res.Code != http.StatusUnprocessableEntity || res.ErrorCode() != "phone_not_verified" {
		t.Fatalf("submit without phone: %s", res)
	}
	// With the requirement switched off by an admin the application goes through.
	if _, err := env.Pool.Exec(context.Background(),
		`UPDATE app_settings SET value = 'false' WHERE key = 'driver.require_phone_verification'`); err != nil {
		t.Fatal(err)
	}
	if res := c.Submit(); res.Code != http.StatusOK || res.Body["status"] != "pending" {
		t.Fatalf("submit with requirement off: %s", res)
	}
}

func TestDuplicatePhoneAndPlate(t *testing.T) {
	env := driverstest.New(t)
	a, b := env.DriverClient(), env.DriverClient()

	// B saves the plate and phone while A is still a draft (nothing claimed yet).
	a.CompleteDraft(driverstest.Registration{Plate: "123ABC04", Phone: "+77011112233"})
	b.CompleteDraft(driverstest.Registration{Plate: "123 авс 04", Phone: "87011112233"})
	if res := a.Submit(); res.Code != http.StatusOK {
		t.Fatalf("A submit: %s", res)
	}
	if res := b.Submit(); res.Code != http.StatusConflict || res.ErrorCode() != "phone_taken" {
		t.Fatalf("B submit with A's phone: %s", res)
	}
	if err := env.VerifyPhone(b.UserID, "+77011112244"); err != nil {
		t.Fatal(err)
	}
	if res := b.Submit(); res.Code != http.StatusConflict || res.ErrorCode() != "plate_taken" {
		t.Fatalf("B submit with A's plate (Cyrillic spelling): %s", res)
	}

	// Once claimed, the plate and phone are refused early.
	c := env.DriverClient()
	res := c.Do(http.MethodPut, pathRegistration, map[string]any{"first_name": "Ерлан", "last_name": "Сейтқали",
		"vehicle": map[string]any{"make": "Kia", "model": "Rio", "color": "Қара", "plate": "123-АВС-04"}})
	if res.Code != http.StatusConflict || res.ErrorCode() != "plate_taken" {
		t.Fatalf("save claimed plate: %s", res)
	}
	res = c.Do(http.MethodPost, pathRegistration+"/check", map[string]any{"plate": "123 АВС 04"})
	plate := res.Body["plate"].(map[string]any)
	if res.Code != http.StatusOK || plate["valid"] != true || plate["available"] != false || plate["normalized"] != "123ABC04" {
		t.Fatalf("check claimed plate: %s", res)
	}
	if err := env.VerifyPhone(c.UserID, "+7 701 111 22 33"); err == nil || !contains(err.Error(), "phone_taken") {
		t.Fatalf("verify claimed phone: %v", err)
	}
}

func contains(s, sub string) bool { return regexp.MustCompile(regexp.QuoteMeta(sub)).MatchString(s) }

func TestConcurrentSamePlateSubmissionsHaveOneWinner(t *testing.T) {
	env := driverstest.New(t)
	const n = 5
	clients := make([]*driverstest.Client, n)
	for i := range clients {
		clients[i] = env.DriverClient()
		clients[i].CompleteDraft(driverstest.Registration{Plate: "505XYZ04", Phone: fmt.Sprintf("+7707100000%d", i)})
	}
	codes := make([]int, n)
	errs := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res := c.Submit()
			codes[i], errs[i] = res.Code, res.ErrorCode()
		}()
	}
	close(start)
	wg.Wait()
	won := 0
	for i := range codes {
		switch {
		case codes[i] == http.StatusOK:
			won++
		case codes[i] == http.StatusConflict && errs[i] == "plate_taken":
		default:
			t.Fatalf("submission %d: %d %s", i, codes[i], errs[i])
		}
	}
	if won != 1 {
		t.Fatalf("%d submissions won, want exactly 1 (codes %v)", won, codes)
	}
	if got := env.Count(`SELECT count(*) FROM vehicles WHERE plate_normalized = '505XYZ04' AND status = 'claimed'`); got != 1 {
		t.Fatalf("claimed vehicles: %d", got)
	}
	if got := env.Count(`SELECT count(*) FROM driver_applications`); got != 1 {
		t.Fatalf("applications: %d", got)
	}
}

func TestCorrectionsWorkflow(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	driverID := c.CompleteDraft(driverstest.Registration{Plate: "321ABC04", Phone: "+77051234567"})
	if res := c.Upload("vehicle_extra", driverstest.JPEG(t, 30, 30, 7)); res.Code != http.StatusCreated {
		t.Fatalf("extra photo: %s", res)
	}
	if res := c.Submit(); res.Code != http.StatusOK {
		t.Fatalf("submit: %s", res)
	}
	mod := env.AdminClient(auth.RoleDriverModerator)

	invalid := []struct {
		name string
		body string
		key  string
	}{
		{"no items", `{"items":[]}`, "items"},
		{"bad field", `{"items":[{"field":"passport","reason":"other","comment":"x"}]}`, "items.0.field"},
		{"bad reason", `{"items":[{"field":"avatar","reason":"ugly"}]}`, "items.0.reason"},
		{"other needs comment", `{"items":[{"field":"avatar","reason":"other"}]}`, "items.0.comment"},
		{"duplicate", `{"items":[{"field":"avatar","reason":"missing_info"},{"field":"avatar","reason":"missing_info"}]}`, "items.1.field"},
	}
	for _, tt := range invalid {
		if res := mod.Do(http.MethodPost, adminPath(driverID, "request-corrections"), tt.body); res.Code != 422 || res.Fields()[tt.key] == nil {
			t.Fatalf("%s: %s", tt.name, res)
		}
	}
	res := mod.Do(http.MethodPost, adminPath(driverID, "request-corrections"), map[string]any{
		"items": []map[string]any{
			{"field": "vehicle_plate", "reason": "plate_mismatch", "comment": "Нөмір суреттегіге сәйкес емес"},
			{"field": "id_back", "reason": "id_unreadable"},
		},
		"comment": "Көліктің артқы жағын қайта суретке түсіріңіз",
	})
	if res.Code != http.StatusOK || res.Body["status"] != "changes_requested" || len(res.Body["corrections"].([]any)) != 2 {
		t.Fatalf("request corrections: %s", res)
	}
	if n := env.Count(`SELECT count(*) FROM vehicles WHERE driver_id = $1 AND status = 'claimed'`, driverID); n != 1 {
		t.Fatal("vehicle must stay claimed while corrections are requested")
	}
	var notice string
	if err := env.Pool.QueryRow(context.Background(), `
		SELECT params->>'comment' FROM notification_outbox WHERE user_id = $1 AND template = 'moderation_changes_requested'`,
		c.UserID).Scan(&notice); err != nil {
		t.Fatal(err)
	}
	if notice != "Жеке куәліктің артқы беті; Мемлекеттік нөмір. Көліктің артқы жағын қайта суретке түсіріңіз" {
		t.Fatalf("notification comment = %q", notice)
	}

	reg := c.Do(http.MethodGet, pathRegistration, nil)
	if reg.Body["editable"] != true || reg.Body["correction_comment"] == nil || len(reg.Body["corrections"].([]any)) != 2 {
		t.Fatalf("driver sees corrections: %s", reg)
	}
	// Unflagged fields stay locked.
	body := map[string]any{"first_name": "Басқа", "last_name": "Бекенов",
		"vehicle": map[string]any{"make": "Toyota", "model": "Camry", "color": "Ақ", "plate": "321ABC04"}}
	res = c.Do(http.MethodPut, pathRegistration, body)
	if res.Code != 422 || res.Fields()["first_name"] != "not_editable" || res.Details()["reason"] != "field_locked" {
		t.Fatalf("change locked name: %s", res)
	}
	if res := c.Upload("avatar", driverstest.JPEG(t, 20, 20, 3)); res.Code != 422 || res.Fields()["kind"] != "not_editable" {
		t.Fatalf("upload locked avatar: %s", res)
	}
	extraID := reg.Body["vehicle"].(map[string]any)["photos"].([]any)[1].(map[string]any)["id"].(string)
	if res := c.Do(http.MethodDelete, pathRegistration+"/files/"+extraID, nil); res.Code != 422 {
		t.Fatalf("delete locked vehicle photo: %s", res)
	}
	// Flagged fields can be fixed.
	body["first_name"] = "Ерлан"
	body["vehicle"].(map[string]any)["plate"] = "321 ABD 04"
	if res := c.Do(http.MethodPut, pathRegistration, body); res.Code != http.StatusOK {
		t.Fatalf("fix plate: %s", res)
	}
	if res := c.Upload("id_back", driverstest.JPEG(t, 50, 30, 4)); res.Code != http.StatusCreated {
		t.Fatalf("replace id_back: %s", res)
	}
	// Consent given in round 1 covers the resubmission.
	res = c.Do(http.MethodPost, pathRegister, nil)
	if res.Code != http.StatusOK || res.Body["status"] != "pending" {
		t.Fatalf("resubmit: %s", res)
	}
	if n := env.Count(`SELECT count(*) FROM driver_documents WHERE driver_id = $1 AND kind = 'id_back'`, driverID); n != 2 {
		t.Fatalf("id_back history rows: %d", n)
	}
	detail := mod.Do(http.MethodGet, adminPath(driverID, ""), nil)
	apps := detail.Body["applications"].([]any)
	events := detail.Body["moderation_events"].([]any)
	if len(apps) != 2 || apps[0].(map[string]any)["round"] != float64(2) || apps[1].(map[string]any)["status"] != "changes_requested" {
		t.Fatalf("applications: %v", apps)
	}
	var actions []string
	for _, e := range events {
		actions = append(actions, e.(map[string]any)["action"].(string))
	}
	if fmt.Sprint(actions) != "[resubmitted changes_requested submitted]" {
		t.Fatalf("events: %v", actions)
	}
	if detail.Body["vehicle"].(map[string]any)["plate_normalized"] != "321ABD04" {
		t.Fatalf("plate not updated: %v", detail.Body["vehicle"])
	}
	if res := mod.Do(http.MethodPost, adminPath(driverID, "approve"), nil); res.Code != http.StatusOK {
		t.Fatalf("approve after resubmission: %s", res)
	}
	if res := mod.Do(http.MethodPost, adminPath(driverID, "request-corrections"), `{"items":[{"field":"avatar","reason":"missing_info"}]}`); res.Code != http.StatusConflict ||
		res.ErrorCode() != "invalid_transition" {
		t.Fatalf("corrections on approved driver: %s", res)
	}
}

func TestRejectReleasesPlateAndPhone(t *testing.T) {
	env := driverstest.New(t)
	a := env.DriverClient()
	aID := a.CompleteDraft(driverstest.Registration{Plate: "999AAA04", Phone: "+77771234567"})
	if res := a.Submit(); res.Code != http.StatusOK {
		t.Fatalf("submit: %s", res)
	}
	mod := env.AdminClient(auth.RoleDriverModerator)
	if res := mod.Do(http.MethodPost, adminPath(aID, "reject"), `{"reason":""}`); res.Code != 422 || res.Fields()["reason"] != "required" {
		t.Fatalf("reject without reason: %s", res)
	}
	res := mod.Do(http.MethodPost, adminPath(aID, "reject"), map[string]any{"reason": "Құжаттар жалған", "comment": "ішкі ескертпе"})
	if res.Code != http.StatusOK || res.Body["status"] != "rejected" {
		t.Fatalf("reject: %s", res)
	}
	reg := a.Do(http.MethodGet, pathRegistration, nil)
	if reg.Body["rejection_reason"] != "Құжаттар жалған" || reg.Body["editable"] != false {
		t.Fatalf("driver view after reject: %s", reg)
	}
	if n := env.Count(`SELECT count(*) FROM vehicles WHERE driver_id = $1 AND status = 'released'`, aID); n != 1 {
		t.Fatal("plate not released")
	}
	if n := env.Count(`SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'moderation_rejected'`, a.UserID); n != 1 {
		t.Fatal("moderation_rejected not queued")
	}
	// The plate and phone are free for another driver now.
	b := env.DriverClient()
	b.CompleteDraft(driverstest.Registration{Plate: "999 ААА 04", Phone: "+77771234567"})
	if res := b.Submit(); res.Code != http.StatusOK {
		t.Fatalf("B submit with released plate and phone: %s", res)
	}
	if res := mod.Do(http.MethodPost, adminPath(aID, "reject"), `{"reason":"again"}`); res.Code != http.StatusConflict {
		t.Fatalf("reject twice: %s", res)
	}
}
