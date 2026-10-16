package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// RequireApp authenticates "Authorization: Bearer <app token>" issued for app
// and rejects blocked users. The principal is stored with WithPrincipal.
func (s *Service) RequireApp(app App) func(http.Handler) http.Handler {
	return s.requireToken(func(a App) bool { return a == app })
}

// RequireAnyApp is RequireApp for routes shared by both Mini Apps.
func (s *Service) RequireAnyApp() func(http.Handler) http.Handler {
	return s.requireToken(App.Valid)
}

func (s *Service) requireToken(allowed func(App) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				httpx.WriteError(w, r, httpx.Unauthorized())
				return
			}
			p, _, err := s.AuthenticateAppToken(r.Context(), token)
			if err == nil && !allowed(p.App) {
				// A valid token of the other Mini App: signing in again cannot
				// help, so this is 403 rather than 401.
				err = httpx.Forbidden()
			}
			if err != nil {
				httpx.WriteError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

// LoadDriverProfile attaches the caller's driver profile (WithDriver) on driver
// app requests. With optional=true requests without a profile continue (e.g.
// registration); otherwise they get 403 driver_not_registered. It must run after
// RequireApp(AppDriver).
func (s *Service) LoadDriverProfile(optional bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				httpx.WriteError(w, r, httpx.Unauthorized())
				return
			}
			ctx := r.Context()
			if p.App == AppDriver {
				d, found, err := s.DriverProfile(ctx, p.UserID)
				if err != nil {
					httpx.WriteError(w, r, err)
					return
				}
				if found {
					ctx = WithDriver(ctx, d)
				}
			}
			if _, has := DriverFrom(ctx); !has && !optional {
				_, err := RequireDriver(r.WithContext(ctx))
				httpx.WriteError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// DriverProfile loads the driver profile owned by userID, if any.
func (s *Service) DriverProfile(ctx context.Context, userID uuid.UUID) (DriverContext, bool, error) {
	var d DriverContext
	err := s.pool.QueryRow(ctx,
		`SELECT id, code, status, city_id FROM driver_profiles WHERE user_id = $1`, userID).
		Scan(&d.ID, &d.Code, &d.Status, &d.CityID)
	if db.IsNoRows(err) {
		return DriverContext{}, false, nil
	}
	if err != nil {
		return DriverContext{}, false, httpx.Internal(err)
	}
	return d, true, nil
}
