package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

const (
	passengerBotToken = "111111:passenger-test-token"
	driverBotToken    = "222222:driver-test-token"
	sessionSecret     = "test-session-secret-0123456789-abcdef"
	serviceSecret     = "internal-bot-service-secret-for-tests"
	adminPassword     = "correct horse battery"
)

func auditLog(ctx context.Context, q db.Querier, r *http.Request, e auth.AuditEntry) error {
	return audit.Log(ctx, q, r, audit.Entry{Action: e.Action, EntityType: e.EntityType, EntityID: e.EntityID,
		Before: e.Before, After: e.After, AdminID: e.AdminID})
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	svc  *auth.Service
	srv  *httptest.Server
}

// newEnv mounts the auth routes the way the app does: /api/v1 public + app
// groups, /api/v1/admin public + RequireAdmin group, /internal/v1 with service auth.
func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewDB(t)
	svc, err := auth.NewService(pool, auth.Config{
		PassengerBotToken: passengerBotToken,
		DriverBotToken:    driverBotToken,
		SessionSecret:     sessionSecret,
		Audit:             auditLog,
		Log:               slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	whoami := func(w http.ResponseWriter, r *http.Request) error {
		p, err := auth.RequirePrincipal(r)
		if err != nil {
			return err
		}
		out := map[string]any{"user_id": p.UserID, "app": p.App, "telegram_id": p.TelegramID}
		if d, ok := auth.DriverFrom(r.Context()); ok {
			out["driver_id"] = d.ID
			out["driver_code"] = d.Code
		}
		return httpx.OK(w, out)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Group(svc.MountPublic)
		r.With(svc.RequireApp(auth.AppPassenger)).Get("/p/whoami", httpx.H(whoami))
		r.With(svc.RequireApp(auth.AppDriver), svc.LoadDriverProfile(true)).Get("/d/whoami", httpx.H(whoami))
		r.With(svc.RequireApp(auth.AppDriver), svc.LoadDriverProfile(false)).Get("/d/strict", httpx.H(whoami))
		r.With(svc.RequireAnyApp()).Get("/any/whoami", httpx.H(whoami))
		r.Route("/admin", func(r chi.Router) {
			r.Group(svc.MountAdminPublic)
			r.Group(func(r chi.Router) {
				r.Use(svc.RequireAdmin())
				svc.MountAdmin(r)
			})
		})
	})
	r.With(auth.RequireService(serviceSecret)).Get("/internal/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		_ = httpx.OK(w, map[string]string{"bot": auth.BotFrom(r.Context())})
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &env{t: t, pool: pool, svc: svc, srv: srv}
}

type response struct {
	status int
	header http.Header
	body   map[string]any
	raw    []byte
}

func (r response) errCode() string {
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func (e *env) do(c *http.Client, method, path string, body any, headers map[string]string) response {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c == nil {
		c = e.srv.Client()
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{status: resp.StatusCode, header: resp.Header, raw: raw}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}

// browser returns a client with a cookie jar, like the admin SPA.
func (e *env) browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Transport: e.srv.Client().Transport, Jar: jar}
}

func (e *env) cookie(c *http.Client) *http.Cookie {
	u, _ := url.Parse(e.srv.URL + "/api/v1/admin/auth/me")
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.AdminCookieName {
			return ck
		}
	}
	return nil
}

func (e *env) createAdmin(email string, role auth.Role) auth.AdminRecord {
	e.t.Helper()
	rec, err := e.svc.CreateAdmin(context.Background(), email, "Test "+strings.Split(email, "@")[0], role, adminPassword)
	if err != nil {
		e.t.Fatalf("create admin: %v", err)
	}
	return rec
}

// login signs in and returns the browser client and its CSRF token.
func (e *env) login(email string) (*http.Client, string) {
	e.t.Helper()
	c := e.browser()
	res := e.do(c, http.MethodPost, "/api/v1/admin/auth/login", map[string]string{"email": email, "password": adminPassword}, nil)
	if res.status != http.StatusOK {
		e.t.Fatalf("login %s: %d %s", email, res.status, res.raw)
	}
	return c, res.body["csrf_token"].(string)
}
