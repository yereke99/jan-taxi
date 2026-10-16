package botapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "s3cret", BotDriver)
	c.RetryBase = time.Millisecond
	return c
}

func TestClientHeadersAndDecoding(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s3cret" || r.Header.Get("X-Jantaxi-Bot") != "driver" {
			t.Errorf("headers: %v", r.Header)
		}
		switch r.URL.Path {
		case "/internal/v1/bots/driver/lease":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["instance_id"] != "i-1" || in["ttl_s"] != float64(30) || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("lease body: %v", in)
			}
			_, _ = io.WriteString(w, `{"acquired":true,"last_update_id":41,"lease_expires_at":"2026-10-16T10:00:00Z"}`)
		case "/internal/v1/driver/77/orders":
			if r.URL.Query().Get("limit") != "5" {
				t.Errorf("limit: %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"items":[{"id":"o1","number":1042,"fare":800,"stops":[{"seq":0,"kind":"pickup","lat":48.8,"lon":58.1,"address":"A"}],"distance_to_pickup_m":650}],"eligibility":{"eligible":true,"reason":null}}`)
		case "/internal/v1/driver/77/location":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	ctx := context.Background()
	l, err := c.AcquireLease(ctx, "i-1", 30*time.Second)
	if err != nil || !l.Acquired || l.LastUpdateID != 41 || l.LeaseExpiresAt == nil {
		t.Fatalf("lease: %+v %v", l, err)
	}
	o, err := c.DriverOrders(ctx, 77, 5)
	if err != nil || len(o.Items) != 1 || o.Items[0].Number != 1042 || *o.Items[0].DistanceToPickupM != 650 || !o.Eligibility.Eligible {
		t.Fatalf("orders: %+v %v", o, err)
	}
	if err := c.ReportLocation(ctx, 77, LocationReport{Lat: 48.8, Lon: 58.1, Live: true}); err != nil {
		t.Fatalf("location: %v", err)
	}
}

func TestClientErrorDecoding(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":{"code":"insufficient_balance","message":"low","details":{"required":50,"available":10}}}`)
	})
	_, err := c.SetAvailability(context.Background(), 77, true)
	e, ok := AsError(err)
	if !ok || e.Status != 402 || e.Code != "insufficient_balance" || !IsCode(err, "conflict", "insufficient_balance") {
		t.Fatalf("error: %#v", err)
	}
	if n, ok := e.DetailInt64("required"); !ok || n != 50 {
		t.Fatalf("details: %v", e.Details)
	}
	if IsTransient(err) {
		t.Fatal("402 is not transient")
	}
	if ErrorCode(errors.New("x")) != "" || IsCode(errors.New("x"), "") {
		t.Fatal("plain errors carry no code")
	}
}

func TestClientRetriesIdempotentCalls(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "<html>bad gateway</html>")
			return
		}
		_, _ = io.WriteString(w, `{"claimed":true}`)
	})
	ok, err := c.ClaimUpdate(context.Background(), 9)
	if err != nil || !ok || calls.Load() != 3 {
		t.Fatalf("claim after retries: %v %v calls=%d", ok, err, calls.Load())
	}

	// Non-idempotent location reports are sent once.
	calls.Store(0)
	c2 := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	err = c2.ReportLocation(context.Background(), 1, LocationReport{})
	if e, ok := AsError(err); !ok || e.Code != "http_503" || !IsTransient(err) || calls.Load() != 1 {
		t.Fatalf("location: %v calls=%d", err, calls.Load())
	}

	// 4xx responses are not retried.
	calls.Store(0)
	c3 := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":"forbidden","message":"no","details":{"reason":"contact_mismatch"}}}`)
	})
	_, err = c3.VerifyContact(context.Background(), 1, "77071234567", 2)
	if e, _ := AsError(err); e == nil || e.DetailString("reason") != "contact_mismatch" || calls.Load() != 1 {
		t.Fatalf("contact: %v calls=%d", err, calls.Load())
	}
}

func TestClientNetworkErrorIsTransientAndSecretFree(t *testing.T) {
	c := New("http://127.0.0.1:1", "very-secret-value", BotPassenger)
	c.RetryBase = time.Millisecond
	_, err := c.Config(context.Background())
	if err == nil || !IsTransient(err) {
		t.Fatalf("network error: %v", err)
	}
	if strings.Contains(err.Error(), "very-secret-value") {
		t.Fatal("error leaks the secret")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Config(ctx); IsTransient(err) {
		t.Fatalf("cancellation must not be transient: %v", err)
	}
}
