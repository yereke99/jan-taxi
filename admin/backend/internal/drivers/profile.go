package drivers

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// ProfileView is the driver's own profile (OpenAPI DriverProfile).
type ProfileView struct {
	ID              uuid.UUID    `json:"id"`
	Code            string       `json:"code"`
	Status          string       `json:"status"`
	FirstName       string       `json:"first_name"`
	LastName        string       `json:"last_name"`
	Phone           *string      `json:"phone"`
	PhoneVerified   bool         `json:"phone_verified"`
	Avatar          *FileRef     `json:"avatar"`
	City            *CityRef     `json:"city"`
	Vehicle         *VehicleView `json:"vehicle"`
	Rating          *float64     `json:"rating"`
	RatingCount     int          `json:"rating_count"`
	CompletedTrips  int          `json:"completed_trips"`
	IsOnline        bool         `json:"is_online"`
	OnlineSince     *time.Time   `json:"online_since"`
	ApprovedAt      *time.Time   `json:"approved_at"`
	SuspendedReason *string      `json:"suspended_reason"`
	CreatedAt       time.Time    `json:"created_at"`
}

// NewProfileView renders a loaded registration as the driver's profile.
func NewProfileView(r Registration) ProfileView {
	p := r.Profile
	phoneNum, _ := r.Phone()
	return ProfileView{
		ID: p.ID, Code: p.Code, Status: p.Status, FirstName: p.FirstName, LastName: p.LastName,
		Phone: phoneNum, PhoneVerified: r.PhoneVerified(),
		Avatar:  NewFileRef(r.Avatar, FieldAvatar, DriverFileURL),
		City:    NewCityRef(r.City),
		Vehicle: NewVehicleView(r.Vehicle),
		Rating:  p.Rating(), RatingCount: p.RatingCount, CompletedTrips: p.CompletedTrips,
		IsOnline: p.IsOnline, OnlineSince: utcPtr(p.OnlineSince), ApprovedAt: utcPtr(p.ApprovedAt),
		SuspendedReason: p.SuspendedReason, CreatedAt: p.CreatedAt.UTC(),
	}
}

func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	reg, err := LoadRegistration(r.Context(), h.pool, d.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, NewProfileView(reg))
}

// moderatedFields are profile fields that only change through registration
// corrections; sending them to PATCH /driver/profile is refused.
var moderatedFields = map[string]bool{
	"first_name": true, "last_name": true, "phone": true, "avatar": true, "avatar_file_id": true, "vehicle": true,
	"city": true, "city_id": true, "code": true, "status": true, "documents": true, "language": true,
}

// patchProfile accepts no self-editable field in this version: an empty object
// is a no-op, moderated fields are 422 not_editable, anything else 400.
func (h *Handler) patchProfile(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	var body map[string]json.RawMessage
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		return err
	}
	if body == nil {
		// JSON null decodes to a nil map.
		return httpx.BadRequest("request body must be a JSON object")
	}
	if len(body) > 0 {
		keys := make([]string, 0, len(body))
		for k := range body {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fields := map[string]string{}
		for _, k := range keys {
			if !moderatedFields[k] {
				return httpx.BadRequest(`invalid JSON: unknown field "` + k + `"`)
			}
			fields[k] = "not_editable"
		}
		e := httpx.Validation(fields)
		e.Details = map[string]any{"reason": "field_locked"}
		return e
	}
	reg, err := LoadRegistration(r.Context(), h.pool, d.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, NewProfileView(reg))
}
