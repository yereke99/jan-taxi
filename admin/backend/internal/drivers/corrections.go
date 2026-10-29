package drivers

import (
	"encoding/json"
	"strings"
)

// Registration fields addressed by moderator corrections and missing_fields
// (OpenAPI RegistrationField).
const (
	FieldIdentity         = "identity"
	FieldAvatar           = "avatar"
	FieldFirstName        = "first_name"
	FieldLastName         = "last_name"
	FieldPhone            = "phone"
	FieldIDFront          = "id_front"
	FieldIDBack           = "id_back"
	FieldVehicleClass     = "vehicle_class" // class, seats and cargo volume (ARCHITECTURE §21)
	FieldVehicleMakeModel = "vehicle_make_model"
	FieldVehicleColor     = "vehicle_color"
	FieldVehiclePlate     = "vehicle_plate"
	FieldVehiclePhotos    = "vehicle_photos"
)

// RegistrationFields lists the fields in the order of the registration form.
var RegistrationFields = []string{
	FieldIdentity, FieldAvatar, FieldFirstName, FieldLastName, FieldPhone, FieldIDFront, FieldIDBack,
	FieldVehicleClass, FieldVehicleMakeModel, FieldVehicleColor, FieldVehiclePlate, FieldVehiclePhotos,
}

// ValidRegistrationField reports whether f is a registration field.
func ValidRegistrationField(f string) bool { return contains(RegistrationFields, f) }

// Correction reasons (OpenAPI CorrectionReason).
var CorrectionReasons = []string{
	"invalid_plate", "plate_mismatch", "duplicate_plate", "duplicate_phone", "id_unreadable",
	"vehicle_photo_unclear", "missing_info", "other",
}

// ValidCorrectionReason reports whether r is a correction reason.
func ValidCorrectionReason(r string) bool { return contains(CorrectionReasons, r) }

// CorrectionItem is one moderator remark on a registration field.
type CorrectionItem struct {
	Field   string  `json:"field"`
	Reason  string  `json:"reason"`
	Comment *string `json:"comment"`
}

// ParseCorrections decodes a stored corrections/reasons array; malformed data
// yields an empty list rather than an error so history stays readable.
func ParseCorrections(raw []byte) []CorrectionItem {
	out := []CorrectionItem{}
	if len(raw) == 0 {
		return out
	}
	var items []CorrectionItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return out
	}
	for _, it := range items {
		if it.Field != "" {
			out = append(out, it)
		}
	}
	return out
}

// CorrectionFields returns the distinct fields of items in form order.
func CorrectionFields(items []CorrectionItem) []string {
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.Field] = true
	}
	out := []string{}
	for _, f := range RegistrationFields {
		if seen[f] {
			out = append(out, f)
		}
	}
	return out
}

// fieldLabels name the registration fields for drivers in kk, ru and en.
var fieldLabels = map[string][3]string{
	FieldIdentity:         {"Жеке құжат деректері", "Данные документа", "Identity document details"},
	FieldAvatar:           {"Профиль суреті", "Фото профиля", "Profile photo"},
	FieldFirstName:        {"Аты", "Имя", "First name"},
	FieldLastName:         {"Тегі", "Фамилия", "Last name"},
	FieldPhone:            {"Телефон нөмірі", "Номер телефона", "Phone number"},
	FieldIDFront:          {"Жеке куәліктің алдыңғы беті", "Удостоверение личности, лицевая сторона", "ID card, front"},
	FieldIDBack:           {"Жеке куәліктің артқы беті", "Удостоверение личности, оборотная сторона", "ID card, back"},
	FieldVehicleClass:     {"Көлік түрі, орын саны және жүк сыйымдылығы", "Тип кузова, число мест и объём багажа", "Vehicle type, seats and cargo space"},
	FieldVehicleMakeModel: {"Көліктің маркасы мен моделі", "Марка и модель автомобиля", "Car make and model"},
	FieldVehicleColor:     {"Көліктің түсі", "Цвет автомобиля", "Car colour"},
	FieldVehiclePlate:     {"Мемлекеттік нөмір", "Госномер", "Licence plate"},
	FieldVehiclePhotos:    {"Көлік суреттері", "Фото автомобиля", "Car photos"},
}

// FieldLabels returns the localized names of fields joined for a message,
// e.g. "Жеке куәліктің артқы беті; Көлік суреттері".
func FieldLabels(fields []string, lang string) string {
	idx := 0
	switch lang {
	case "ru":
		idx = 1
	case "en":
		idx = 2
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		if l, ok := fieldLabels[f]; ok {
			parts = append(parts, l[idx])
		}
	}
	return strings.Join(parts, "; ")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
