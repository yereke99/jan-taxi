package drivers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/internal/verification"
)

// Options configure the driver endpoints.
type Options struct {
	// DefaultCity is the city code (or English name) new driver profiles start in.
	DefaultCity string
	// MaxUploadBytes limits one uploaded image (defaults to storage.DefaultMaxBytes).
	MaxUploadBytes int64
}

// Location reports are limited to one per 2 seconds per driver (small burst
// for network jitter).
const (
	locationPerMinute = 30
	locationBurst     = 3
)

// Handler serves the driver, passenger and admin endpoints of this package.
type Handler struct {
	pool            *pgxpool.Pool
	store           *storage.Store
	opt             Options
	verification    *verification.Service
	locationLimiter *httpx.RateLimiter
}

// NewHandler builds the handler.
func NewHandler(pool *pgxpool.Pool, store *storage.Store, opt Options) *Handler {
	if opt.MaxUploadBytes <= 0 {
		opt.MaxUploadBytes = storage.DefaultMaxBytes
	}
	return &Handler{pool: pool, store: store, opt: opt, locationLimiter: httpx.NewRateLimiter(locationPerMinute, locationBurst)}
}

func (h *Handler) WithVerification(s *verification.Service) *Handler { h.verification = s; return h }

// MountDriver mounts the driver Mini App routes (router with the driver app
// token and the optional driver profile already loaded).
func (h *Handler) MountDriver(r chi.Router) {
	r.Get("/driver/registration", httpx.H(h.getRegistration))
	r.Put("/driver/registration", httpx.H(h.putRegistration))
	r.Post("/driver/registration/files", httpx.H(h.uploadFile))
	r.Delete("/driver/registration/files/{fileId}", httpx.H(h.deleteFile))
	r.Post("/driver/registration/check", httpx.H(h.checkRegistration))
	r.Get("/driver/files/{id}", httpx.H(h.getOwnFile))
	r.Get("/driver/profile", httpx.H(h.getProfile))
	r.Patch("/driver/profile", httpx.H(h.patchProfile))
	r.Patch("/driver/availability", httpx.H(h.patchAvailability))
	r.Post("/driver/location", httpx.H(h.postLocation))
}

// MountPassenger mounts the passenger Mini App routes.
func (h *Handler) MountPassenger(r chi.Router) {
	r.Get("/orders/{id}/driver-photo", httpx.H(h.getOrderDriverPhoto))
}

// MountAdmin mounts the admin routes (router under /api/v1/admin with the
// admin session already checked).
func (h *Handler) MountAdmin(r chi.Router) {
	// The permission depends on the file's purpose; checked in the handler.
	r.Get("/files/{id}", httpx.H(h.getAdminFile))
	r.With(auth.Permit(auth.PermDriversView)).Get("/drivers/locations", httpx.H(h.adminLocations))
}

// ErrRegistrationLocked is returned when the registration cannot be changed in
// its current status (pending, approved, rejected, suspended).
func ErrRegistrationLocked(status string) *httpx.Error {
	return httpx.Conflict("conflict", "registration is not editable in its current status").
		WithDetails(map[string]any{"reason": "registration_locked", "status": status})
}

// ErrFieldLocked is returned in changes_requested when a field the moderator did
// not flag is changed; fields maps request fields to "not_editable".
func ErrFieldLocked(fields map[string]string, editable map[string]bool) *httpx.Error {
	list := []string{}
	for _, f := range RegistrationFields {
		if editable[f] {
			list = append(list, f)
		}
	}
	e := httpx.Validation(fields)
	e.Details = map[string]any{"reason": "field_locked", "editable_fields": list}
	return e
}

var errNotMultipart = httpx.NewError(http.StatusUnsupportedMediaType, "unsupported_media_type", "expected multipart/form-data")
