package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
)

func TestSendHTMLAfterTheHandlerDeadlineStillAnswers(t *testing.T) {
	api := newScriptedBotAPI(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done() // an earlier API call used up the handler's time
	if err := SendHTML(ctx, api.client(), 7, "Қате орын алды", nil); err != nil {
		t.Fatalf("reply after the deadline: %v", err)
	}
	if api.count("sendMessage") != 1 {
		t.Fatal("reply not sent")
	}
	// Cancellation (not a deadline) is respected.
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if err := SendHTML(cctx, api.client(), 7, "x", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reply: %v", err)
	}
}

type countingConfig struct {
	calls atomic.Int32
	err   error
}

func (c *countingConfig) Config(ctx context.Context) (botapi.Config, error) {
	c.calls.Add(1)
	if c.err != nil {
		return botapi.Config{}, c.err
	}
	return botapi.Config{PassengerMiniAppURL: "https://app.example.kz/passenger/"}, nil
}

func TestConfigCacheWaitsAfterAFailure(t *testing.T) {
	src := &countingConfig{err: errors.New("connection refused")}
	c := NewConfigCache(src, 5*time.Minute, nil)
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()
	for range 5 { // e.g. one reply with several Mini App buttons
		if _, ok := c.Get(ctx); ok {
			t.Fatal("no value is known yet")
		}
	}
	if n := src.calls.Load(); n != 1 {
		t.Fatalf("%d fetches while the API is down, want 1", n)
	}
	now = now.Add(configRetryAfter + time.Second)
	src.err = nil
	if v, ok := c.Get(ctx); !ok || v.PassengerMiniAppURL == "" {
		t.Fatal("value after the API came back")
	}
	src.err = errors.New("down again")
	now = now.Add(10 * time.Minute)
	if v, ok := c.Get(ctx); !ok || v.PassengerMiniAppURL == "" {
		t.Fatal("the last known value must be kept when a refresh fails")
	}
	if n := src.calls.Load(); n != 3 {
		t.Fatalf("%d fetches, want 3", n)
	}
}

func TestPlaceLabelNeverShowsCoordinates(t *testing.T) {
	for in, want := range map[string]string{
		"Ембі, Абай көшесі 12": "Ембі, Абай көшесі 12",
		"  Базар ":             "Базар",
		"":                     "Картадағы нүкте",
		"48.83010, 58.15100":   "Картадағы нүкте",
		"48.8301,58.151":       "Картадағы нүкте",
		"-12.5; +45":           "Картадағы нүкте",
		"Үй 48, пәтер 12":      "Үй 48, пәтер 12",
	} {
		if got := PlaceLabel(in, "Картадағы нүкте"); got != want {
			t.Errorf("PlaceLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
