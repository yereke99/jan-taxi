package users

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func appRouter(pool *pgxpool.Pool, p auth.Principal) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithPrincipal(req.Context(), p)))
		})
	})
	NewHandler(pool).MountApp(r)
	return r
}

func doJSON(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec.Code, out
}

func TestMePassenger(t *testing.T) {
	pool := testutil.NewDB(t)
	id, tg := testutil.CreateUser(t, pool)
	if _, err := pool.Exec(context.Background(), `UPDATE users SET phone_e164 = '+77012345678', phone_verified_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	code, me := doJSON(t, appRouter(pool, auth.Principal{UserID: id, TelegramID: tg, App: auth.AppPassenger}), http.MethodGet, "/me", "")
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, me)
	}
	if me["id"] != id.String() || me["telegram_user_id"] != float64(tg) || me["language"] != "kk" || me["app"] != "passenger" {
		t.Fatalf("me = %v", me)
	}
	if me["phone_masked"] != "+7 701 *** ** 78" || me["phone_verified"] != true {
		t.Fatalf("phone = %v %v", me["phone_masked"], me["phone_verified"])
	}
	if v, ok := me["driver"]; !ok || v != nil {
		t.Fatalf("driver should be null for passenger app, got %v", v)
	}
	for _, k := range []string{"first_name", "last_name", "username"} {
		if _, ok := me[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
}

func TestMeDriver(t *testing.T) {
	pool := testutil.NewDB(t)
	d := testutil.CreateDriver(t, pool, testutil.DriverOptions{Online: true})
	h := appRouter(pool, auth.Principal{UserID: d.UserID, TelegramID: d.TelegramID, App: auth.AppDriver})
	code, me := doJSON(t, h, http.MethodGet, "/me", "")
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, me)
	}
	drv, ok := me["driver"].(map[string]any)
	if !ok {
		t.Fatalf("driver = %v", me["driver"])
	}
	city := drv["city"].(map[string]any)
	name := city["name"].(map[string]any)
	if drv["id"] != d.DriverID.String() || drv["code"] != d.Code || drv["status"] != "approved" || drv["is_online"] != true ||
		city["code"] != "EMB" || name["kk"] != "Ембі" || name["ru"] != "Эмба" || name["en"] != "Embi" {
		t.Fatalf("driver summary = %v", drv)
	}
	// The fixture stores the verified phone on the driver profile only.
	if me["phone_masked"] == nil || me["phone_verified"] != true {
		t.Fatalf("driver phone = %v %v", me["phone_masked"], me["phone_verified"])
	}

	// A user without a driver profile using the driver app gets driver: null.
	id, tg := testutil.CreateUser(t, pool)
	_, me = doJSON(t, appRouter(pool, auth.Principal{UserID: id, TelegramID: tg, App: auth.AppDriver}), http.MethodGet, "/me", "")
	if me["driver"] != nil || me["phone_masked"] != nil || me["phone_verified"] != false {
		t.Fatalf("unregistered driver me = %v", me)
	}
}

func TestMeUnknownUserIsUnauthorized(t *testing.T) {
	pool := testutil.NewDB(t)
	code, _ := doJSON(t, appRouter(pool, auth.Principal{UserID: uuid.New(), TelegramID: 1, App: auth.AppPassenger}), http.MethodGet, "/me", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("status %d", code)
	}
	// Without a principal at all.
	r := chi.NewRouter()
	NewHandler(pool).MountApp(r)
	if code, _ := doJSON(t, r, http.MethodGet, "/me", ""); code != http.StatusUnauthorized {
		t.Fatalf("no principal status %d", code)
	}
}

func TestPatchPreferences(t *testing.T) {
	pool := testutil.NewDB(t)
	id, tg := testutil.CreateUser(t, pool)
	h := appRouter(pool, auth.Principal{UserID: id, TelegramID: tg, App: auth.AppPassenger})

	tests := []struct {
		body   string
		status int
		field  string
		lang   string
	}{
		{`{"language":"ru"}`, 200, "", "ru"},
		{`{"language":"en"}`, 200, "", "en"},
		{`{"language":"kz"}`, 422, "invalid", "en"},
		{`{"language":""}`, 422, "invalid", "en"},
		{`{}`, 422, "required", "en"},
		{`{"language":"kk","theme":"dark"}`, 400, "", "en"},
		{`not json`, 400, "", "en"},
	}
	for _, tt := range tests {
		code, body := doJSON(t, h, http.MethodPatch, "/me/preferences", tt.body)
		if code != tt.status {
			t.Fatalf("%s: status %d %v", tt.body, code, body)
		}
		if tt.status == 200 && body["language"] != tt.lang {
			t.Fatalf("%s: language %v", tt.body, body["language"])
		}
		if tt.field != "" {
			if got := body["error"].(map[string]any)["fields"].(map[string]any)["language"]; got != tt.field {
				t.Fatalf("%s: field = %v", tt.body, got)
			}
		}
		if lang, _ := Language(context.Background(), pool, id); lang != tt.lang {
			t.Fatalf("%s: stored language %s, want %s", tt.body, lang, tt.lang)
		}
	}
}
