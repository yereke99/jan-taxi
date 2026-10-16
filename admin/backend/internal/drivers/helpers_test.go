package drivers

import (
	"fmt"
	"testing"
	"time"
)

func TestValidName(t *testing.T) {
	valid := []string{"Ерлан", "Әлі-Ахмет", "О'Нил", "Нұрсұлтан", "Mary Ann", "Ж.", "Ғабит"}
	invalid := []string{"Ерлан2", "-", "", "Ерлан!", "<b>", "@admin"}
	for _, s := range valid {
		if !validName(s) {
			t.Errorf("validName(%q) = false", s)
		}
	}
	for _, s := range invalid {
		if validName(s) {
			t.Errorf("validName(%q) = true", s)
		}
	}
}

func TestParseCorrectionsAndLabels(t *testing.T) {
	items := ParseCorrections([]byte(`[{"field":"vehicle_photos","reason":"vehicle_photo_unclear","comment":null},
		{"field":"id_back","reason":"id_unreadable"},{"field":"","reason":"other"}]`))
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	if got := ParseCorrections([]byte(`not json`)); len(got) != 0 {
		t.Fatalf("malformed corrections = %v", got)
	}
	// Fields come back in form order whatever order the moderator used.
	fields := CorrectionFields(items)
	if fmt.Sprint(fields) != "[id_back vehicle_photos]" {
		t.Fatalf("fields = %v", fields)
	}
	want := map[string]string{
		"kk": "Жеке куәліктің артқы беті; Көлік суреттері",
		"ru": "Удостоверение личности, оборотная сторона; Фото автомобиля",
		"en": "ID card, back; Car photos",
		"xx": "Жеке куәліктің артқы беті; Көлік суреттері",
	}
	for lang, w := range want {
		if got := FieldLabels(fields, lang); got != w {
			t.Errorf("FieldLabels(%s) = %q, want %q", lang, got, w)
		}
	}
	for _, f := range RegistrationFields {
		if _, ok := fieldLabels[f]; !ok {
			t.Errorf("no label for %s", f)
		}
	}
}

func TestRegistrationRules(t *testing.T) {
	if Rating(0, 0) != nil {
		t.Fatal("rating without votes")
	}
	if r := Rating(14, 3); r == nil || *r != 4.67 {
		t.Fatalf("Rating(14,3) = %v", r)
	}
	now := time.Now()
	phone := "+77011234567"
	draft := Registration{Profile: &Profile{Status: StatusDraft}}
	if !draft.Editable() || len(draft.EditableFields()) != len(RegistrationFields) {
		t.Fatal("draft must be fully editable")
	}
	empty := Registration{}
	if empty.Status() != StatusDraft || !empty.Editable() {
		t.Fatal("registration without a profile is an editable draft")
	}
	if got := empty.MissingFields(false); fmt.Sprint(got) != "[avatar first_name last_name id_front id_back vehicle_make_model vehicle_color vehicle_plate vehicle_photos]" {
		t.Fatalf("missing without phone requirement = %v", got)
	}
	for _, s := range []string{StatusPending, StatusApproved, StatusRejected, StatusSuspended} {
		r := Registration{Profile: &Profile{Status: s}}
		if r.Editable() || len(r.EditableFields()) != 0 {
			t.Errorf("%s must not be editable", s)
		}
	}
	// The user's verified phone counts until the profile holds its own copy.
	r := Registration{User: UserInfo{PhoneE164: &phone, PhoneVerifiedAt: &now}, Profile: &Profile{Status: StatusDraft}}
	if !r.PhoneVerified() {
		t.Fatal("user phone not used")
	}
	other := "+77019999999"
	r.Profile.PhoneE164 = &other
	if p, _ := r.Phone(); *p != other || r.PhoneVerified() {
		t.Fatal("profile phone without verification time must not count as verified")
	}
}
