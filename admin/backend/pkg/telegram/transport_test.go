package telegram

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// rawServer is a TCP Bot API stand-in whose connections follow a script:
// each accepted request takes the next behaviour ("hang", "drop", "ok").
type rawServer struct {
	ln    net.Listener
	mu    sync.Mutex
	steps []string
	conns int
	done  chan struct{}
}

func newRawServer(t *testing.T, steps ...string) *rawServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &rawServer{ln: ln, steps: steps, done: make(chan struct{})}
	t.Cleanup(func() { close(s.done); _ = ln.Close() })
	go s.accept()
	return s
}

func (s *rawServer) url() string { return "http://" + s.ln.Addr().String() }

func (s *rawServer) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns++
		s.mu.Unlock()
		go s.serve(conn)
	}
}

func (s *rawServer) next() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steps) == 0 {
		return "ok"
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step
}

func (s *rawServer) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func (s *rawServer) serve(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, req.Body)
		switch s.next() {
		case "hang": // the network died: nothing ever comes back
			<-s.done
			return
		case "drop": // the connection breaks in the middle of the body
			_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 200\r\n\r\n{\"ok\":tr")
			return
		default:
			body := `{"ok":true,"result":[]}`
			if !strings.HasSuffix(req.URL.Path, "/getUpdates") {
				body = `{"ok":true,"result":true}`
			}
			_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
		}
	}
}

func TestHungConnectionHitsTheRequestDeadline(t *testing.T) {
	srv := newRawServer(t, "hang")
	c := New(testToken, WithBaseURL(srv.url()), WithRequestTimeout(200*time.Millisecond))

	start := time.Now()
	err := c.DeleteWebhook(context.Background(), false)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("hung request returned after %v, want about the 200ms deadline", elapsed)
	}
	if !IsNetworkError(err) || !IsRetryable(err) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung request: %v", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("token leaked: %v", err)
	}
	// The next call does not reuse the dead connection.
	if err := c.DeleteWebhook(context.Background(), false); err != nil {
		t.Fatalf("call after the hang: %v", err)
	}
	if n := srv.connections(); n != 2 {
		t.Fatalf("%d connections, want a fresh one after the hang", n)
	}
}

func TestGetUpdatesDeadlineIsPollTimeoutPlusRequestTimeout(t *testing.T) {
	srv := newRawServer(t, "hang", "hang")
	c := New(testToken, WithBaseURL(srv.url()), WithRequestTimeout(300*time.Millisecond))
	start := time.Now()
	_, err := c.GetUpdates(context.Background(), GetUpdatesParams{Timeout: 1})
	elapsed := time.Since(start)
	if !IsNetworkError(err) || elapsed < time.Second || elapsed > 3*time.Second {
		t.Fatalf("long poll on a dead connection: %v after %v, want a deadline of 1s+300ms", err, elapsed)
	}
	// A long poll that is cancelled by its caller is not a network error.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := c.GetUpdates(ctx, GetUpdatesParams{Timeout: 1}); IsNetworkError(err) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled poll: %v", err)
	}
	if _, err := c.GetUpdates(context.Background(), GetUpdatesParams{Timeout: 1}); err != nil {
		t.Fatalf("poll after recovery: %v", err)
	}
}

func TestDropMidResponseIsRetryableAndRecovers(t *testing.T) {
	srv := newRawServer(t, "drop")
	c := New(testToken, WithBaseURL(srv.url()))
	_, err := c.GetUpdates(context.Background(), GetUpdatesParams{})
	if !IsNetworkError(err) || !IsRetryable(err) {
		t.Fatalf("dropped response: %v", err)
	}
	if _, err := c.GetUpdates(context.Background(), GetUpdatesParams{}); err != nil {
		t.Fatalf("poll after the drop: %v", err)
	}
}

func TestOwnTransportsAreBoundedAndResettable(t *testing.T) {
	c := New(testToken)
	for name, pool := range map[string]*httpPool{"calls": c.http, "poll": c.poll} {
		tr, ok := pool.client().Transport.(*http.Transport)
		if !ok {
			t.Fatalf("%s: transport %T", name, pool.client().Transport)
		}
		if tr.TLSHandshakeTimeout <= 0 || tr.ResponseHeaderTimeout <= 0 || tr.IdleConnTimeout <= 0 || tr.DialContext == nil {
			t.Fatalf("%s: unbounded transport %+v", name, tr)
		}
		if tr.Protocols == nil || !tr.Protocols.HTTP1() || tr.Protocols.HTTP2() {
			t.Fatalf("%s: protocols %v, want HTTP/1.1 only", name, tr.Protocols)
		}
	}
	if poll := c.poll.client().Transport.(*http.Transport); poll.ResponseHeaderTimeout <= MaxPollTimeout*time.Second {
		t.Fatalf("poll response header timeout %v shorter than a long poll", poll.ResponseHeaderTimeout)
	}
	before := c.poll.client()
	c.ResetConnections()
	if c.poll.client() == before {
		t.Fatal("ResetConnections must replace the client's own transport")
	}

	// A client without a transport gets the package transports and keeps its timeout.
	c2 := New(testToken, WithHTTPClient(&http.Client{Timeout: 30 * time.Second}))
	if c2.http.client().Timeout != 30*time.Second || c2.http.client().Transport == nil || c2.poll.client().Timeout != 0 {
		t.Fatal("caller client without a transport")
	}
	// A caller transport is kept as is.
	custom := &http.Client{Transport: &http.Transport{}}
	c3 := New(testToken, WithHTTPClient(custom))
	c3.ResetConnections()
	if c3.http.client() != custom || c3.poll.client() != custom {
		t.Fatal("caller transport replaced")
	}
}

func TestResetChatMenuButtonAndPollClamp(t *testing.T) {
	f, c := newFake(t, func(w http.ResponseWriter, method string, _ map[string]any) {
		if method == "getUpdates" {
			writeJSON(w, 200, `{"ok":true,"result":[]}`)
			return
		}
		writeJSON(w, 200, `{"ok":true,"result":true}`)
	})
	if err := c.ResetChatMenuButton(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	b := f.last().body
	if f.last().path != "setChatMenuButton" || b["chat_id"] != float64(42) || b["menu_button"].(map[string]any)["type"] != "default" {
		t.Fatalf("reset body: %v", b)
	}
	if _, err := c.GetUpdates(context.Background(), GetUpdatesParams{Timeout: 600}); err != nil || f.last().body["timeout"] != float64(MaxPollTimeout) {
		t.Fatalf("long poll timeout not clamped: %v %v", err, f.last().body)
	}
	if _, err := c.GetUpdates(context.Background(), GetUpdatesParams{Timeout: -1}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.last().body["timeout"]; ok {
		t.Fatal("non-positive timeout must be omitted")
	}
}

// blackholeProxy forwards TCP connections to target until they are killed:
// a killed connection stays open but silently drops every byte, which is
// what a client sees after the network under it changed (Wi-Fi switch, NAT
// reset, laptop sleep). New connections work normally.
type blackholeProxy struct {
	ln       net.Listener
	target   string
	mu       sync.Mutex
	conns    []*atomic.Bool
	accepted int
}

func newBlackholeProxy(t *testing.T, target string) *blackholeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &blackholeProxy{ln: ln, target: target}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", target)
			if err != nil {
				_ = client.Close()
				continue
			}
			dead := new(atomic.Bool)
			p.mu.Lock()
			p.conns = append(p.conns, dead)
			p.accepted++
			p.mu.Unlock()
			go p.pipe(client, server, dead)
			go p.pipe(server, client, dead)
		}
	}()
	return p
}

func (p *blackholeProxy) pipe(src, dst net.Conn, dead *atomic.Bool) {
	defer src.Close()
	defer dst.Close()
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && !dead.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// killAll silences every connection opened so far.
func (p *blackholeProxy) killAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.conns {
		d.Store(true)
	}
}

func (p *blackholeProxy) connections() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accepted
}

func TestRecoversAfterTheNetworkSilentlyDropsConnections(t *testing.T) {
	var protoMu sync.Mutex
	protos := map[int]bool{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protoMu.Lock()
		protos[r.ProtoMajor] = true
		protoMu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	srv.EnableHTTP2 = true // like api.telegram.org
	srv.StartTLS()
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	proxy := newBlackholeProxy(t, srv.Listener.Addr().String())
	c := New(testToken, WithBaseURL("https://"+proxy.ln.Addr().String()),
		WithRequestTimeout(300*time.Millisecond), WithTLSConfig(&tls.Config{RootCAs: roots}))
	ctx := context.Background()

	if err := c.DeleteWebhook(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetUpdates(ctx, GetUpdatesParams{}); err != nil {
		t.Fatal(err)
	}
	proxy.killAll()
	before := proxy.connections()
	for _, call := range []struct {
		name string
		run  func() error
	}{
		{"deleteWebhook", func() error { return c.DeleteWebhook(ctx, false) }},
		{"getUpdates", func() error { _, err := c.GetUpdates(ctx, GetUpdatesParams{}); return err }},
	} {
		start := time.Now()
		err := call.run()
		if !IsNetworkError(err) || time.Since(start) > 2*time.Second {
			t.Fatalf("%s on a dead connection: %v after %v", call.name, err, time.Since(start))
		}
		// The very next call must not be sent over the dead connection again.
		if err := call.run(); err != nil {
			t.Fatalf("%s after the network came back: %v", call.name, err)
		}
	}
	if proxy.connections() <= before {
		t.Fatal("no new connection was opened after the drop")
	}
	protoMu.Lock()
	defer protoMu.Unlock()
	if protos[2] || !protos[1] {
		t.Fatalf("protocols used: %v, want HTTP/1.1 only", protos)
	}
}
