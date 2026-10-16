package realtime

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func quietHub() *Hub { return NewHub(slog.New(slog.DiscardHandler)) }

func TestEncodeValidation(t *testing.T) {
	uid := uuid.New()
	tests := []struct {
		name    string
		ev      Event
		wantErr bool
		tooBig  bool
	}{
		{"valid user event", Event{Type: OrderAccepted, Scopes: []string{UserScope(uid), AdminScope}, Data: map[string]any{"order_id": uid}}, false, false},
		{"nil data", Event{Type: WalletUpdated, Scopes: []string{DriverScope(uid)}}, false, false},
		{"empty type", Event{Type: "", Scopes: []string{AdminScope}}, true, false},
		{"bad type", Event{Type: "Order Accepted", Scopes: []string{AdminScope}}, true, false},
		{"no scopes", Event{Type: OrderCreated}, true, false},
		{"unknown scope kind", Event{Type: OrderCreated, Scopes: []string{"city:" + uid.String()}}, true, false},
		{"non-canonical uuid", Event{Type: OrderCreated, Scopes: []string{"user:" + strings.ToUpper(uid.String())}}, true, false},
		{"bad uuid", Event{Type: OrderCreated, Scopes: []string{"user:123"}}, true, false},
		{"unencodable data", Event{Type: OrderCreated, Scopes: []string{AdminScope}, Data: func() {}}, true, false},
		{"too large", Event{Type: OrderCreated, Scopes: []string{AdminScope}, Data: map[string]string{"x": strings.Repeat("a", MaxPayloadBytes)}}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := encode(tt.ev, uuid.New(), time.Now())
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tt.tooBig != errors.Is(err, ErrPayloadTooLarge) {
					t.Fatalf("ErrPayloadTooLarge mismatch: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var env map[string]any
			if err := json.Unmarshal(payload, &env); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"type", "id", "ts", "scopes", "data"} {
				if _, ok := env[k]; !ok {
					t.Fatalf("missing %q in %s", k, payload)
				}
			}
			if _, err := time.Parse(time.RFC3339, env["ts"].(string)); err != nil {
				t.Fatalf("ts not RFC 3339: %v", err)
			}
			if _, ok := env["data"].(map[string]any); !ok {
				t.Fatalf("data must be an object: %s", payload)
			}
		})
	}
}

func payloadFor(t *testing.T, ev Event) []byte {
	t.Helper()
	b, err := encode(ev, uuid.New(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func recv(t *testing.T, s *Subscription) map[string]any {
	t.Helper()
	select {
	case msg := <-s.C():
		var m map[string]any
		if err := json.Unmarshal(msg, &m); err != nil {
			t.Fatal(err)
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

func expectNone(t *testing.T, s *Subscription, wait time.Duration) {
	t.Helper()
	select {
	case msg := <-s.C():
		t.Fatalf("unexpected message: %s", msg)
	case <-time.After(wait):
	}
}

func TestDispatchScopeIsolation(t *testing.T) {
	h := quietHub()
	a, b, d := uuid.New(), uuid.New(), uuid.New()
	subA := h.Subscribe([]string{UserScope(a)})
	subB := h.Subscribe([]string{UserScope(b)})
	subDriver := h.Subscribe([]string{UserScope(b), DriverScope(d)})
	subAdmin := h.Subscribe([]string{AdminScope})
	defer subA.Close()
	defer subB.Close()
	defer subDriver.Close()
	defer subAdmin.Close()

	h.dispatch(payloadFor(t, Event{Type: OrderAccepted, Scopes: []string{UserScope(a), AdminScope}, Data: map[string]string{"order_id": "o1"}}))
	m := recv(t, subA)
	if m["type"] != OrderAccepted || m["data"].(map[string]any)["order_id"] != "o1" || m["id"] == "" || m["ts"] == "" {
		t.Fatalf("unexpected message: %v", m)
	}
	if _, leaked := m["scopes"]; leaked {
		t.Fatal("scopes must not be forwarded to clients")
	}
	recv(t, subAdmin)
	expectNone(t, subB, 50*time.Millisecond)
	expectNone(t, subDriver, 10*time.Millisecond)

	// A subscriber matching several scopes of one event gets it once.
	h.dispatch(payloadFor(t, Event{Type: WalletUpdated, Scopes: []string{UserScope(b), DriverScope(d)}}))
	recv(t, subDriver)
	expectNone(t, subDriver, 50*time.Millisecond)
	recv(t, subB)
	expectNone(t, subA, 10*time.Millisecond)

	// Scopes can change while connected.
	subA.SetScopes([]string{DriverScope(d)})
	h.dispatch(payloadFor(t, Event{Type: OrderCreated, Scopes: []string{UserScope(a)}}))
	expectNone(t, subA, 50*time.Millisecond)
	h.dispatch(payloadFor(t, Event{Type: OrderAvailable, Scopes: []string{DriverScope(d)}}))
	if recv(t, subA)["type"] != OrderAvailable {
		t.Fatal("re-scoped subscriber missed event")
	}

	// Malformed payloads are ignored.
	h.dispatch([]byte(`not json`))
	expectNone(t, subAdmin, 20*time.Millisecond)
}

func TestSlowClientIsEvicted(t *testing.T) {
	h := quietHub()
	h.bufSize = 2
	scope := UserScope(uuid.New())
	slow := h.Subscribe([]string{scope})
	fast := h.Subscribe([]string{scope})
	defer fast.Close()

	ev := payloadFor(t, Event{Type: OrderStarted, Scopes: []string{scope}})
	for i := 0; i < 3; i++ {
		h.dispatch(ev)
		recv(t, fast) // the fast client keeps up
	}
	select {
	case <-slow.Done():
	case <-time.After(time.Second):
		t.Fatal("slow client was not evicted")
	}
	if slow.Reason() != ReasonSlow {
		t.Fatalf("reason = %q", slow.Reason())
	}
	if h.ClientCount() != 1 {
		t.Fatalf("evicted client still registered: %d", h.ClientCount())
	}
	// Further events neither block nor reach the evicted client's index.
	done := make(chan struct{})
	go func() { h.dispatch(ev); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatch blocked")
	}
	recv(t, fast)
}

func TestShutdownEndsSubscriptions(t *testing.T) {
	h := quietHub()
	s := h.Subscribe([]string{AdminScope})
	h.shutdown()
	select {
	case <-s.Done():
	default:
		t.Fatal("subscription still open after shutdown")
	}
	if s.Reason() != ReasonShutdown {
		t.Fatalf("reason = %q", s.Reason())
	}
	late := h.Subscribe([]string{AdminScope})
	if late.Reason() != ReasonShutdown || h.ClientCount() != 0 {
		t.Fatal("subscribe after shutdown must end immediately")
	}
}

func TestOriginPatterns(t *testing.T) {
	got := OriginPatterns([]string{"https://app.jantaxi.kz", "https://APP.jantaxi.kz/", "http://localhost:5173", "ftp://x", "", "not a url", "https://admin.jantaxi.kz/path"})
	want := []string{"https://app.jantaxi.kz", "http://localhost:5173", "https://admin.jantaxi.kz"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("OriginPatterns = %v, want %v", got, want)
	}
}
