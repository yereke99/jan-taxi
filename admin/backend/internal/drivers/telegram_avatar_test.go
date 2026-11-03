package drivers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

const testTelegramPhotoURL = "https://t.me/i/userpic/320/photo.jpg"

type photoRoundTripper func(*http.Request) (*http.Response, error)

func (f photoRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTelegramPhotoURL(t *testing.T) {
	for _, raw := range []string{
		"", "http://t.me/i/userpic/320/p.jpg", "https://t.me:444/i/userpic/320/p.jpg",
		"https://t.me.evil.test/i/userpic/p.jpg", "https://evil.test/i/userpic/p.jpg",
		"https://u:p@t.me/i/userpic/p.jpg", "https://t.me/a", "https://127.0.0.1/i/userpic/p.jpg",
		"https://t.me/i/userpic/../p.jpg", "https://t.me/i/userpic/%2e%2e/p.jpg",
		"https://t.me/i/userpic/p.jpg#fragment", "https://t.me/i/userpic/p.SVG",
	} {
		if _, ok := telegramPhotoURL(raw); ok {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, ok := telegramPhotoURL(testTelegramPhotoURL); !ok {
		t.Fatal("rejected Telegram userpic")
	}
}

func TestTelegramPhotoPublicIP(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.0.1", "169.254.169.254",
		"0.0.0.0", "224.0.0.1", "100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1",
		"::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "fc00::1", "fe80::1", "2001:db8::1",
	} {
		if publicPhotoIP(netip.MustParseAddr(raw)) {
			t.Errorf("accepted non-public %s", raw)
		}
	}
	for _, raw := range []string{"149.154.167.99", "2001:67c:4e8:f004::9", "::ffff:149.154.167.99"} {
		if !publicPhotoIP(netip.MustParseAddr(raw)) {
			t.Errorf("rejected public %s", raw)
		}
	}
}

func TestTelegramPhotoFetchLimitsAndRedirects(t *testing.T) {
	u, _ := url.Parse(testTelegramPhotoURL)
	for _, tt := range []struct {
		name   string
		status int
		body   string
		length int64
		want   string
	}{
		{"success", 200, "abc", 3, ""},
		{"declared too large", 200, "abc", 6, "file_too_large"},
		{"stream too large", 200, "abcdef", -1, "file_too_large"},
		{"redirect", 302, "", 0, "telegram_photo_failed"},
		{"missing", 404, "", 0, "telegram_photo_failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := newTelegramPhotoClient()
			client.Transport = photoRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != testTelegramPhotoURL || r.Header.Get("Authorization") != "" {
					t.Fatal("unexpected external request or leaked token")
				}
				return &http.Response{StatusCode: tt.status, Header: http.Header{"Location": {"http://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader(tt.body)), ContentLength: tt.length}, nil
			})
			_, err := fetchTelegramPhoto(context.Background(), client, u, 5)
			if tt.want == "" && err != nil || tt.want != "" && (err == nil || httpx.AsError(err).Code != tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
	client := newTelegramPhotoClient()
	if err := client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) || client.Timeout != 10*time.Second {
		t.Fatal("download must be bounded and never follow redirects")
	}
}

// Sign the actual photo_url as Telegram does: changing it after signing must
// never let a caller fetch a URL or another account's portrait.
func signedPhotoLaunch(token string, tgID int64, photo string, at time.Time) string {
	u, _ := json.Marshal(map[string]any{"id": tgID, "first_name": "Test", "photo_url": photo})
	v := url.Values{"auth_date": {strconv.FormatInt(at.Unix(), 10)}, "user": {string(u)}}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte("auth_date=" + v.Get("auth_date") + "\nuser=" + v.Get("user")))
	v.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return v.Encode()
}

func TestImportTelegramAvatar(t *testing.T) {
	pool := testutil.NewDB(t)
	svc, err := auth.NewService(pool, auth.Config{
		DriverBotToken: config.TestDriverBotToken, PassengerBotToken: config.TestPassengerBotToken,
		SessionSecret: config.TestSessionSecret,
		Audit:         func(context.Context, db.Querier, *http.Request, auth.AuditEntry) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.New(t.TempDir(), 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	d := testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: StatusDraft})
	h := NewHandler(pool, store, Options{TelegramAuth: svc})
	var imageData bytes.Buffer
	if err := jpeg.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 24, 32)), nil); err != nil {
		t.Fatal(err)
	}
	downloads := 0
	lockDuringDownload := false
	h.telegramPhotoClient = &http.Client{Transport: photoRoundTripper(func(r *http.Request) (*http.Response, error) {
		downloads++
		if lockDuringDownload {
			if _, err := pool.Exec(r.Context(), `UPDATE driver_profiles SET status = 'pending' WHERE id = $1`, d.DriverID); err != nil {
				t.Fatal(err)
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(imageData.Bytes())), ContentLength: int64(imageData.Len())}, nil
	})}
	router := chi.NewRouter()
	h.MountDriver(router.With(svc.RequireApp(auth.AppDriver), svc.LoadDriverProfile(true)))
	token, _, err := svc.IssueAppToken(d.UserID, auth.AppDriver, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	call := func(body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/driver/registration/telegram-avatar", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	launch := signedPhotoLaunch(config.TestDriverBotToken, d.TelegramID, testTelegramPhotoURL, time.Now())
	tampered := strings.Replace(launch, "photo.jpg", "other.jpg", 1)
	for _, tt := range []struct {
		name, raw string
		status    int
		code      string
	}{
		{"missing", "", 422, "validation_failed"},
		{"invalid", "invalid", 401, "invalid_init_data"},
		{"tampered URL", tampered, 401, "invalid_init_data"},
		{"wrong bot", signedPhotoLaunch(config.TestPassengerBotToken, d.TelegramID, testTelegramPhotoURL, time.Now()), 401, "invalid_init_data"},
		{"expired", signedPhotoLaunch(config.TestDriverBotToken, d.TelegramID, testTelegramPhotoURL, time.Now().Add(-25*time.Hour)), 401, "init_data_expired"},
		{"another user", signedPhotoLaunch(config.TestDriverBotToken, d.TelegramID+1, testTelegramPhotoURL, time.Now()), 403, "forbidden"},
		{"no photo", signedPhotoLaunch(config.TestDriverBotToken, d.TelegramID, "", time.Now()), 422, "telegram_photo_unavailable"},
		{"untrusted host", signedPhotoLaunch(config.TestDriverBotToken, d.TelegramID, "https://127.0.0.1/i/userpic/p.jpg", time.Now()), 422, "telegram_photo_unavailable"},
		{"SVG", signedPhotoLaunch(config.TestDriverBotToken, d.TelegramID, "https://t.me/i/userpic/p.svg", time.Now()), 422, "telegram_photo_unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := call(map[string]string{"init_data": tt.raw})
			if res.Code != tt.status || !strings.Contains(res.Body.String(), `"code":"`+tt.code+`"`) {
				t.Fatalf("response: %d %s", res.Code, res.Body)
			}
		})
	}
	if downloads != 0 {
		t.Fatal("invalid imports reached the network")
	}
	res := call(map[string]string{"init_data": launch})
	if res.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", res.Code, res.Body)
	}
	var f FileRef
	if err := json.Unmarshal(res.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if f.Kind != KindAvatar || f.Width != 24 || f.Height != 32 || f.URL != DriverFileURL+f.ID.String() {
		t.Fatalf("file ref: %+v", f)
	}
	meta, err := store.Get(context.Background(), pool, f.ID)
	if err != nil || meta.OwnerUserID != d.UserID || meta.Purpose != storage.PurposeDriverAvatar || meta.Mime != "image/jpeg" {
		t.Fatalf("stored private photo: %+v %v", meta, err)
	}
	prof, err := GetProfile(context.Background(), pool, d.DriverID)
	if err != nil || prof.AvatarFileID == nil || *prof.AvatarFileID != f.ID {
		t.Fatalf("profile avatar: %+v %v", prof, err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE driver_profiles SET status = 'approved' WHERE id = $1`, d.DriverID); err != nil {
		t.Fatal(err)
	}
	res = call(map[string]string{"init_data": launch})
	if res.Code != 409 || downloads != 1 {
		t.Fatalf("locked profile: %d %s downloads=%d", res.Code, res.Body, downloads)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE driver_profiles SET status = 'draft' WHERE id = $1`, d.DriverID); err != nil {
		t.Fatal(err)
	}
	lockDuringDownload = true
	res = call(map[string]string{"init_data": launch})
	if res.Code != 409 {
		t.Fatalf("lock while downloading: %d %s", res.Code, res.Body)
	}
	var active int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM files WHERE owner_user_id = $1 AND deleted_at IS NULL`, d.UserID).Scan(&active); err != nil || active != 1 {
		t.Fatalf("unlinked image not discarded: %d %v", active, err)
	}
}
