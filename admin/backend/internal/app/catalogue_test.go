package app

import (
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// endpointCatalogue is ARCHITECTURE §19, one "METHOD path" per operation.
// Public paths are written with their /api/v1 prefix.
var endpointCatalogue = []string{
	// Identity, config, realtime
	"POST /api/v1/auth/telegram/passenger",
	"POST /api/v1/auth/telegram/driver",
	"GET /api/v1/me",
	"PATCH /api/v1/me/preferences",
	"GET /api/v1/public/config",
	"GET /api/v1/ws",
	"GET /api/v1/openapi.yaml",
	"GET /api/v1/healthz",
	"GET /api/v1/readyz",
	"GET /healthz",
	"GET /readyz",
	// Geo, routes, passenger orders
	"GET /api/v1/geo/search",
	"GET /api/v1/geo/reverse",
	"POST /api/v1/routes/preview",
	"POST /api/v1/orders",
	"GET /api/v1/orders",
	"GET /api/v1/orders/{}",
	"POST /api/v1/orders/{}/cancel",
	"POST /api/v1/orders/{}/rating",
	"POST /api/v1/orders/{}/dispute",
	"GET /api/v1/orders/{}/driver-photo",
	// Driver
	"GET /api/v1/driver/registration",
	"PUT /api/v1/driver/registration",
	"POST /api/v1/driver/registration/files",
	"DELETE /api/v1/driver/registration/files/{}",
	"POST /api/v1/driver/registration/check",
	"POST /api/v1/driver/register",
	"GET /api/v1/driver/files/{}",
	"GET /api/v1/driver/profile",
	"PATCH /api/v1/driver/profile",
	"PATCH /api/v1/driver/availability",
	"POST /api/v1/driver/location",
	"GET /api/v1/driver/orders",
	"GET /api/v1/driver/orders/active",
	"GET /api/v1/driver/orders/history",
	"GET /api/v1/driver/orders/{}",
	"POST /api/v1/driver/orders/{}/accept",
	"POST /api/v1/driver/orders/{}/arriving",
	"POST /api/v1/driver/orders/{}/start",
	"POST /api/v1/driver/orders/{}/stops/{}/complete",
	"POST /api/v1/driver/orders/{}/complete",
	// Drivers cannot cancel (§22.3): no /driver/orders/{}/cancel.
	// Wallet and top-ups
	"GET /api/v1/driver/wallet",
	"GET /api/v1/driver/wallet/transactions",
	"POST /api/v1/driver/wallet/topups",
	"GET /api/v1/driver/wallet/topups",
	"GET /api/v1/driver/wallet/topups/{}",
	"POST /api/v1/driver/wallet/topups/{}/receipt",
	"POST /api/v1/driver/wallet/topups/{}/submit",
	"POST /api/v1/driver/wallet/topups/{}/cancel",
	"POST /api/v1/payments/webhooks/{}",
	// Admin
	"POST /api/v1/admin/auth/login",
	"POST /api/v1/admin/auth/logout",
	"GET /api/v1/admin/auth/me",
	"POST /api/v1/admin/auth/password",
	"GET /api/v1/admin/admins",
	"POST /api/v1/admin/admins",
	"PATCH /api/v1/admin/admins/{}",
	"GET /api/v1/admin/dashboard",
	"GET /api/v1/admin/reports/orders.csv",
	"GET /api/v1/admin/reports/ledger.csv",
	"GET /api/v1/admin/reports/topups.csv",
	"GET /api/v1/admin/orders",
	"GET /api/v1/admin/orders/{}",
	"POST /api/v1/admin/orders/{}/cancel",
	"POST /api/v1/admin/orders/{}/assign",
	"POST /api/v1/admin/orders/{}/dispute",
	"GET /api/v1/admin/disputes",
	"POST /api/v1/admin/disputes/{}/resolve",
	"GET /api/v1/admin/drivers",
	"GET /api/v1/admin/drivers/{}",
	"GET /api/v1/admin/drivers/{}/orders",
	"GET /api/v1/admin/drivers/{}/ledger",
	"GET /api/v1/admin/drivers/locations",
	"GET /api/v1/admin/files/{}",
	"POST /api/v1/admin/drivers/{}/approve",
	"POST /api/v1/admin/drivers/{}/reject",
	"POST /api/v1/admin/drivers/{}/request-corrections",
	"POST /api/v1/admin/drivers/{}/suspend",
	"POST /api/v1/admin/drivers/{}/restore",
	"PATCH /api/v1/admin/drivers/{}",
	"POST /api/v1/admin/drivers/{}/wallet/adjustments",
	"POST /api/v1/admin/drivers/{}/bonus",
	"GET /api/v1/admin/topups",
	"GET /api/v1/admin/topups/{}",
	"GET /api/v1/admin/topups/{}/receipt",
	"POST /api/v1/admin/topups/{}/confirm",
	"POST /api/v1/admin/topups/{}/reject",
	"POST /api/v1/admin/topups/{}/refund",
	"GET /api/v1/admin/ledger",
	"GET /api/v1/admin/wallets/reconciliation",
	"GET /api/v1/admin/cities",
	"POST /api/v1/admin/cities",
	"PATCH /api/v1/admin/cities/{}",
	"GET /api/v1/admin/cities/{}/zones",
	"POST /api/v1/admin/cities/{}/zones",
	"PATCH /api/v1/admin/zones/{}",
	"DELETE /api/v1/admin/zones/{}",
	"GET /api/v1/admin/places",
	"POST /api/v1/admin/places",
	"PATCH /api/v1/admin/places/{}",
	"DELETE /api/v1/admin/places/{}",
	"GET /api/v1/admin/tariffs",
	"POST /api/v1/admin/tariffs",
	"GET /api/v1/admin/vehicle-classes", // §21.3
	"POST /api/v1/admin/vehicle-classes",
	"PATCH /api/v1/admin/vehicle-classes/{}",
	"GET /api/v1/admin/settings",
	"PATCH /api/v1/admin/settings",
	"GET /api/v1/admin/notification-templates",
	"PUT /api/v1/admin/notification-templates/{}/{}",
	"DELETE /api/v1/admin/notification-templates/{}/{}",
	"GET /api/v1/admin/notifications",
	"POST /api/v1/admin/notifications/{}/retry",
	"GET /api/v1/admin/audit-logs",
	// Internal (bots → API, internal listener)
	"POST /internal/v1/bots/{}/lease",
	"POST /internal/v1/bots/{}/offset",
	"POST /internal/v1/bots/{}/updates/{}/claim",
	"POST /internal/v1/bots/{}/users/sync",
	"PUT /internal/v1/bots/{}/users/{}/language",
	"GET /internal/v1/passenger/{}/orders",
	"POST /internal/v1/passenger/{}/contact",
	"GET /internal/v1/driver/{}/summary",
	"POST /internal/v1/driver/{}/availability",
	"GET /internal/v1/driver/{}/orders",
	"POST /internal/v1/driver/{}/orders/{}/accept",
	"POST /internal/v1/driver/{}/contact",
	"POST /internal/v1/driver/{}/location",
	"POST /internal/v1/driver/{}/topup-receipt",
	"GET /internal/v1/config",
}

// mountedBeyondCatalogue lists routes that exist on purpose without a §19 row.
var mountedBeyondCatalogue = []string{
	"GET /api/v1/admin/tariffs/{}", // one tariff version (admin tariff history)
}

// mountedRoutes walks both listeners and returns "METHOD path" with parameter
// names erased, split by listener.
func mountedRoutes(t *testing.T, a *App) (public, internal map[string]bool) {
	t.Helper()
	walk := func(h chi.Routes) map[string]bool {
		out := map[string]bool{}
		err := chi.Walk(h, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			out[method+" "+normalizePath(route)] = true
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	return walk(a.root), walk(a.internalRoot)
}

// TestEndpointCatalogueMounted checks that every ARCHITECTURE §19 endpoint is
// mounted on the right listener and that nothing undocumented is mounted.
func TestEndpointCatalogueMounted(t *testing.T) {
	pool := testutil.NewDB(t)
	a := newTestApp(t, pool, testConfig(t))
	public, internal := mountedRoutes(t, a)

	var missing []string
	for _, op := range endpointCatalogue {
		on := public
		if strings.Contains(op, " /internal/") {
			on = internal
		}
		if !on[op] {
			missing = append(missing, op)
		}
	}
	if len(missing) > 0 {
		t.Errorf("§19 endpoints not mounted:\n  %s", strings.Join(missing, "\n  "))
	}

	var extra []string
	for _, routes := range []map[string]bool{public, internal} {
		for op := range routes {
			if !slices.Contains(endpointCatalogue, op) && !slices.Contains(mountedBeyondCatalogue, op) &&
				op != "GET /healthz" { // the internal listener's own liveness probe
				extra = append(extra, op)
			}
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("mounted routes missing from the catalogue:\n  %s", strings.Join(extra, "\n  "))
	}
	for op := range internal {
		if strings.HasPrefix(strings.SplitN(op, " ", 2)[1], "/api/") {
			t.Errorf("public route on the internal listener: %s", op)
		}
	}
	for op := range public {
		if strings.Contains(op, " /internal/") {
			t.Errorf("internal route on the public listener: %s", op)
		}
	}
}

// TestInternalRoutesRequireServiceSecret calls every internal route without
// valid service credentials: each must answer 401 before any handler runs.
func TestInternalRoutesRequireServiceSecret(t *testing.T) {
	pool := testutil.NewDB(t)
	cfg := testConfig(t)
	a := newTestApp(t, pool, cfg)

	fill := strings.NewReplacer("{}", "1")
	headerSets := []struct {
		name string
		hdr  []string
	}{
		{"no credentials", nil},
		{"wrong secret", []string{"Authorization", "Bearer not-the-secret", "X-Jantaxi-Bot", "passenger"}},
		{"secret without bot header", []string{"Authorization", "Bearer " + cfg.InternalBotServiceSecret}},
		{"secret with unknown bot", []string{"Authorization", "Bearer " + cfg.InternalBotServiceSecret, "X-Jantaxi-Bot", "admin"}},
	}
	n := 0
	for _, op := range endpointCatalogue {
		method, path, _ := strings.Cut(op, " ")
		if !strings.HasPrefix(path, "/internal/") {
			continue
		}
		n++
		for _, hs := range headerSets {
			res := do(t, a.InternalHandler(), method, fill.Replace(path), hs.hdr...)
			if res.status != http.StatusUnauthorized || errorCode(t, res.body) != "unauthorized" {
				t.Errorf("%s %s (%s): %d %s", method, path, hs.name, res.status, res.body)
			}
		}
		// Internal routes are never reachable on the public listener.
		if res := do(t, a.Handler(), method, fill.Replace(path),
			"Authorization", "Bearer "+cfg.InternalBotServiceSecret, "X-Jantaxi-Bot", "driver"); res.status != http.StatusNotFound {
			t.Errorf("%s %s on the public listener: %d", method, path, res.status)
		}
	}
	if n != 15 {
		t.Fatalf("catalogue lists %d internal routes, want 15", n)
	}

	// With valid credentials the same listener answers normally.
	res := do(t, a.InternalHandler(), http.MethodGet, "/internal/v1/config",
		"Authorization", "Bearer "+cfg.InternalBotServiceSecret, "X-Jantaxi-Bot", "passenger")
	if res.status != http.StatusOK {
		t.Fatalf("authorised GET /internal/v1/config: %d %s", res.status, res.body)
	}
}

// TestBackgroundJobsRegistered checks that every ARCHITECTURE §16 job (plus the
// retention jobs added since) is registered with its interval, and that the
// notification worker runs as a long-lived worker when enabled.
func TestBackgroundJobsRegistered(t *testing.T) {
	pool := testutil.NewDB(t)
	cfg := testConfig(t)
	cfg.NotificationWorkerEnabled = true
	a := newTestApp(t, pool, cfg)

	want := map[string]time.Duration{
		"search_expiry":              15 * time.Second,
		"dispatch_expand":            15 * time.Second,
		"topup_expiry":               5 * time.Minute,
		"quote_cleanup":              time.Hour,
		"stale_driver_offline":       5 * time.Minute,
		"bot_update_cleanup":         time.Hour,
		"location_history_retention": 24 * time.Hour,
		"session_cleanup":            time.Hour,
		"notification_retention":     24 * time.Hour,
		"file_orphan_cleanup":        6 * time.Hour,
		"driver_eta":                 10 * time.Second,
	}
	got := map[string]time.Duration{}
	for _, j := range a.jobs {
		got[j.name] = j.every
	}
	for name, every := range want {
		if g, ok := got[name]; !ok {
			t.Errorf("job %s not registered", name)
		} else if g != every {
			t.Errorf("job %s every %v, want %v", name, g, every)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected job %s", name)
		}
	}
	workers := map[string]bool{}
	for _, w := range a.workers {
		workers[w.name] = true
	}
	if !workers["notification_worker"] || !workers["realtime_listener"] {
		t.Errorf("workers: %v", workers)
	}

	// With the worker disabled the outbox retention still runs.
	cfg2 := testConfig(t)
	cfg2.NotificationWorkerEnabled = false
	b := newTestApp(t, pool, cfg2)
	found := false
	for _, j := range b.jobs {
		found = found || j.name == "notification_retention"
	}
	for _, w := range b.workers {
		if w.name == "notification_worker" {
			t.Error("notification worker registered while disabled")
		}
	}
	if !found {
		t.Error("notification_retention must not depend on NOTIFICATION_WORKER_ENABLED")
	}
}
