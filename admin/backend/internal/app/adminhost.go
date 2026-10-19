package app

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// adminHostGuard keeps the admin API off the public Mini App host. Caddy
// serves /api/v1/admin* only on ADMIN_DOMAIN (the Mini App site answers 404
// before its API proxy); this is the same rule inside the API, so a proxy
// change or a tunnel that forwards the Mini App host cannot expose admin
// login or the admin routes. Requests whose Host is the host of
// PASSENGER_MINIAPP_URL or DRIVER_MINIAPP_URL get 404 not_found, unless the
// admin panel (ADMIN_FRONTEND_URL) shares that host. Other hosts (the admin
// domain, loopback addresses of the local admin port, the Vite dev proxy,
// direct calls) are not affected.
func adminHostGuard(cfg config.Config) func(http.Handler) http.Handler {
	admin := urlHost(cfg.AdminFrontendURL)
	blocked := map[string]bool{}
	for _, raw := range []string{cfg.PassengerMiniAppURL, cfg.DriverMiniAppURL} {
		if h := urlHost(raw); h != "" && h != admin {
			blocked[h] = true
		}
	}
	return func(next http.Handler) http.Handler {
		if len(blocked) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if blocked[normalHost(r.Host)] {
				httpx.WriteError(w, r, httpx.NotFound())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// urlHost returns the normalised host[:port] of an absolute URL, or "".
func urlHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return normalHost(u.Host)
}

// normalHost lowercases a Host value and drops a trailing dot and the
// default ports, so "App.Example.:443" and "app.example" compare equal.
func normalHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	for _, p := range []string{":443", ":80"} {
		h = strings.TrimSuffix(h, p)
	}
	return strings.TrimSuffix(h, ".")
}
