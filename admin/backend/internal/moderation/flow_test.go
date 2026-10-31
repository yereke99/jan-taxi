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

// TestRegistrationIsApprovedOnSubmit: a complete registration that passes the
// automatic checks is approved in the submitting request, through the same
// effects as an administrator's approval, with the system as the actor.
func TestRegistrationIsApprovedOnSubmit(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()

	res := c.Do(http.MethodGet, pathRegistration, nil)
	if res.Code != http.StatusOK || res.Body["status"] != "draft" || res.Body["id"] != nil || res.Body["code"] != nil ||
		res.Body["can_submit"] != false || res.Body["editable"] != true {
		t.Fatalf("empty registration: %s", res)
	}
	if got := strs(res.Body["missing_fields"]); fmt.Sprint(got) != "[avatar first_name last_name identity phone vehicle_class vehicle_make_model vehicle_color vehicle_plate]" {
		t.Fatalf("missing fields before start: %v", got)
	}

	iin := "000102500780"
	driverID := c.CompleteDraft(driverstest.Registration{Plate: "123 АВС 04", Phone: "8 701 555 12 34", IIN: iin, Patronymic: "Серікұлы"})
	res = c.Do(http.MethodGet, pathRegistration, nil)
	if res.Body["can_submit"] != true || len(strs(res.Body["missing_fields"])) != 0 || res.Body["phone"] != "+77015551234" ||
		res.Body["iin"] != iin || res.Body["patronymic"] != "Серікұлы" {
		t.Fatalf("complete draft: %s", res)
	}
	if !regexp.MustCompile(`^EMB\d{3,}$`).MatchString(fmt.Sprint(res.Body["code"])) {
		t.Fatalf("driver code: %v", res.Body["code"])
	}
	vehicle := res.Body["vehicle"].(map[string]any)
	if vehicle["plate"] != "123 ABC 04" || vehicle["plate_normalized"] != "123ABC04" || len(vehicle["photos"].([]any)) != 0 {
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
	if res.Code != http.StatusOK || res.Body["status"] != "approved" || res.Body["editable"] != false || res.Body["pd_consent_version"] != "2026-10" {
		t.Fatalf("submit: %s", res)
	}
	if n := env.Count(`SELECT count(*) FROM driver_profiles WHERE id = $1 AND status = 'approved' AND approved_at IS NOT NULL
		AND submitted_at IS NOT NULL AND pd_consent_at IS NOT NULL AND iin = $2`, driverID, iin); n != 1 {
		t.Fatal("profile not approved")
	}
	if n := env.Count(`SELECT count(*) FROM vehicles WHERE driver_id = $1 AND status = 'claimed'`, driverID); n != 1 {
		t.Fatalf("vehicle not claimed")
	}
	if n := env.Count(`SELECT count(*) FROM driver_applications WHERE driver_id = $1 AND round = 1 AND status = 'approved'
		AND reviewed_by IS NULL AND reviewed_at IS NOT NULL AND snapshot->>'phone' = '+77015551234'
		AND snapshot->>'iin_masked' = '********0780' AND snapshot->>'patronymic' = 'Серікұлы'`, driverID); n != 1 {
		t.Fatalf("application snapshot missing")
	}
	var actions []string
	rows, err := env.Pool.Query(context.Background(), `
		SELECT action || ':' || actor_type || ':' || COALESCE(admin_id::text, '-') FROM driver_moderation_events
		WHERE driver_id = $1 ORDER BY id`, driverID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, a)
	}
	rows.Close()
	if fmt.Sprint(actions) != "[submitted:driver:- approved:system:-]" {
		t.Fatalf("events: %v", actions)
	}
	if n := env.Count(`SELECT count(*) FROM admin_audit_logs WHERE action = 'driver.approve' AND entity_id = $1 AND admin_id IS NULL`, driverID.String()); n != 1 {
		t.Fatalf("automatic approval not audited")
	}
	for tpl, want := range map[string]int{"moderation_approved": 1, "driver_welcome_bonus": 1, "moderation_submitted": 0, "moderation_review_required": 0} {
		if n := env.Count(`SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = $2`, c.UserID, tpl); n != want {
			t.Errorf("%s queued %d times, want %d", tpl, n, want)
		}
	}
	if n := env.Count(`SELECT COALESCE((SELECT balance FROM wallets WHERE driver_id = $1), 0)`, driverID); n != 500 {
		t.Fatalf("welcome bonus balance %d", n)
	}
	// The raw IIN stays in the profile row only.
	for _, q := range []string{
		`SELECT count(*) FROM driver_applications WHERE snapshot::text LIKE '%' || $1 || '%'`,
		`SELECT count(*) FROM admin_audit_logs WHERE COALESCE(after::text, '') || COALESCE(before::text, '') LIKE '%' || $1 || '%'`,
		`SELECT count(*) FROM notification_outbox WHERE params::text LIKE '%' || $1 || '%'`,
		`SELECT count(*) FROM driver_moderation_events WHERE COALESCE(comment, '') || reasons::text LIKE '%' || $1 || '%'`,
	} {
		if n := env.Count(q, iin); n != 0 {
			t.Errorf("raw IIN copied: %s", q)
		}
	}

	// Approved data is locked; submitting again is a conflict.
	if res := c.Do(http.MethodPut, pathRegistration, map[string]any{"first_name": "A", "last_name": "B"}); res.Code != http.StatusConflict ||
		res.Details()["reason"] != "registration_locked" {
		t.Fatalf("edit after approval: %s", res)
	}
	if res := c.Upload("avatar", driverstest.JPEG(t, 20, 20, 9)); res.Code != http.StatusConflict {
		t.Fatalf("upload after approval: %s", res)
	}
	if res := c.Submit(); res.Code != http.StatusConflict || res.ErrorCode() != "invalid_transition" {
		t.Fatalf("double submit: %s", res)
	}
	mod := env.AdminClient(auth.RoleDriverModerator)
	if res := mod.Do(http.MethodPost, adminPath(driverID, "approve"), nil); res.Code != http.StatusConflict || res.ErrorCode() != "invalid_transition" {
		t.Fatalf("approve an approved driver: %s", res)
	}
	res = c.Do(http.MethodGet, pathRegistration, nil)
	if res.Body["status"] != "approved" || len(res.Body["history"].([]any)) != 2 {
		t.Fatalf("driver view after approval: %s", res)
	}
	// The driver may go online at once (with a fresh location).
	if res := c.Do(http.MethodPost, "/api/v1/driver/location", map[string]any{"lat": 48.8264, "lon": 58.1442}); res.Code != http.StatusNoContent {
		t.Fatalf("location: %s", res)
	}
	if res := c.Do(http.MethodPatch, "/api/v1/driver/availability", map[string]any{"online": true}); res.Code != http.StatusOK || res.Body["is_online"] != true {
		t.Fatalf("online: %s", res)
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
	// The body type starts as Sedan, so only what the driver must type is missing.
	want := []string{"avatar", "last_name", "identity", "phone", "vehicle_make_model", "vehicle_color", "vehicle_plate"}
	if got := strs(res.Details()["missing_fields"]); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("missing_fields = %v, want %v", got, want)
	}
	if n := env.Count(`SELECT count(*) FROM driver_applications`); n != 0 {
		t.Fatal("application created for incomplete registration")
	}
	if n := env.Count(`SELECT count(*) FROM driver_profiles WHERE user_id = $1 AND status = 'draft'`, c.UserID); n != 1 {
		t.Fatal("incomplete registration left draft")
	}
}

func TestSubmitRequiresVerifiedPhone(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	c.CompleteDraft(driverstest.Registration{Plate: "777KZA02"})
	if res := c.Submit(); res.Code != http.StatusUnprocessableEntity || res.ErrorCode() != "phone_not_verified" {
		t.Fatalf("submit without phone: %s", res)
	}
	// With the requirement switched off by an admin the registration goes through.
	if _, err := env.Pool.Exec(context.Background(),
		`UPDATE app_settings SET value = 'false' WHERE key = 'driver.require_phone_verification'`); err != nil {
		t.Fatal(err)
	}
	if res := c.Submit(); res.Code != http.StatusOK || res.Body["status"] != "approved" {
		t.Fatalf("submit with requirement off: %s", res)
	}
	// Without a phone there is no welcome bonus (it is once per phone and plate).
	if n := env.Count(`SELECT count(*) FROM welcome_bonus_grants`); n != 0 {
		t.Fatalf("bonus without a phone")
	}
}

// TestUniqueIINPhoneAndPlate: the automatic checks refuse an IIN, a phone or
// a plate another live driver holds; nothing of the refused submission stays.
func TestUniqueIINPhoneAndPlate(t *testing.T) {
	env := driverstest.New(t)
	a, b := env.DriverClient(), env.DriverClient()

	// B saves A's IIN, plate and phone while A is still a draft (nothing claimed yet).
	shared := driverstest.NewIIN()
	a.CompleteDraft(driverstest.Registration{Plate: "123ABC04", Phone: "+77011112233", IIN: shared})
	bID := b.CompleteDraft(driverstest.Registration{Plate: "123 авс 04", Phone: "87011112233", IIN: shared})
	if res := a.Submit(); res.Code != http.StatusOK || res.Body["status"] != "approved" {
		t.Fatalf("A submit: %s", res)
	}
	if res := b.Submit(); res.Code != http.StatusConflict || res.ErrorCode() != "iin_taken" {
		t.Fatalf("B submit with A's IIN: %s", res)
	}
	// B's draft still holds the IIN it typed; correcting it moves on to the phone.
	if res := b.Do(http.MethodPut, pathRegistration, map[string]any{"iin": "900101300017"}); res.Code != http.StatusOK {
		t.Fatalf("B fixes the IIN: %s", res)
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
	if n := env.Count(`SELECT count(*) FROM driver_profiles WHERE id = $1 AND status = 'draft' AND submitted_at IS NULL`, bID); n != 1 {
		t.Fatal("refused submission changed the profile")
	}
	if n := env.Count(`SELECT count(*) FROM driver_applications WHERE driver_id = $1`, bID); n != 0 {
		t.Fatal("refused submission left an application")
	}

	// Once claimed, the plate, phone and IIN are refused early.
	c := env.DriverClient()
	res := c.Do(http.MethodPut, pathRegistration, map[string]any{"first_name": "Ерлан", "last_name": "Сейтқали",
		"vehicle": map[string]any{"make": "Kia", "model": "Rio", "color": "Қара", "plate": "123-АВС-04"}})
	if res.Code != http.StatusConflict || res.ErrorCode() != "plate_taken" {
		t.Fatalf("save claimed plate: %s", res)
	}
	if res := c.Do(http.MethodPut, pathRegistration, map[string]any{"first_name": "Ерлан", "iin": shared}); res.Code != http.StatusConflict ||
		res.ErrorCode() != "iin_taken" {
		t.Fatalf("save claimed IIN: %s", res)
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
	if got := env.Count(`SELECT count(*) FROM welcome_bonus_grants`); got != 1 {
		t.Fatalf("welcome bonuses: %d", got)
	}
}

// TestLegacyCorrectionsWorkflow: an application pending from before automatic
// approval is sent back by a moderator; the driver fixes the flagged fields,
// adds the IIN that was never asked for and is approved on resubmission.
func TestLegacyCorrectionsWorkflow(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	driverID := c.LegacyPending(driverstest.Registration{Plate: "321ABC04", Phone: "+77051234567"})
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
		"comment": "Нөмірді тексеріңіз",
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
	if notice != "Жеке куәліктің артқы беті; Мемлекеттік нөмір. Нөмірді тексеріңіз" {
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
	// ID scans are no longer collected, even when a moderator flagged one.
	if res := c.Upload("id_back", driverstest.JPEG(t, 50, 30, 4)); res.Code != 422 || res.Fields()["kind"] != "invalid" {
		t.Fatalf("upload id_back: %s", res)
	}
	// Flagged fields can be fixed, and so can the IIN that was never given.
	body["first_name"] = "Ерлан"
	body["vehicle"].(map[string]any)["plate"] = "321 ABD 04"
	body["iin"] = driverstest.NewIIN()
	if res := c.Do(http.MethodPut, pathRegistration, body); res.Code != http.StatusOK {
		t.Fatalf("fix plate and add IIN: %s", res)
	}
	// Once stored, the IIN is locked like any unflagged field.
	if res := c.Do(http.MethodPut, pathRegistration, map[string]any{"iin": "900101300017"}); res.Code != 422 || res.Fields()["iin"] != "not_editable" {
		t.Fatalf("change stored IIN: %s", res)
	}
	// Consent given in round 1 covers the resubmission; the checks pass and it is approved.
	res = c.Do(http.MethodPost, pathRegister, nil)
	if res.Code != http.StatusOK || res.Body["status"] != "approved" {
		t.Fatalf("resubmit: %s", res)
	}
	detail := mod.Do(http.MethodGet, adminPath(driverID, ""), nil)
	apps := detail.Body["applications"].([]any)
	events := detail.Body["moderation_events"].([]any)
	if len(apps) != 2 || apps[0].(map[string]any)["round"] != float64(2) || apps[0].(map[string]any)["status"] != "approved" ||
		apps[1].(map[string]any)["status"] != "changes_requested" {
		t.Fatalf("applications: %v", apps)
	}
	var actions []string
	for _, e := range events {
		actions = append(actions, e.(map[string]any)["action"].(string)+":"+e.(map[string]any)["actor_type"].(string))
	}
	if fmt.Sprint(actions) != "[approved:system resubmitted:driver changes_requested:admin submitted:driver]" {
		t.Fatalf("events: %v", actions)
	}
	if detail.Body["vehicle"].(map[string]any)["plate_normalized"] != "321ABD04" || detail.Body["iin_masked"] == nil {
		t.Fatalf("detail after resubmission: %s", detail)
	}
	if res := mod.Do(http.MethodPost, adminPath(driverID, "approve"), nil); res.Code != http.StatusConflict || res.ErrorCode() != "invalid_transition" {
		t.Fatalf("approve after automatic approval: %s", res)
	}
	if res := mod.Do(http.MethodPost, adminPath(driverID, "request-corrections"), `{"items":[{"field":"avatar","reason":"missing_info"}]}`); res.Code != http.StatusConflict ||
		res.ErrorCode() != "invalid_transition" {
		t.Fatalf("corrections on approved driver: %s", res)
	}
}

// TestLegacyApprovalByModerator: an application pending from before is still
// approved by a moderator through the same approval path.
func TestLegacyApprovalByModerator(t *testing.T) {
	env := driverstest.New(t)
	c := env.DriverClient()
	driverID := c.LegacyPending(driverstest.Registration{Plate: "888AAA04", Phone: "+77771234500"})
	for _, role := range []auth.Role{auth.RoleOperator, auth.RoleFinanceManager, auth.RoleAuditor} {
		if res := env.AdminClient(role).Do(http.MethodPost, adminPath(driverID, "approve"), nil); res.Code != http.StatusForbidden {
			t.Fatalf("%s approve: %s", role, res)
		}
	}
	mod := env.AdminClient(auth.RoleDriverModerator)
	res := mod.Do(http.MethodPost, adminPath(driverID, "approve"), map[string]any{"comment": "Барлығы дұрыс"})
	if res.Code != http.StatusOK || res.Body["status"] != "approved" || res.Body["approved_at"] == nil || res.Body["wallet"] == nil ||
		res.Body["iin_masked"] != nil {
		t.Fatalf("approve: %s", res)
	}
	apps := res.Body["applications"].([]any)
	if len(apps) != 1 || apps[0].(map[string]any)["status"] != "approved" || apps[0].(map[string]any)["reviewed_by"] == nil {
		t.Fatalf("applications: %v", apps)
	}
	if n := env.Count(`SELECT count(*) FROM admin_audit_logs WHERE action = 'driver.approve' AND entity_id = $1 AND admin_id = $2`, driverID.String(), mod.Admin); n != 1 {
		t.Fatalf("approve not audited")
	}
	if n := env.Count(`SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'moderation_approved'`, c.UserID); n != 1 {
		t.Fatalf("moderation_approved not queued")
	}
	// A driver approved without an IIN keeps working and may add it later.
	if res := c.Do(http.MethodPut, "/api/v1/driver/profile/iin", map[string]any{"iin": driverstest.NewIIN()}); res.Code != http.StatusOK || res.Body["iin_masked"] == nil {
		t.Fatalf("add IIN after approval: %s", res)
	}
}

func TestRejectReleasesPlateAndPhone(t *testing.T) {
	env := driverstest.New(t)
	a := env.DriverClient()
	aID := a.LegacyPending(driverstest.Registration{Plate: "999AAA04", Phone: "+77771234567"})
	mod := env.AdminClient(auth.RoleDriverModerator)
	if res := mod.Do(http.MethodPost, adminPath(aID, "reject"), `{"reason":""}`); res.Code != 422 || res.Fields()["reason"] != "required" {
		t.Fatalf("reject without reason: %s", res)
	}
	res := mod.Do(http.MethodPost, adminPath(aID, "reject"), map[string]any{"reason": "Деректер жалған", "comment": "ішкі ескертпе"})
	if res.Code != http.StatusOK || res.Body["status"] != "rejected" {
		t.Fatalf("reject: %s", res)
	}
	reg := a.Do(http.MethodGet, pathRegistration, nil)
	if reg.Body["rejection_reason"] != "Деректер жалған" || reg.Body["editable"] != false {
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
	if res := b.Submit(); res.Code != http.StatusOK || res.Body["status"] != "approved" {
		t.Fatalf("B submit with released plate and phone: %s", res)
	}
	if res := mod.Do(http.MethodPost, adminPath(aID, "reject"), `{"reason":"again"}`); res.Code != http.StatusConflict {
		t.Fatalf("reject twice: %s", res)
	}
}
