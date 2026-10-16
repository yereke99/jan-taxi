package botapi

import (
	"math"
	"net/http"

	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

type locationRequest struct {
	Lat       *float64 `json:"lat"`
	Lon       *float64 `json:"lon"`
	AccuracyM *float64 `json:"accuracy_m"`
	Heading   *float64 `json:"heading"`
	Live      *bool    `json:"live"`
}

func invalidNumber(v *float64, min, max float64) bool {
	return v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < min || *v > max)
}

func (l locationRequest) validate() error {
	fields := map[string]string{}
	switch {
	case l.Lat == nil:
		fields["lat"] = "required"
	case invalidNumber(l.Lat, -90, 90):
		fields["lat"] = "invalid"
	}
	switch {
	case l.Lon == nil:
		fields["lon"] = "required"
	case invalidNumber(l.Lon, -180, 180):
		fields["lon"] = "invalid"
	}
	if invalidNumber(l.AccuracyM, 0, math.MaxFloat64) {
		fields["accuracy_m"] = "invalid"
	}
	if invalidNumber(l.Heading, 0, 360) {
		fields["heading"] = "invalid"
	}
	if l.Live == nil {
		fields["live"] = "required"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// driverLocation stores a location shared in the driver bot (a one-off pin or
// a live location and its edits) through the same path as POST
// /driver/location: approved drivers only, inside Kazakhstan, rate-limited.
func (h *Handler) driverLocation(w http.ResponseWriter, r *http.Request) error {
	tgID, err := tgIDParam(r)
	if err != nil {
		return err
	}
	var req locationRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := req.validate(); err != nil {
		return err
	}
	ctx := r.Context()
	p, err := h.resolveDriver(ctx, tgID)
	if err != nil {
		return err
	}
	if p.Status != drivers.StatusApproved {
		return httpx.NewError(http.StatusForbidden, "driver_not_approved", "driver is not approved").
			WithDetails(map[string]any{"status": p.Status})
	}
	if !h.locationLimiter.Allow(p.ID.String()) {
		w.Header().Set("Retry-After", "2")
		return httpx.TooManyRequests()
	}
	source := drivers.SourceBotStatic
	if *req.Live {
		source = drivers.SourceBotLive
	}
	in := drivers.LocationInput{Lat: *req.Lat, Lon: *req.Lon, AccuracyM: req.AccuracyM, Heading: req.Heading}
	if err := drivers.RecordLocation(ctx, h.pool, p.ID, in, source); err != nil {
		return err
	}
	return httpx.NoContent(w)
}
