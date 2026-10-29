// Package logging builds the process logger. Every record passes through a
// redaction step so that tokens, secrets, passwords, Telegram initData and
// cookies never reach the log output, even when a caller logs them by mistake.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// Redacted replaces sensitive values in log output.
const Redacted = "[redacted]"

// sensitiveKeys are matched case-insensitively as substrings of attribute keys
// and group names.
var sensitiveKeys = []string{
	"token", "secret", "password", "passwd", "init_data", "initdata",
	"authorization", "cookie", "api_key", "apikey",
	"verification_key", "iin", "document_number", "ocr_text",
}

// botTokenRe matches Telegram bot tokens, which appear inside Bot API URLs and
// therefore inside *url.Error messages returned by net/http.
var botTokenRe = regexp.MustCompile(`[0-9]{6,}:[A-Za-z0-9_-]{30,}`)

// New returns a logger writing to stderr. Unknown levels fall back to info and
// unknown formats to JSON.
func New(level, format string) *slog.Logger { return NewWriter(os.Stderr, level, format) }

// NewWriter is New with an explicit destination (used by tests).
func NewWriter(w io.Writer, level, format string) *slog.Logger {
	lvl, err := ParseLevel(level)
	if err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl, ReplaceAttr: ReplaceAttr}
	var h slog.Handler
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}

// ParseLevel parses debug, info, warn(ing) or error.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q", s)
	}
}

// ReplaceAttr is the slog.HandlerOptions.ReplaceAttr hook: it hides values of
// sensitive keys (or of any attribute inside a sensitive group) and scrubs bot
// tokens from string, error and Stringer values.
func ReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	for _, g := range groups {
		if IsSensitiveKey(g) {
			return slog.String(a.Key, Redacted)
		}
	}
	switch a.Value.Kind() {
	case slog.KindString:
		if s := a.Value.String(); mayContainToken(s) {
			return slog.String(a.Key, Scrub(s))
		}
	case slog.KindAny:
		switch v := a.Value.Any().(type) {
		case error:
			return slog.String(a.Key, Scrub(v.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, Scrub(v.String()))
		}
	}
	return a
}

// IsSensitiveKey reports whether values under key must not be logged.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// Scrub removes Telegram bot tokens from s.
func Scrub(s string) string {
	if !mayContainToken(s) {
		return s
	}
	return botTokenRe.ReplaceAllString(s, Redacted)
}

func mayContainToken(s string) bool { return len(s) >= 37 && strings.IndexByte(s, ':') >= 0 }

// ScrubError wraps err so that its message has bot tokens removed while
// errors.Is/As still reach the original error.
func ScrubError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if clean := Scrub(msg); clean != msg {
		return &scrubbedError{msg: clean, err: err}
	}
	return err
}

type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }
