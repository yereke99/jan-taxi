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
	"sync/atomic"
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
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/iin"
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

var iinSeq atomic.Int64

// NewIIN returns a valid IIN (check digit included) that no other driver of
// this test process uses.
func NewIIN() string {
	for {
		prefix := fmt.Sprintf("9001013%04d", iinSeq.Add(1)%10_000)
		for d := '0'; d <= '9'; d++ {
			if v := prefix + string(d); iin.Valid(v) {
				return v
			}
		}
	}
}

// Registration describes the data CompleteDraft fills in. IIN defaults to a
// fresh valid one (NoIIN leaves it out, like registrations from before it was
// asked for); VehicleClass defaults to "sedan"; Seats 0 takes the class default.
type Registration struct {
	FirstName, LastName, Patronymic, Make, Model, Color, Plate, Phone string
	IIN                                                               string
	NoIIN                                                             bool
	VehicleClass                                                      string
	Seats                                                             int
}

// CompleteDraft fills in every required field and the profile photo of a
// driver's registration (without submitting) and returns the driver profile id.
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
	if reg.IIN == "" && !reg.NoIIN {
		reg.IIN = NewIIN()
	}
	vehicle := map[string]any{"make": reg.Make, "model": reg.Model, "color": reg.Color, "plate": reg.Plate,
		"vehicle_class": reg.VehicleClass}
	if reg.Seats > 0 {
		vehicle["seats"] = reg.Seats
	}
	body := map[string]any{"first_name": reg.FirstName, "last_name": reg.LastName, "patronymic": reg.Patronymic, "vehicle": vehicle}
	if reg.IIN != "" {
		body["iin"] = reg.IIN
	}
	if res := c.Do(http.MethodPut, "/api/v1/driver/registration", body); res.Code != http.StatusOK {
		t.Fatalf("save registration: %s", res)
	}
	if up := c.Upload("avatar", JPEG(t, 40, 30, 1)); up.Code != http.StatusCreated {
		t.Fatalf("upload avatar: %s", up)
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

// LegacyPending creates an application still waiting for a moderator, as
// submissions did before registrations were approved automatically: a
// complete draft without an IIN (it was not asked for then) moved to pending
// with its first application round, claimed vehicle and submission event.
func (c *Client) LegacyPending(reg Registration) uuid.UUID {
	t := c.env.T
	t.Helper()
	reg.NoIIN = true
	id := c.CompleteDraft(reg)
	err := db.WithTx(context.Background(), c.env.Pool, func(tx pgx.Tx) error {
		ctx := context.Background()
		if _, err := tx.Exec(ctx, `
			UPDATE driver_profiles dp
			SET status = 'pending', submitted_at = now(), pd_consent_at = now(), pd_consent_version = '2026-10',
			    phone_e164 = u.phone_e164, phone_verified_at = u.phone_verified_at
			FROM users u WHERE u.id = dp.user_id AND dp.id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE vehicles SET status = 'claimed' WHERE driver_id = $1 AND status <> 'archived'`, id); err != nil {
			return err
		}
		var appID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO driver_applications (driver_id, round, status, snapshot) VALUES ($1, 1, 'pending', '{"round": 1}')
			RETURNING id`, id).Scan(&appID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO driver_moderation_events (driver_id, application_id, actor_type, action, from_status, to_status)
			VALUES ($1, $2, 'driver', 'submitted', 'draft', 'pending')`, id, appID)
		return err
	})
	if err != nil {
		t.Fatalf("legacy pending application: %v", err)
	}
	return id
}

// LegacyEvidence stores ID scans and a vehicle photo for a driver, as
// registrations collected them before they were retired, and returns the
// file ids (front, back, vehicle photo).
func (c *Client) LegacyEvidence(driverID uuid.UUID) (front, back, photo uuid.UUID) {
	t := c.env.T
	t.Helper()
	ctx := context.Background()
	save := func(purpose string, seed uint8) uuid.UUID {
		f, err := c.env.Store.SaveImage(ctx, c.env.Pool, c.UserID, purpose, bytes.NewReader(JPEG(t, 40, 30, seed)))
		if err != nil {
			t.Fatalf("save %s: %v", purpose, err)
		}
		return f.ID
	}
	front, back, photo = save(storage.PurposeIDFront, 21), save(storage.PurposeIDBack, 22), save(storage.PurposeVehiclePhoto, 23)
	if _, err := c.env.Pool.Exec(ctx, `
		INSERT INTO driver_documents (driver_id, kind, file_id) VALUES ($1, 'id_front', $2), ($1, 'id_back', $3)`,
		driverID, front, back); err != nil {
		t.Fatal(err)
	}
	if _, err := c.env.Pool.Exec(ctx, `
		INSERT INTO vehicle_photos (vehicle_id, kind, file_id)
		SELECT id, 'main', $2 FROM vehicles WHERE driver_id = $1 AND status <> 'archived'`, driverID, photo); err != nil {
		t.Fatal(err)
	}
	return front, back, photo
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
