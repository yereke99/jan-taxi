package realtime

import (
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// defaultClientBuffer is how many undelivered messages a client may have queued
// before it is considered too slow and disconnected.
const defaultClientBuffer = 64

// Reasons a subscription ended.
const (
	ReasonClosed   = "closed"
	ReasonSlow     = "slow_consumer"
	ReasonShutdown = "server_shutdown"
)

// Hub fans NOTIFY payloads out to local subscribers by scope. Delivery never
// blocks: a subscriber whose buffer is full is evicted.
type Hub struct {
	log       *slog.Logger
	bufSize   int
	running   atomic.Bool
	listening atomic.Bool

	mu      sync.RWMutex
	byScope map[string]map[*Subscription]struct{}
	all     map[*Subscription]struct{}
	closed  bool
}

// NewHub creates an idle hub; start Run to receive events.
func NewHub(log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		log:     log.With(slog.String("component", "realtime")),
		bufSize: defaultClientBuffer,
		byScope: make(map[string]map[*Subscription]struct{}),
		all:     make(map[*Subscription]struct{}),
	}
}

// Listening reports whether the LISTEN connection is currently up.
func (h *Hub) Listening() bool { return h.listening.Load() }

// ErrNotListening is reported by Ready while the listener is reconnecting.
var ErrNotListening = errors.New("realtime: LISTEN connection is down")

// Ready is the readiness check: it fails while Run is active but its LISTEN
// connection is down. Before Run starts (the server is not serving yet) it passes.
func (h *Hub) Ready() error {
	if h.running.Load() && !h.listening.Load() {
		return ErrNotListening
	}
	return nil
}

// ClientCount returns the number of active subscriptions.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.all)
}

// Subscription receives encoded client messages for its scopes.
type Subscription struct {
	hub    *Hub
	ch     chan []byte
	done   chan struct{}
	once   sync.Once
	reason atomic.Value // string
	scopes []string     // guarded by hub.mu
}

// C delivers encoded messages ({"type","id","ts","data"}).
func (s *Subscription) C() <-chan []byte { return s.ch }

// Done is closed when the subscription ends (closed, evicted or hub shutdown).
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Reason reports why the subscription ended ("" while active).
func (s *Subscription) Reason() string {
	r, _ := s.reason.Load().(string)
	return r
}

// Close unsubscribes.
func (s *Subscription) Close() { s.end(ReasonClosed) }

func (s *Subscription) end(reason string) {
	s.once.Do(func() {
		s.reason.Store(reason)
		close(s.done)
		s.hub.remove(s)
	})
}

// Subscribe registers a subscriber for scopes.
func (h *Hub) Subscribe(scopes []string) *Subscription {
	s := &Subscription{hub: h, ch: make(chan []byte, h.bufSize), done: make(chan struct{})}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		s.end(ReasonShutdown)
		return s
	}
	h.all[s] = struct{}{}
	h.setScopesLocked(s, scopes)
	h.mu.Unlock()
	return s
}

// SetScopes replaces the subscription's scopes (e.g. a driver profile created
// while connected).
func (s *Subscription) SetScopes(scopes []string) {
	h := s.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.all[s]; ok {
		h.setScopesLocked(s, scopes)
	}
}

// Scopes returns a copy of the current scopes.
func (s *Subscription) Scopes() []string {
	s.hub.mu.RLock()
	defer s.hub.mu.RUnlock()
	return slices.Clone(s.scopes)
}

func (h *Hub) setScopesLocked(s *Subscription, scopes []string) {
	for _, sc := range s.scopes {
		h.unindexLocked(sc, s)
	}
	s.scopes = slices.Compact(slices.Sorted(slices.Values(scopes)))
	for _, sc := range s.scopes {
		m := h.byScope[sc]
		if m == nil {
			m = make(map[*Subscription]struct{})
			h.byScope[sc] = m
		}
		m[s] = struct{}{}
	}
}

func (h *Hub) unindexLocked(scope string, s *Subscription) {
	if m := h.byScope[scope]; m != nil {
		delete(m, s)
		if len(m) == 0 {
			delete(h.byScope, scope)
		}
	}
}

func (h *Hub) remove(s *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sc := range s.scopes {
		h.unindexLocked(sc, s)
	}
	delete(h.all, s)
}

// outbound is what clients receive for an event.
type outbound struct {
	Type string          `json:"type"`
	ID   uuid.UUID       `json:"id"`
	TS   time.Time       `json:"ts"`
	Data json.RawMessage `json:"data"`
}

// dispatch delivers one NOTIFY payload to every subscriber of its scopes.
func (h *Hub) dispatch(payload []byte) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil || env.Type == "" {
		h.log.Warn("dropping malformed realtime payload", slog.Int("bytes", len(payload)))
		return
	}
	if len(env.Data) == 0 {
		env.Data = json.RawMessage(`{}`)
	}
	msg, err := json.Marshal(outbound{Type: env.Type, ID: env.ID, TS: env.TS, Data: env.Data})
	if err != nil {
		h.log.Warn("dropping unencodable realtime event", slog.String("type", env.Type))
		return
	}
	h.mu.RLock()
	targets := make(map[*Subscription]struct{})
	for _, sc := range env.Scopes {
		for s := range h.byScope[sc] {
			targets[s] = struct{}{}
		}
	}
	h.mu.RUnlock()
	h.send(targets, msg)
}

// broadcast sends msg to every subscriber.
func (h *Hub) broadcast(msg []byte) {
	h.mu.RLock()
	targets := make(map[*Subscription]struct{}, len(h.all))
	for s := range h.all {
		targets[s] = struct{}{}
	}
	h.mu.RUnlock()
	h.send(targets, msg)
}

func (h *Hub) send(targets map[*Subscription]struct{}, msg []byte) {
	var slow []*Subscription
	for s := range targets {
		select {
		case <-s.done:
		case s.ch <- msg:
		default:
			slow = append(slow, s)
		}
	}
	for _, s := range slow {
		s.end(ReasonSlow)
	}
	if len(slow) > 0 {
		h.log.Info("evicted slow realtime clients", slog.Int("count", len(slow)))
	}
}

// shutdown ends every subscription and refuses new ones.
func (h *Hub) shutdown() {
	h.mu.Lock()
	h.closed = true
	subs := make([]*Subscription, 0, len(h.all))
	for s := range h.all {
		subs = append(subs, s)
	}
	h.mu.Unlock()
	for _, s := range subs {
		s.end(ReasonShutdown)
	}
}
