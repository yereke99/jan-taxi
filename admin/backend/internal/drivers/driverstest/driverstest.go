// Package driverstest builds an HTTP test environment for the drivers and
// moderation packages: real database, real app-token authentication for the
// Mini App routes and an injected admin principal for the admin routes.
package driverstest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/moderation"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// AdminHeader carries "<admin id>:<role>" for the injected admin principal.
const AdminHeader = "X-Test-Admin"

// Env is a test environment.
type Env struct {
	T     testing.TB
	Pool  *pgxpool.Pool
	Auth  *auth.Service
	Store *storage.Store
	H     http.Handler
}

// New builds an environment on a fresh database.
func New(t testing.TB) *Env {
	t.Helper()
	pool := testutil.NewDB(t)
	svc, err := auth.NewService(pool, auth.Config{
		PassengerBotToken: config.TestPassengerBotToken,
		DriverBotToken:    config.TestDriverBotToken,
		SessionSecret:     config.TestSessionSecret,
		Audit: func(ctx context.Context, q db.Querier, r *http.Request, e auth.AuditEntry) error {
			return audit.Log(ctx, q, r, audit.Entry{Action: e.Action, EntityType: e.EntityType, EntityID: e.EntityID,
				Before: e.Before, After: e.After, AdminID: e.AdminID})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.New(t.TempDir(), 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	api := chi.NewRouter()
	driverR := api.With(svc.RequireApp(auth.AppDriver), svc.LoadDriverProfile(true))
	passengerR := api.With(svc.RequireApp(auth.AppPassenger))
	adminMux := chi.NewRouter()
	adminR := adminMux.With(injectAdmin)
	api.Mount("/admin", adminMux)
	root := chi.NewRouter()
	root.Mount("/api/v1", api)

	dh := drivers.NewHandler(pool, store, drivers.Options{DefaultCity: "EMB", MaxUploadBytes: 10 << 20})
	dh.MountDriver(driverR)
	dh.MountPassenger(passengerR)
	dh.MountAdmin(adminR)
	mh := moderation.NewHandler(pool)
	mh.MountDriver(driverR)
	mh.MountAdmin(adminR)
	return &Env{T: t, Pool: pool, Auth: svc, Store: store, H: root}
}

func injectAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, role, ok := strings.Cut(r.Header.Get(AdminHeader), ":")
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		a := auth.AdminPrincipal{AdminID: uuid.MustParse(id), Role: auth.Role(role)}
		next.ServeHTTP(w, r.WithContext(auth.WithAdmin(r.Context(), a)))
	})
}

// Response is a recorded HTTP response.
type Response struct {
	Code   int
	Header http.Header
	Raw    []byte
	Body   map[string]any
}

// ErrorCode returns error.code of an error response.
func (r Response) ErrorCode() string {
	e, _ := r.Body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

// Fields returns error.fields of an error response.
func (r Response) Fields() map[string]any {
	e, _ := r.Body["error"].(map[string]any)
	f, _ := e["fields"].(map[string]any)
	return f
}

// Details returns error.details of an error response.
func (r Response) Details() map[string]any {
	e, _ := r.Body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	return d
}

// String summarises the response for failure messages.
func (r Response) String() string { return fmt.Sprintf("%d %s", r.Code, r.Raw) }

// Client sends requests as one identity.
type Client struct {
	env    *Env
	header http.Header
	UserID uuid.UUID
	Admin  uuid.UUID
}

// DriverClient creates a user and returns a client with a driver app token.
func (e *Env) DriverClient() *Client {
	e.T.Helper()
	userID, _ := testutil.CreateUser(e.T, e.Pool)
	return e.AppClient(userID, auth.AppDriver)
}

// AppClient returns a client with an app token for an existing user.
func (e *Env) AppClient(userID uuid.UUID, app auth.App) *Client {
	e.T.Helper()
	token, _, err := e.Auth.IssueAppToken(userID, app, time.Now())
	if err != nil {
		e.T.Fatal(err)
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	return &Client{env: e, header: h, UserID: userID}
}

// AdminClient creates an administrator with role and returns its client.
func (e *Env) AdminClient(role auth.Role) *Client {
	e.T.Helper()
	var id uuid.UUID
	if err := e.Pool.QueryRow(context.Background(), `
		INSERT INTO admin_users (email, full_name, password_hash, role) VALUES ($1, 'Test Admin', 'x', $2) RETURNING id`,
		uuid.NewString()+"@jantaxi.kz", string(role)).Scan(&id); err != nil {
		e.T.Fatal(err)
	}
	h := http.Header{}
	h.Set(AdminHeader, id.String()+":"+string(role))
	return &Client{env: e, header: h, Admin: id}
}

// Do sends a JSON request (body nil = no body).
func (c *Client) Do(method, path string, body any) Response {
	c.env.T.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				c.env.T.Fatal(err)
			}
			rd = bytes.NewReader(raw)
		}
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req)
}

// Upload sends a multipart registration upload.
func (c *Client) Upload(kind string, data []byte) Response {
	c.env.T.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if kind != "" {
		_ = mw.WriteField("kind", kind)
	}
	if data != nil {
		fw, err := mw.CreateFormFile("file", "photo.jpg")
		if err != nil {
			c.env.T.Fatal(err)
		}
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/driver/registration/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.send(req)
}

func (c *Client) send(req *http.Request) Response {
	for k, v := range c.header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	c.env.H.ServeHTTP(rec, req)
	res := Response{Code: rec.Code, Header: rec.Header(), Raw: rec.Body.Bytes()}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &res.Body); err != nil {
			c.env.T.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return res
}

// JPEG returns a small generated JPEG image; seed varies its colour.
func JPEG(t testing.TB, w, h int, seed uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x) + seed, G: uint8(y) * seed, B: seed, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// VerifyPhone records a bot-verified phone for the user, as the driver bot does.
func (e *Env) VerifyPhone(userID uuid.UUID, phone string) error {
	return db.WithTx(context.Background(), e.Pool, func(tx pgx.Tx) error {
		return drivers.ApplyVerifiedPhone(context.Background(), tx, userID, phone)
	})
}

// Registration describes the data CompleteDraft fills in. VehicleClass
// defaults to "sedan"; Seats 0 takes the class default.
type Registration struct {
	FirstName, LastName, Make, Model, Color, Plate, Phone string
	VehicleClass                                          string
	Seats                                                 int
}

// CompleteDraft fills in every required field and photo of a driver's
// registration (without submitting) and returns the driver profile id.
func (c *Client) CompleteDraft(reg Registration) uuid.UUID {
	t := c.env.T
	t.Helper()
	if reg.FirstName == "" {
		reg.FirstName, reg.LastName = "Ерлан", "Бекенов"
	}
	if reg.Make == "" {
		reg.Make, reg.Model, reg.Color = "Toyota", "Camry", "Ақ"
	}
	if reg.VehicleClass == "" {
		reg.VehicleClass = "sedan"
	}
	vehicle := map[string]any{"make": reg.Make, "model": reg.Model, "color": reg.Color, "plate": reg.Plate,
		"vehicle_class": reg.VehicleClass}
	if reg.Seats > 0 {
		vehicle["seats"] = reg.Seats
	}
	res := c.Do(http.MethodPut, "/api/v1/driver/registration", map[string]any{
		"first_name": reg.FirstName, "last_name": reg.LastName, "vehicle": vehicle,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("save registration: %s", res)
	}
	for i, kind := range []string{"avatar", "id_front", "id_back", "vehicle_main"} {
		if up := c.Upload(kind, JPEG(t, 40, 30, uint8(i+1))); up.Code != http.StatusCreated {
			t.Fatalf("upload %s: %s", kind, up)
		}
	}
	if reg.Phone != "" {
		if err := c.env.VerifyPhone(c.UserID, reg.Phone); err != nil {
			t.Fatalf("verify phone: %v", err)
		}
	}
	id, err := drivers.IDForUser(context.Background(), c.env.Pool, c.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Submit posts the registration with the personal-data consent.
func (c *Client) Submit() Response {
	c.env.T.Helper()
	return c.Do(http.MethodPost, "/api/v1/driver/register", map[string]any{"pd_consent": true, "pd_consent_version": "2026-10"})
}

// CreateOrder inserts an order of the seeded EMB taxi tariff for passengerID,
// assigned to driverID (uuid.Nil for none), in status.
func (e *Env) CreateOrder(passengerID, driverID uuid.UUID, status string) uuid.UUID {
	e.T.Helper()
	var driver any
	if driverID != uuid.Nil {
		driver = driverID
	}
	var id uuid.UUID
	err := e.Pool.QueryRow(context.Background(), `
		INSERT INTO orders (passenger_id, city_id, category, service_type, cargo_type, status, driver_id, fare,
		                    commission_bps, commission_amount, payment_method, tariff_id, distance_m, duration_s,
		                    stop_count, pickup_lat, pickup_lon, idempotency_key, accepted_at, cancelled_at, cancelled_by,
		                    vehicle_class, passenger_count)
		SELECT $1, c.id, 'taxi', 'passenger', 'none', $3, $2, 800, 1000, 80, 'cash', t.id, 1200, 180, 2, 48.8264, 58.1442,
		       'test-' || gen_random_uuid(), CASE WHEN $2::uuid IS NOT NULL THEN now() END,
		       CASE WHEN $3 = 'cancelled' THEN now() END, CASE WHEN $3 = 'cancelled' THEN 'driver' END, 'sedan', 1
		FROM cities c JOIN tariffs t ON t.city_id = c.id AND t.category = 'taxi' AND t.vehicle_class = 'sedan' AND t.is_active
		WHERE c.code = 'EMB'
		RETURNING id`, passengerID, driver, status).Scan(&id)
	if err != nil {
		e.T.Fatalf("create order: %v", err)
	}
	return id
}

// Count runs a count query.
func (e *Env) Count(sql string, args ...any) int {
	e.T.Helper()
	var n int
	if err := e.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.T.Fatalf("count %q: %v", sql, err)
	}
	return n
}
