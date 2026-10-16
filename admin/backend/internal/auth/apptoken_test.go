package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

func tokenService(secret string) *Service {
	return &Service{secret: []byte(secret), cfg: Config{AppTokenTTL: 12 * time.Hour}, now: time.Now}
}

func forge(s *Service, claims any) string {
	payload, _ := json.Marshal(claims)
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(s.sign(payload))
}

func TestAppTokenRoundTrip(t *testing.T) {
	s := tokenService(strings.Repeat("k", 32))
	uid := uuid.New()
	now := time.Now()
	tok, exp, err := s.IssueAppToken(uid, AppDriver, now)
	if err != nil {
		t.Fatal(err)
	}
	if d := exp.Sub(now); d < 12*time.Hour-time.Second || d > 12*time.Hour {
		t.Fatalf("unexpected expiry %v", d)
	}
	p, gotExp, err := s.verifyAppToken(tok, now)
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != uid || p.App != AppDriver || !gotExp.Equal(exp) || p.TelegramID != 0 {
		t.Fatalf("unexpected principal %+v exp %v", p, gotExp)
	}
	payload, _ := b64.DecodeString(strings.Split(tok, ".")[0])
	var claims map[string]any
	_ = json.Unmarshal(payload, &claims)
	if claims["v"] != float64(1) || claims["uid"] != uid.String() || claims["app"] != "driver" || len(claims) != 4 {
		t.Fatalf("unexpected payload %s", payload)
	}
}

func TestAppTokenRejections(t *testing.T) {
	s := tokenService(strings.Repeat("k", 32))
	other := tokenService(strings.Repeat("x", 32))
	uid := uuid.New()
	now := time.Now()
	valid, _, _ := s.IssueAppToken(uid, AppPassenger, now)
	parts := strings.Split(valid, ".")
	driverPayload := b64.EncodeToString([]byte(`{"v":1,"uid":"` + uid.String() + `","app":"driver","exp":` + jsonInt(now.Add(time.Hour).Unix()) + `}`))
	expired, _, _ := s.IssueAppToken(uid, AppPassenger, now.Add(-13*time.Hour))
	foreign, _, _ := other.IssueAppToken(uid, AppPassenger, now)

	tests := []struct {
		name    string
		token   string
		wantErr error
	}{
		{"empty", "", nil},
		{"garbage", "abc", nil},
		{"three parts", valid + ".x", nil},
		{"payload swapped (app escalation)", driverPayload + "." + parts[1], nil},
		{"signature tampered", parts[0] + "." + b64.EncodeToString([]byte("nope")), nil},
		{"signed with another secret", foreign, nil},
		{"expired", expired, ErrTokenExpired},
		{"wrong version", forge(s, map[string]any{"v": 2, "uid": uid.String(), "app": "passenger", "exp": now.Add(time.Hour).Unix()}), nil},
		{"unknown app", forge(s, map[string]any{"v": 1, "uid": uid.String(), "app": "admin", "exp": now.Add(time.Hour).Unix()}), nil},
		{"bad uid", forge(s, map[string]any{"v": 1, "uid": "x", "app": "passenger", "exp": now.Add(time.Hour).Unix()}), nil},
		{"nil uid", forge(s, map[string]any{"v": 1, "uid": uuid.Nil.String(), "app": "passenger", "exp": now.Add(time.Hour).Unix()}), nil},
		{"extra claim", forge(s, map[string]any{"v": 1, "uid": uid.String(), "app": "passenger", "exp": now.Add(time.Hour).Unix(), "admin": true}), nil},
		{"oversized", strings.Repeat("a", 2000), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.VerifyAppToken(tt.token)
			if err == nil {
				t.Fatal("expected rejection")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if e := httpx.AsError(err); e.Status != http.StatusUnauthorized {
				t.Fatalf("status = %d", e.Status)
			}
		})
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestCSRFTokenIsPerSessionAndSecret(t *testing.T) {
	a := tokenService(strings.Repeat("k", 32))
	b := tokenService(strings.Repeat("x", 32))
	s1, s2 := uuid.New(), uuid.New()
	if a.CSRFToken(s1) == a.CSRFToken(s2) || a.CSRFToken(s1) == b.CSRFToken(s1) || a.CSRFToken(s1) != a.CSRFToken(s1) {
		t.Fatal("CSRF token must be deterministic per (secret, session) and differ otherwise")
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if a.validCSRF(r, s1) {
		t.Fatal("missing header accepted")
	}
	r.Header.Set(CSRFHeader, a.CSRFToken(s2))
	if a.validCSRF(r, s1) {
		t.Fatal("token of another session accepted")
	}
	r.Header.Set(CSRFHeader, a.CSRFToken(s1))
	if !a.validCSRF(r, s1) {
		t.Fatal("valid token rejected")
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
		ok     bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"BEARER  abc ", "abc", true},
		{"Basic abc", "", false},
		{"Bearer", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if tt.header != "" {
			r.Header.Set("Authorization", tt.header)
		}
		got, ok := bearerToken(r)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%q: got %q,%v want %q,%v", tt.header, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCleanName(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"  Әлия  ", 64, "Әлия"},
		{"A‮evil", 64, "Aevil"},
		{"a\x00b\tc\n d", 64, "ab c d"},
		{"Қанат   Серік", 64, "Қанат Серік"},
		{strings.Repeat("ұ", 70), 64, strings.Repeat("ұ", 64)},
		{"abc def", 4, "abc"},
		{"\xff\xfeok", 64, "ok"},
	}
	for _, tt := range tests {
		if got := cleanName(tt.in, tt.max); got != tt.want {
			t.Errorf("cleanName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPasswordProblem(t *testing.T) {
	tests := map[string]string{
		"":                             "required",
		"short":                        "too_short",
		"elevenchars":                  "too_short",
		"twelve chars":                 "",
		"Қазақстан-2026":               "",
		"            ":                 "invalid",
		strings.Repeat("a", 257):       "too_long",
		strings.Repeat("ә", 200) + "x": "",
	}
	for pw, want := range tests {
		if got := passwordProblem(pw); got != want {
			t.Errorf("passwordProblem(%d chars) = %q, want %q", len([]rune(pw)), got, want)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	tests := []struct{ in, want, reason string }{
		{" Admin@JanTaxi.kz ", "admin@jantaxi.kz", ""},
		{"", "", "required"},
		{"not-an-email", "", "invalid"},
		{"Name <a@b.kz>", "", "invalid"},
		{"a@localhost", "", "invalid"},
		{strings.Repeat("a", 250) + "@b.kz", "", "too_long"},
	}
	for _, tt := range tests {
		got, reason := normalizeEmail(tt.in)
		if got != tt.want || reason != tt.reason {
			t.Errorf("normalizeEmail(%q) = %q,%q want %q,%q", tt.in, got, reason, tt.want, tt.reason)
		}
	}
}

func TestStatusCacheTTL(t *testing.T) {
	c := newStatusCache(10 * time.Second)
	id := uuid.New()
	t0 := time.Now()
	c.put(id, userStatus{telegramID: 1, blocked: false, at: t0})
	if _, ok := c.get(id, t0.Add(5*time.Second)); !ok {
		t.Fatal("fresh entry missing")
	}
	if _, ok := c.get(id, t0.Add(10*time.Second)); ok {
		t.Fatal("stale entry returned")
	}
	c.invalidate(id)
	if _, ok := c.get(id, t0); ok {
		t.Fatal("invalidated entry returned")
	}
}
