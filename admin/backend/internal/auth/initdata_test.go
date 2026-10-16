package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

const (
	passengerToken = "111111:passenger-test-token"
	driverToken    = "222222:driver-test-token"
)

// signValues signs v like Telegram, skipping the keys in exclude from the
// data-check-string (used to prove that "signature" must stay in it).
func signValues(token string, v url.Values, exclude ...string) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		skip := k == "hash"
		for _, e := range exclude {
			skip = skip || k == e
		}
		if !skip {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + v.Get(k)
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	out := url.Values{}
	for k, vs := range v {
		out[k] = vs
	}
	out.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return out.Encode()
}

func baseValues(now time.Time) url.Values {
	v := url.Values{}
	v.Set("user", `{"id":424242,"first_name":"Әлия","last_name":"Серікова","username":"aliya","language_code":"ru","allows_write_to_pm":true,"photo_url":"https://t.me/i/userpic/320/a.jpg","is_premium":true}`)
	v.Set("auth_date", strconv.FormatInt(now.Unix(), 10))
	v.Set("query_id", "AAHq")
	v.Set("signature", "c2lnbmF0dXJl")
	v.Set("start_param", "order_123")
	return v
}

func TestValidateInitData(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	maxAge := 24 * time.Hour

	tests := []struct {
		name    string
		raw     func() string
		token   string
		wantErr error
	}{
		{"valid (testutil signer)", func() string {
			return testutil.SignInitData(passengerToken, 777, "Test", now.Add(-time.Hour), "taxi")
		}, passengerToken, nil},
		{"valid with signature and extra user fields", func() string { return signValues(passengerToken, baseValues(now)) }, passengerToken, nil},
		{"auth_date slightly in the future is tolerated", func() string {
			v := baseValues(now)
			v.Set("auth_date", strconv.FormatInt(now.Add(30*time.Second).Unix(), 10))
			return signValues(passengerToken, v)
		}, passengerToken, nil},
		{"tampered user", func() string {
			raw := signValues(passengerToken, baseValues(now))
			q, _ := url.ParseQuery(raw)
			q.Set("user", strings.Replace(q.Get("user"), "424242", "424243", 1))
			return q.Encode()
		}, passengerToken, ErrInvalidInitData},
		{"tampered start_param", func() string {
			raw := signValues(passengerToken, baseValues(now))
			q, _ := url.ParseQuery(raw)
			q.Set("start_param", "order_999")
			return q.Encode()
		}, passengerToken, ErrInvalidInitData},
		{"signature excluded from check string is rejected", func() string {
			return signValues(passengerToken, baseValues(now), "signature")
		}, passengerToken, ErrInvalidInitData},
		{"wrong bot token", func() string { return signValues(driverToken, baseValues(now)) }, passengerToken, ErrInvalidInitData},
		{"expired", func() string {
			v := baseValues(now)
			v.Set("auth_date", strconv.FormatInt(now.Add(-25*time.Hour).Unix(), 10))
			return signValues(passengerToken, v)
		}, passengerToken, ErrInitDataExpired},
		{"too far in the future", func() string {
			v := baseValues(now)
			v.Set("auth_date", strconv.FormatInt(now.Add(2*time.Minute).Unix(), 10))
			return signValues(passengerToken, v)
		}, passengerToken, ErrInvalidInitData},
		{"missing user", func() string {
			v := baseValues(now)
			v.Del("user")
			return signValues(passengerToken, v)
		}, passengerToken, ErrInvalidInitData},
		{"user without id", func() string {
			v := baseValues(now)
			v.Set("user", `{"first_name":"A"}`)
			return signValues(passengerToken, v)
		}, passengerToken, ErrInvalidInitData},
		{"missing auth_date", func() string {
			v := baseValues(now)
			v.Del("auth_date")
			return signValues(passengerToken, v)
		}, passengerToken, ErrInvalidInitData},
		{"missing hash", func() string {
			raw := signValues(passengerToken, baseValues(now))
			q, _ := url.ParseQuery(raw)
			q.Del("hash")
			return q.Encode()
		}, passengerToken, ErrInvalidInitData},
		{"non-hex hash", func() string {
			raw := signValues(passengerToken, baseValues(now))
			q, _ := url.ParseQuery(raw)
			q.Set("hash", strings.Repeat("z", 64))
			return q.Encode()
		}, passengerToken, ErrInvalidInitData},
		{"duplicate key", func() string {
			return signValues(passengerToken, baseValues(now)) + "&start_param=home"
		}, passengerToken, ErrInvalidInitData},
		{"too long", func() string {
			v := baseValues(now)
			v.Set("start_param", strings.Repeat("a", 4100))
			return signValues(passengerToken, v)
		}, passengerToken, ErrInvalidInitData},
		{"empty", func() string { return "" }, passengerToken, ErrInvalidInitData},
		{"not a query string", func() string { return "%zz" }, passengerToken, ErrInvalidInitData},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateInitData(tt.raw(), tt.token, maxAge, now)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.User.ID <= 0 || got.AuthDate.IsZero() || got.QueryID == "" {
				t.Fatalf("incomplete result: %+v", got)
			}
		})
	}
}

func TestValidateInitDataFields(t *testing.T) {
	now := time.Now()
	got, err := validateInitData(signValues(passengerToken, baseValues(now)), passengerToken, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	u := got.User
	if u.ID != 424242 || u.FirstName != "Әлия" || u.LastName != "Серікова" || u.Username != "aliya" ||
		u.LanguageCode != "ru" || !u.AllowsWriteToPM || u.PhotoURL == "" {
		t.Fatalf("unexpected user: %+v", u)
	}
	if got.StartParam != "order_123" || got.QueryID != "AAHq" || got.AuthDate.Unix() != now.Unix() {
		t.Fatalf("unexpected init data: %+v", got)
	}
}

func TestServiceValidateInitDataUsesMatchingBot(t *testing.T) {
	s := &Service{cfg: Config{PassengerBotToken: passengerToken, DriverBotToken: driverToken, InitDataMaxAge: time.Hour}}
	now := time.Now()
	raw := testutil.SignInitData(driverToken, 5, "D", now, "")
	if _, err := s.ValidateInitData(raw, AppDriver, now); err != nil {
		t.Fatalf("driver initData rejected by driver app: %v", err)
	}
	if _, err := s.ValidateInitData(raw, AppPassenger, now); !errors.Is(err, ErrInvalidInitData) {
		t.Fatalf("driver initData accepted by passenger app: %v", err)
	}
	s.cfg.DriverBotToken = ""
	if _, err := s.ValidateInitData(raw, AppDriver, now); !errors.Is(err, ErrInvalidInitData) {
		t.Fatalf("unconfigured bot must reject: %v", err)
	}
	if _, err := s.ValidateInitData(raw, App("other"), now); !errors.Is(err, ErrInvalidInitData) {
		t.Fatalf("unknown app must reject: %v", err)
	}
}
