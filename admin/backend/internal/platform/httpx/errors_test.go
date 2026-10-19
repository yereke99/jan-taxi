package httpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteErrorLogging: server failures are logged as errors, but a request
// the client abandoned (its context cancelled) is not a server failure.
func TestWriteErrorLogging(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		cancel    bool
		wantError bool
	}{
		{"server failure", errors.New("db down"), false, true},
		{"client went away", fmt.Errorf("routing: %w", context.Canceled), true, false},
		{"cancelled inside a live request", fmt.Errorf("job: %w", context.Canceled), false, true},
		{"client error", BadRequest("bad"), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
			ctx, cancel := context.WithCancel(WithLogger(context.Background(), log))
			defer cancel()
			if tc.cancel {
				cancel()
			}
			r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/routes/preview", nil)
			w := httptest.NewRecorder()
			WriteError(w, r, tc.err)
			if got := strings.Contains(buf.String(), "level=ERROR"); got != tc.wantError {
				t.Fatalf("logged error = %v, want %v: %s", got, tc.wantError, buf.String())
			}
		})
	}
}
