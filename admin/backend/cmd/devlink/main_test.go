package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

const testToken = "1000000001:TESTpassengerTOKENaaaaaaaaaaaaaaaaaaa"

func env(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

// verify re-implements the server-side check (ARCHITECTURE §4.1) independently.
func verify(t *testing.T, raw, token string) url.Values {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	hash := q.Get("hash")
	q.Del("hash")
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(k + "=" + q.Get(k))
	}
	s := hmac.New(sha256.New, []byte("WebAppData"))
	s.Write([]byte(token))
	m := hmac.New(sha256.New, s.Sum(nil))
	m.Write([]byte(b.String()))
	if !hmac.Equal([]byte(hex.EncodeToString(m.Sum(nil))), []byte(hash)) {
		t.Fatal("initData hash does not verify")
	}
	return q
}

func TestLinkIsSignedAndWellFormed(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var out, errOut bytes.Buffer
	code := run([]string{"--app", "passenger", "--tg-id", "4242", "--name", "Айгерім Test", "--start-param", "taxi"},
		env(map[string]string{"PASSENGER_BOT_TOKEN": testToken}), &out, &errOut, now)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	link := strings.TrimSpace(out.String())
	if strings.Contains(out.String()+errOut.String(), testToken) {
		t.Fatal("the bot token must never be printed")
	}
	base, frag, ok := strings.Cut(link, "#")
	if !ok || base != "http://localhost:5173/passenger/" {
		t.Fatalf("link base: %q", link)
	}
	params, err := url.ParseQuery(frag)
	if err != nil {
		t.Fatal(err)
	}
	if params.Get("tgWebAppVersion") != "8.0" || params.Get("tgWebAppPlatform") != "ios" || params.Get("tgWebAppStartParam") != "taxi" {
		t.Fatalf("launch params: %v", params)
	}
	var theme map[string]string
	if err := json.Unmarshal([]byte(params.Get("tgWebAppThemeParams")), &theme); err != nil || theme["bg_color"] != "#ffffff" {
		t.Fatalf("theme: %v %v", theme, err)
	}

	q := verify(t, params.Get("tgWebAppData"), testToken)
	if q.Get("auth_date") != "1792152000" || q.Get("start_param") != "taxi" || q.Get("signature") == "" {
		t.Fatalf("fields: %v", q)
	}
	var user initUser
	if err := json.Unmarshal([]byte(q.Get("user")), &user); err != nil || user.ID != 4242 || user.FirstName != "Айгерім Test" {
		t.Fatalf("user: %+v %v", user, err)
	}
}

func TestDriverDefaultsAndInitDataOnly(t *testing.T) {
	var out bytes.Buffer
	driverToken := "1000000002:TESTdriverTOKENbbbbbbbbbbbbbbbbbbbbbbbb"
	code := run([]string{"--app", "driver", "--init-data"}, env(map[string]string{"DRIVER_BOT_TOKEN": driverToken}),
		&out, &bytes.Buffer{}, time.Now())
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	verify(t, strings.TrimSpace(out.String()), driverToken)
}

func TestRefusals(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"production", nil, map[string]string{"APP_ENV": "Production", "PASSENGER_BOT_TOKEN": testToken}, "APP_ENV=production"},
		{"missing token", nil, map[string]string{}, "PASSENGER_BOT_TOKEN is not set"},
		{"bad app", []string{"--app", "admin"}, map[string]string{"PASSENGER_BOT_TOKEN": testToken}, "--app"},
		{"bad start param", []string{"--start-param", "order 1"}, map[string]string{"PASSENGER_BOT_TOKEN": testToken}, "--start-param"},
		{"remote base", []string{"--base", "https://app.jantaxi.kz/passenger/"}, map[string]string{"PASSENGER_BOT_TOKEN": testToken}, "local development host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			code := run(tc.args, env(tc.env), &bytes.Buffer{}, &errOut, time.Now())
			if code == 0 || !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("code=%d stderr=%q, want mention of %q", code, errOut.String(), tc.want)
			}
		})
	}
}

func TestLocalHosts(t *testing.T) {
	for h, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "::1": true, "app.localhost": true, "jantaxi.test": true,
		"192.168.1.10": false, "app.jantaxi.kz": false,
	} {
		if isLocalHost(h) != want {
			t.Errorf("isLocalHost(%q) != %v", h, want)
		}
	}
}
