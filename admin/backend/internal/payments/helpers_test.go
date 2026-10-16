package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

const testPaymentURL = "https://pay.kaspi.kz/pay/jantaxi-test"

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	svc  *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewDB(t)
	store, err := storage.New(t.TempDir(), storage.DefaultMaxBytes)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	svc := NewService(pool, store, Options{
		KaspiPaymentURL:    testPaymentURL,
		KaspiRecipientName: "ЖШС Jan Taxi",
		Log:                slog.New(slog.DiscardHandler),
	})
	return &env{t: t, pool: pool, svc: svc}
}

// driver creates a driver fixture and an HTTP handler authenticated as it.
type driver struct {
	testutil.DriverFixture
	h http.Handler
}

func (e *env) driver(status string) driver {
	e.t.Helper()
	f := testutil.CreateDriver(e.t, e.pool, testutil.DriverOptions{Status: status})
	return driver{DriverFixture: f, h: e.driverHandler(f, status)}
}

func (e *env) driverHandler(f testutil.DriverFixture, status string) http.Handler {
	cityID := testutil.CityID(e.t, e.pool, "EMB")
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithPrincipal(req.Context(), auth.Principal{UserID: f.UserID, TelegramID: f.TelegramID, App: auth.AppDriver})
			ctx = auth.WithDriver(ctx, auth.DriverContext{ID: f.DriverID, Code: f.Code, Status: status, CityID: cityID})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	e.svc.MountDriver(r)
	return r
}

// admin creates an admin user with role and an HTTP handler authenticated as it.
func (e *env) admin(role auth.Role) (auth.AdminPrincipal, http.Handler) {
	e.t.Helper()
	email := fmt.Sprintf("%s-%s@jantaxi.test", role, uuid.NewString()[:8])
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `
		INSERT INTO admin_users (email, full_name, password_hash, role) VALUES ($1, $2, 'x', $3) RETURNING id`,
		email, "Admin "+string(role), string(role)).Scan(&id); err != nil {
		e.t.Fatalf("create admin: %v", err)
	}
	p := auth.AdminPrincipal{AdminID: id, Email: email, FullName: "Admin " + string(role), Role: role, SessionID: uuid.New()}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithAdmin(req.Context(), p)))
		})
	})
	e.svc.MountAdmin(r)
	return p, r
}

type resp struct {
	code int
	body map[string]any
	raw  []byte
	hdr  http.Header
}

func (r resp) errCode() string {
	if e, ok := r.body["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func (r resp) errField(name string) string {
	if e, ok := r.body["error"].(map[string]any); ok {
		if f, ok := e["fields"].(map[string]any); ok {
			s, _ := f[name].(string)
			return s
		}
	}
	return ""
}

func (r resp) errDetails() map[string]any {
	if e, ok := r.body["error"].(map[string]any); ok {
		d, _ := e["details"].(map[string]any)
		return d
	}
	return nil
}

func do(t *testing.T, h http.Handler, method, path string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			rd = bytes.NewReader(raw)
		}
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return serve(t, h, req)
}

func serve(t *testing.T, h http.Handler, req *http.Request) resp {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := resp{code: rec.Code, raw: rec.Body.Bytes(), hdr: rec.Header()}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out.body); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return out
}

func expect(t *testing.T, r resp, code int) {
	t.Helper()
	if r.code != code {
		t.Fatalf("status %d, want %d: %s", r.code, code, r.raw)
	}
}

// createTopUp creates a top-up for d and returns its id.
func createTopUp(t *testing.T, d driver, amount int64) (uuid.UUID, map[string]any) {
	t.Helper()
	r := do(t, d.h, http.MethodPost, "/driver/wallet/topups", map[string]any{"amount": amount, "idempotency_key": uuid.NewString()})
	expect(t, r, http.StatusCreated)
	return uuid.MustParse(r.body["id"].(string)), r.body
}

func submit(t *testing.T, d driver, id uuid.UUID, body any) resp {
	t.Helper()
	return do(t, d.h, http.MethodPost, "/driver/wallet/topups/"+id.String()+"/submit", body)
}

func confirmBody(amount int64, txn string) map[string]any {
	return map[string]any{
		"verified_amount":         amount,
		"provider_transaction_id": txn,
		"paid_at":                 time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"payer_info":              "Ерлан Б.",
	}
}

func confirm(t *testing.T, h http.Handler, id uuid.UUID, amount int64, txn string) resp {
	t.Helper()
	return do(t, h, http.MethodPost, "/topups/"+id.String()+"/confirm", confirmBody(amount, txn))
}

func balance(t *testing.T, pool *pgxpool.Pool, driverID uuid.UUID) (int64, int64) {
	t.Helper()
	var b, r int64
	if err := pool.QueryRow(context.Background(), `SELECT balance, reserved FROM wallets WHERE driver_id = $1`, driverID).Scan(&b, &r); err != nil {
		t.Fatalf("wallet: %v", err)
	}
	return b, r
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func topUpStatus(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM top_up_requests WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("status: %v", err)
	}
	return s
}

// pngImage returns a small valid PNG.
func pngImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := 0; x < 40; x++ {
		for y := 0; y < 30; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 6), G: uint8(y * 8), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png: %v", err)
	}
	return buf.Bytes()
}

func multipartRequest(t *testing.T, path, field string, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, "receipt.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func newRouter() chi.Router { return chi.NewRouter() }

func newJSONRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}
