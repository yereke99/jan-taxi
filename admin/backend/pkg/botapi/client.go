// Package botapi is the typed client for the internal bot API
// (/internal/v1 on the API's internal listener). Both Telegram bots use it;
// they never talk to the database directly.
package botapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Bot names accepted by the internal API.
const (
	BotPassenger = "passenger"
	BotDriver    = "driver"
)

const (
	// DefaultTimeout bounds one attempt of a call; uploads get UploadTimeout.
	DefaultTimeout = 10 * time.Second
	// UploadTimeout bounds one attempt of a file upload.
	UploadTimeout = 20 * time.Second

	defaultMaxAttempts = 3
	maxRetryAfter      = 5 * time.Second
	maxResponseBytes   = 4 << 20
)

// Client calls the internal bot API as one bot. It is safe for concurrent use.
type Client struct {
	BaseURL string       // e.g. http://api:8081
	Secret  string       // INTERNAL_BOT_SERVICE_SECRET
	Bot     string       // "passenger" | "driver"
	HTTP    *http.Client // nil means the package's own client (see ResetConnections)

	// Timeout bounds one attempt of a call (default 10s, uploads 20s).
	Timeout time.Duration
	// MaxAttempts bounds retries of idempotent calls (default 3).
	MaxAttempts int
	// RetryBase is the first retry delay (default 250ms); it doubles per attempt.
	RetryBase time.Duration

	mu  sync.Mutex
	own *http.Client
}

// New returns a Client with default settings.
func New(baseURL, secret, bot string) *Client {
	return &Client{BaseURL: baseURL, Secret: secret, Bot: bot}
}

// newHTTPClient bounds every phase of a request to the internal API.
func newHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: UploadTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
	}
	t.Protocols = new(http.Protocols)
	t.Protocols.SetHTTP1(true)
	return &http.Client{Transport: t, Timeout: UploadTimeout + 5*time.Second}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.own == nil {
		c.own = newHTTPClient()
	}
	return c.own
}

// ResetConnections drops pooled connections; the package's own client is
// replaced by a new one, so the next call dials afresh.
func (c *Client) ResetConnections() {
	if c.HTTP != nil {
		c.HTTP.CloseIdleConnections()
		return
	}
	c.mu.Lock()
	old := c.own
	c.own = nil
	c.mu.Unlock()
	if old != nil {
		old.CloseIdleConnections()
	}
}

// Error is an error response of the internal API
// ({"error":{"code","message","fields","details"}}) or an HTTP failure in front of it.
type Error struct {
	Status     int
	Code       string
	Message    string
	Fields     map[string]string
	Details    map[string]any
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	return fmt.Sprintf("bot api error %d %s: %s", e.Status, e.Code, e.Message)
}

// ErrorCode returns the API error code of err, or "" when err is not an API error.
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// IsCode reports whether err is an API error with one of codes.
func IsCode(err error, codes ...string) bool {
	c := ErrorCode(err)
	for _, code := range codes {
		if c != "" && c == code {
			return true
		}
	}
	return false
}

// AsError returns the API error carried by err, if any.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// IsTransient reports failures worth retrying later: network errors,
// timeouts, 429 and 5xx. Cancellation by the caller is not transient.
func IsTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Status == http.StatusTooManyRequests || e.Status >= 500
	}
	return true
}

// DetailInt64 returns an integer from the error details (JSON numbers decode as float64).
func (e *Error) DetailInt64(key string) (int64, bool) {
	if e == nil {
		return 0, false
	}
	switch v := e.Details[key].(type) {
	case float64:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	}
	return 0, false
}

// DetailString returns a string from the error details.
func (e *Error) DetailString(key string) string {
	if e == nil {
		return ""
	}
	s, _ := e.Details[key].(string)
	return s
}

type errorEnvelope struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
		Details map[string]any    `json:"details"`
	} `json:"error"`
}

// do performs one API call; idempotent calls are retried on transient failures.
type requestIDKey struct{}

// WithRequestID tags the API calls made with ctx with id (sent as
// X-Request-ID, at most 64 characters), so the API's log lines of a Telegram
// update can be found by it, e.g. "tg-cb-<callback query id>".
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" || len(id) > 64 {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any, idempotent bool) error {
	attempts := 1
	if idempotent {
		attempts = c.MaxAttempts
		if attempts <= 0 {
			attempts = defaultMaxAttempts
		}
	}
	base := c.RetryBase
	if base <= 0 {
		base = 250 * time.Millisecond
	}
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			delay := base << (i - 1)
			delay = delay/2 + rand.N(delay/2+1)
			if e, ok := AsError(err); ok && e.RetryAfter > 0 {
				delay = min(e.RetryAfter, maxRetryAfter)
			}
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return err
			case <-t.C:
			}
		}
		err = c.once(ctx, method, path, query, in, out)
		if err == nil || !IsTransient(err) || ctx.Err() != nil {
			return err
		}
	}
	return err
}

// multipartBody is a request body sent as multipart/form-data.
type multipartBody struct {
	data        []byte
	contentType string
}

func (c *Client) once(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body io.Reader
	contentType := ""
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	switch v := in.(type) {
	case nil:
	case multipartBody:
		body, contentType = bytes.NewReader(v.data), v.contentType
		timeout = max(timeout, UploadTimeout)
	default:
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("bot api %s %s: encode: %w", method, path, err)
		}
		body, contentType = bytes.NewReader(b), "application/json"
	}
	u := strings.TrimRight(c.BaseURL, "/") + "/internal/v1" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return fmt.Errorf("bot api %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	req.Header.Set("X-Jantaxi-Bot", c.Bot)
	req.Header.Set("Accept", "application/json")
	if id := requestIDFrom(ctx); id != "" {
		req.Header.Set("X-Request-ID", id)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	hc := c.httpClient()
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL carries no secret, but keep errors short
		}
		if !errors.Is(err, context.Canceled) {
			hc.CloseIdleConnections() // they may have died together with this one
		}
		return fmt.Errorf("bot api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			hc.CloseIdleConnections()
		}
		return fmt.Errorf("bot api %s %s: read: %w", method, path, err)
	}
	if resp.StatusCode >= 300 {
		return decodeError(resp, raw)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("bot api %s %s: decode: %w", method, path, err)
	}
	return nil
}

func decodeError(resp *http.Response, raw []byte) error {
	e := &Error{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	var env errorEnvelope
	if json.Unmarshal(raw, &env) == nil && env.Error.Code != "" {
		e.Code, e.Message, e.Fields, e.Details = env.Error.Code, env.Error.Message, env.Error.Fields, env.Error.Details
	} else {
		e.Code = "http_" + strconv.Itoa(resp.StatusCode)
	}
	if n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && n > 0 {
		e.RetryAfter = time.Duration(n) * time.Second
	}
	return e
}

func tgPath(prefix string, tgID int64, suffix string) string {
	return prefix + "/" + strconv.FormatInt(tgID, 10) + suffix
}
