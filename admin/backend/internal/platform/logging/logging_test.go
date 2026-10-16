package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

const botToken = "123456789:AAH_abcdefghijklmnopqrstuvwxyz-0123456"

func TestRedactsSensitiveKeys(t *testing.T) {
	var buf bytes.Buffer
	log := NewWriter(&buf, "debug", "json")
	log.Info("login",
		slog.String("token", "tok-value"),
		slog.String("SessionSecret", "sec-value"),
		slog.String("admin_password", "pw-value"),
		slog.String("init_data", "query_id=1&hash=abc"),
		slog.String("Authorization", "Bearer xyz"),
		slog.String("cookie", "jt_admin=zzz"),
		slog.Group("headers", slog.String("Cookie", "a=b"), slog.String("accept", "json")),
		slog.Group("set_cookie", slog.String("jt_admin", "group-hidden")),
		slog.String("user", "visible"),
	)
	out := buf.String()
	for _, leaked := range []string{"tok-value", "sec-value", "pw-value", "query_id=1", "Bearer xyz", "jt_admin=zzz", "a=b", "group-hidden"} {
		if strings.Contains(out, leaked) {
			t.Errorf("log leaks %q: %s", leaked, out)
		}
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if rec["user"] != "visible" || rec["token"] != Redacted {
		t.Fatalf("unexpected record: %v", rec)
	}
	if h, _ := rec["headers"].(map[string]any); h["accept"] != "json" || h["Cookie"] != Redacted {
		t.Fatalf("group handling: %v", rec["headers"])
	}
}

func TestScrubsBotTokens(t *testing.T) {
	var buf bytes.Buffer
	log := NewWriter(&buf, "info", "text")
	apiURL := "https://api.telegram.org/bot" + botToken + "/sendMessage"
	u, _ := url.Parse(apiURL)
	urlErr := &url.Error{Op: "Post", URL: apiURL, Err: errors.New("connection refused")}

	log.Info("send failed "+botToken,
		slog.String("url", apiURL),
		slog.Any("err", fmt.Errorf("telegram: %w", urlErr)),
		slog.Any("endpoint", u),
	)
	out := buf.String()
	if strings.Contains(out, "AAH_abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("bot token leaked: %s", out)
	}
	if !strings.Contains(out, "connection refused") || !strings.Contains(out, "/sendMessage") {
		t.Fatalf("scrubbing removed too much: %s", out)
	}
}

func TestScrubKeepsOrdinaryText(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"order 1042 accepted at 12:30", "order 1042 accepted at 12:30"},
		{"bot" + botToken + "/getMe", "bot" + Redacted + "/getMe"},
		{"short 123456:abc", "short 123456:abc"},
	}
	for _, tc := range tests {
		if got := Scrub(tc.in); got != tc.want {
			t.Errorf("Scrub(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestScrubErrorKeepsChain(t *testing.T) {
	base := errors.New("dial tcp: refused")
	err := ScrubError(fmt.Errorf("POST https://api.telegram.org/bot%s/x: %w", botToken, base))
	if strings.Contains(err.Error(), botToken) {
		t.Fatal("token not scrubbed")
	}
	if !errors.Is(err, base) {
		t.Fatal("errors.Is must still reach the cause")
	}
	if ScrubError(nil) != nil {
		t.Fatal("nil in, nil out")
	}
}

func TestLevels(t *testing.T) {
	var buf bytes.Buffer
	log := NewWriter(&buf, "warn", "json")
	log.Info("hidden")
	log.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Fatalf("level filtering: %s", buf.String())
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("unknown level must error")
	}
	for _, s := range []string{"debug", "INFO", "warning", "error", ""} {
		if _, err := ParseLevel(s); err != nil {
			t.Errorf("ParseLevel(%q): %v", s, err)
		}
	}
}
