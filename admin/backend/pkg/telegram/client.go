// Package telegram is a small Bot API client used by the API's notification
// worker and by both long-polling bots. Errors are typed (see APIError and the
// Is* helpers) and never contain the bot token; request URLs are never logged.
package telegram

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL is the public Bot API endpoint.
	DefaultBaseURL = "https://api.telegram.org"

	// DefaultRequestTimeout is the deadline of one call; getUpdates gets its
	// long-poll timeout on top, so a healthy long poll never hits it.
	DefaultRequestTimeout = 15 * time.Second

	// MaxPollTimeout is the longest long poll (seconds) sent to Telegram.
	MaxPollTimeout = 50

	dialTimeout         = 10 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
	idleConnTimeout     = 60 * time.Second
	tcpKeepAlive        = 30 * time.Second

	// maxResponseBytes bounds a single response body (100 updates fit easily).
	maxResponseBytes = 32 << 20
)

// Client calls the Bot API for one bot token. It is safe for concurrent use.
type Client struct {
	token   string
	baseURL string
	timeout time.Duration
	tls     *tls.Config  // from WithTLSConfig
	custom  *http.Client // from WithHTTPClient
	http    *httpPool    // calls and file downloads
	poll    *httpPool    // getUpdates
	log     *slog.Logger
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the Bot API endpoint (tests, local Bot API server).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient sets the HTTP client. A client without a Transport gets the
// package's own transports (timeouts, HTTP/1.1, rebuilt by ResetConnections);
// a custom Transport is used as is for every call. Deadlines are applied per
// request, so the client must not set a Timeout shorter than a long poll.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.custom = h
		}
	}
}

// WithRequestTimeout sets the deadline of one call (default 15s); getUpdates
// gets its long-poll timeout on top.
func WithRequestTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithTLSConfig sets the TLS configuration of the client's own transports
// (e.g. a private CA of a local Bot API server).
func WithTLSConfig(cfg *tls.Config) Option {
	return func(c *Client) { c.tls = cfg }
}

// WithLogger sets the logger used for debug-level request tracing.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) {
		if l != nil {
			c.log = l
		}
	}
}

// New creates a client for token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: DefaultBaseURL,
		timeout: DefaultRequestTimeout,
		log:     slog.New(slog.DiscardHandler),
	}
	for _, o := range opts {
		o(c)
	}
	c.http, c.poll = newPools(c.custom, c.tls, c.timeout)
	return c
}

// ResetConnections drops every pooled connection and replaces the client's
// own transports, so the next call dials afresh. The bot runtime calls it
// after a stalled or repeatedly failing request.
func (c *Client) ResetConnections() {
	c.http.reset()
	if c.poll != c.http {
		c.poll.reset()
	}
}

// httpPool is an HTTP client that can be replaced while in use.
type httpPool struct {
	mu    sync.Mutex
	c     *http.Client
	build func() *http.Client // nil for a caller-supplied transport
}

func (p *httpPool) client() *http.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.c
}

// Do sends req with the current client.
func (p *httpPool) Do(req *http.Request) (*http.Response, error) { return p.client().Do(req) }

// dropIdle closes idle connections, which may be dead after a failure.
func (p *httpPool) dropIdle() { p.client().CloseIdleConnections() }

// reset starts a new transport (own transports only) and closes the idle
// connections of the old one; requests in flight end with their deadline.
func (p *httpPool) reset() {
	p.mu.Lock()
	old := p.c
	if p.build != nil {
		p.c = p.build()
	}
	p.mu.Unlock()
	old.CloseIdleConnections()
}

func newPools(custom *http.Client, tlsConfig *tls.Config, timeout time.Duration) (calls, poll *httpPool) {
	if custom != nil && custom.Transport != nil {
		p := &httpPool{c: custom}
		return p, p
	}
	callClient := func() *http.Client {
		hc := &http.Client{Transport: newTransport(tlsConfig, timeout+5*time.Second)}
		if custom != nil {
			hc.Timeout, hc.Jar, hc.CheckRedirect = custom.Timeout, custom.Jar, custom.CheckRedirect
		}
		return hc
	}
	pollClient := func() *http.Client {
		return &http.Client{Transport: newTransport(tlsConfig, MaxPollTimeout*time.Second+timeout)}
	}
	return &httpPool{c: callClient(), build: callClient}, &httpPool{c: pollClient(), build: pollClient}
}

// newTransport bounds every phase of a request. It speaks HTTP/1.1 only: a
// request that times out closes its own connection, so a connection that
// died with the network is never used again, whereas HTTP/2 would keep
// multiplexing new requests onto it.
func newTransport(tlsConfig *tls.Config, responseHeaderTimeout time.Duration) *http.Transport {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: tcpKeepAlive}
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       tlsConfig.Clone(),
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       idleConnTimeout,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
	}
	t.Protocols = new(http.Protocols)
	t.Protocols.SetHTTP1(true)
	return t
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call POSTs params as JSON to method and decodes the result into out (if non-nil).
// pollTimeout is the long-poll duration; the request deadline is pollTimeout
// plus the request timeout.
func (c *Client) call(ctx context.Context, method string, params any, out any, pollTimeout time.Duration) error {
	start := time.Now()
	pool := c.http
	if method == "getUpdates" {
		pool = c.poll
	}
	err := c.do(ctx, pool, method, params, out, pollTimeout)
	if err != nil {
		c.log.Debug("telegram call failed", slog.String("method", method),
			slog.Duration("dur", time.Since(start)), slog.String("err", err.Error()))
	} else {
		c.log.Debug("telegram call", slog.String("method", method), slog.Duration("dur", time.Since(start)))
	}
	return err
}

func (c *Client) do(ctx context.Context, pool *httpPool, method string, params any, out any, pollTimeout time.Duration) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram %s: encode request: %w", method, err)
	}
	return c.send(ctx, pool, method, body, "application/json", out, pollTimeout)
}

// send POSTs body with contentType to method and decodes the result into out.
func (c *Client) send(ctx context.Context, pool *httpPool, method string, body []byte, contentType string, out any, pollTimeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, pollTimeout+c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.transportError(method, err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	resp, err := pool.Do(req)
	if err != nil {
		return c.failed(pool, method, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return c.failed(pool, method, err)
	}

	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("telegram %s: %w", method, &APIError{
				Code:        resp.StatusCode,
				Description: http.StatusText(resp.StatusCode),
				RetryAfter:  headerRetryAfter(resp.Header),
			})
		}
		return &requestError{method: method, msg: "malformed response body", cause: err}
	}
	if !ar.OK {
		ae := &APIError{Code: ar.ErrorCode, Description: c.redact(ar.Description)}
		if ae.Code == 0 {
			ae.Code = resp.StatusCode
		}
		if ar.Parameters != nil && ar.Parameters.RetryAfter > 0 {
			ae.RetryAfter = time.Duration(ar.Parameters.RetryAfter) * time.Second
		} else if ae.Code == http.StatusTooManyRequests {
			ae.RetryAfter = headerRetryAfter(resp.Header)
		}
		return fmt.Errorf("telegram %s: %w", method, ae)
	}
	if out != nil {
		if err := json.Unmarshal(ar.Result, out); err != nil {
			return &requestError{method: method, msg: "malformed result", cause: err}
		}
	}
	return nil
}

// failed drops the pool's idle connections (they may have died together with
// this one) unless the caller cancelled, and returns the redacted error.
func (c *Client) failed(pool *httpPool, method string, err error) error {
	if !errors.Is(err, context.Canceled) {
		pool.dropIdle()
	}
	return c.transportError(method, err)
}

// transportError strips the *url.Error wrapper (whose message embeds the request
// URL and therefore the token) and redacts whatever remains.
func (c *Client) transportError(method string, err error) error {
	cause := err
	var ue *url.Error
	if errors.As(err, &ue) {
		cause = ue.Err
	}
	msg := "request failed"
	if cause != nil {
		msg = c.redact(cause.Error())
	}
	return &requestError{method: method, msg: msg, cause: cause}
}

func (c *Client) redact(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "<redacted>")
}

func headerRetryAfter(h http.Header) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}
