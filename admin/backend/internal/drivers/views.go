package drivers

import (
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicles"
)

// File URL prefixes: drivers fetch their own files with the app token, admins
// with the session cookie.
const (
	DriverFileURL = "/api/v1/driver/files/"
	AdminFileURL  = "/api/v1/admin/files/"
)

// FileRef is a private file reference (OpenAPI FileRef).
type FileRef struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	CreatedAt time.Time `json:"created_at"`
}

// NewFileRef builds a reference with the URL under base.
func NewFileRef(m *FileMeta, kind, base string) *FileRef {
	if m == nil {
		return nil
	}
	return &FileRef{ID: m.ID, Kind: kind, URL: base + m.ID.String(), Width: m.Width, Height: m.Height, CreatedAt: m.CreatedAt.UTC()}
}

// CityRef is a short city reference (OpenAPI CityRef).
type CityRef struct {
	ID   uuid.UUID   `json:"id"`
	Code string      `json:"code"`
	Name cities.Name `json:"name"`
}

// NewCityRef converts a city; nil stays nil.
func NewCityRef(c *cities.City) *CityRef {
	if c == nil {
		return nil
	}
	return &CityRef{ID: c.ID, Code: c.Code, Name: c.Name}
}

// DocumentsView holds the identity card photos.
type DocumentsView struct {
	IDFront *FileRef `json:"id_front"`
	IDBack  *FileRef `json:"id_back"`
}

// VehiclePhotoView is one vehicle photo (OpenAPI VehiclePhoto).
type VehiclePhotoView struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	CreatedAt time.Time `json:"created_at"`
}

// RegistrationVehicleView is the vehicle as entered (OpenAPI RegistrationVehicle).
// PlateKind is only filled for administrators.
type RegistrationVehicleView struct {
	Make            string             `json:"make"`
	Model           string             `json:"model"`
	Color           string             `json:"color"`
	Plate           string             `json:"plate"`
	PlateNormalized *string            `json:"plate_normalized"`
	PlateKind       *string            `json:"plate_kind,omitempty"`
	Photos          []VehiclePhotoView `json:"photos"`
}

// NewRegistrationVehicleView converts the vehicle and its photos; a missing
// vehicle becomes empty fields.
func NewRegistrationVehicleView(v *vehicles.Vehicle, photos []vehicles.Photo, base string, withKind bool) RegistrationVehicleView {
	out := RegistrationVehicleView{Photos: []VehiclePhotoView{}}
	if v == nil {
		return out
	}
	out.Make, out.Model, out.Color, out.Plate = v.Make, v.Model, v.Color, v.PlateDisplay()
	if v.PlateNormalized != "" {
		n := v.PlateNormalized
		out.PlateNormalized = &n
		if withKind {
			k := v.PlateKind()
			out.PlateKind = &k
		}
	}
	for _, p := range photos {
		out.Photos = append(out.Photos, VehiclePhotoView{
			ID: p.FileID, Kind: p.Kind, URL: base + p.FileID.String(), Width: p.Width, Height: p.Height, CreatedAt: p.CreatedAt.UTC(),
		})
	}
	return out
}

// VehicleView is the short vehicle description (OpenAPI Vehicle).
type VehicleView struct {
	Make  string `json:"make"`
	Model string `json:"model"`
	Color string `json:"color"`
	Plate string `json:"plate"`
}

// NewVehicleView converts a vehicle; nil stays nil.
func NewVehicleView(v *vehicles.Vehicle) *VehicleView {
	if v == nil {
		return nil
	}
	return &VehicleView{Make: v.Make, Model: v.Model, Color: v.Color, Plate: v.PlateDisplay()}
}

// HistoryItem is a moderation event as shown to the driver (no admin identity).
type HistoryItem struct {
	Action      string           `json:"action"`
	FromStatus  *string          `json:"from_status"`
	ToStatus    *string          `json:"to_status"`
	Comment     *string          `json:"comment"`
	Corrections []CorrectionItem `json:"corrections"`
	CreatedAt   time.Time        `json:"created_at"`
}

// RegistrationView is the driver's registration (OpenAPI DriverRegistration).
type RegistrationView struct {
	ID                *uuid.UUID              `json:"id"`
	Status            string                  `json:"status"`
	Code              *string                 `json:"code"`
	City              *CityRef                `json:"city"`
	FirstName         string                  `json:"first_name"`
	LastName          string                  `json:"last_name"`
	Phone             *string                 `json:"phone"`
	PhoneVerified     bool                    `json:"phone_verified"`
	Avatar            *FileRef                `json:"avatar"`
	Documents         DocumentsView           `json:"documents"`
	Vehicle           RegistrationVehicleView `json:"vehicle"`
	Corrections       []CorrectionItem        `json:"corrections"`
	CorrectionComment *string                 `json:"correction_comment"`
	RejectionReason   *string                 `json:"rejection_reason"`
	MissingFields     []string                `json:"missing_fields"`
	Editable          bool                    `json:"editable"`
	CanSubmit         bool                    `json:"can_submit"`
	SubmittedAt       *time.Time              `json:"submitted_at"`
	History           []HistoryItem           `json:"history"`
	PDConsentAt       *time.Time              `json:"pd_consent_at"`
	PDConsentVersion  *string                 `json:"pd_consent_version"`
}

// NewRegistrationView renders the registration for its driver.
func NewRegistrationView(r Registration, requirePhone bool) RegistrationView {
	corrections, comment := r.OpenCorrections()
	missing := r.MissingFields(requirePhone)
	phoneNum, _ := r.Phone()
	v := RegistrationView{
		Status:            r.Status(),
		City:              NewCityRef(r.City),
		Phone:             phoneNum,
		PhoneVerified:     r.PhoneVerified(),
		Avatar:            NewFileRef(r.Avatar, FieldAvatar, DriverFileURL),
		Documents:         DocumentsView{IDFront: NewFileRef(r.IDFront, FieldIDFront, DriverFileURL), IDBack: NewFileRef(r.IDBack, FieldIDBack, DriverFileURL)},
		Vehicle:           NewRegistrationVehicleView(r.Vehicle, r.Photos, DriverFileURL, false),
		Corrections:       corrections,
		CorrectionComment: comment,
		RejectionReason:   r.RejectionReason(),
		MissingFields:     missing,
		Editable:          r.Editable(),
		History:           []HistoryItem{},
	}
	v.CanSubmit = v.Editable && len(missing) == 0 && (r.PhoneVerified() || !requirePhone)
	if p := r.Profile; p != nil {
		id, code := p.ID, p.Code
		v.ID, v.Code = &id, &code
		v.FirstName, v.LastName = p.FirstName, p.LastName
		v.SubmittedAt = utcPtr(p.SubmittedAt)
		v.PDConsentAt, v.PDConsentVersion = utcPtr(p.PDConsentAt), p.PDConsentVersion
	}
	for _, e := range r.Events {
		v.History = append(v.History, HistoryItem{
			Action: e.Action, FromStatus: e.FromStatus, ToStatus: e.ToStatus, Comment: e.Comment,
			Corrections: e.Reasons, CreatedAt: e.CreatedAt.UTC(),
		})
	}
	return v
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// UTCPtr returns t in UTC (nil stays nil).
func UTCPtr(t *time.Time) *time.Time { return utcPtr(t) }
