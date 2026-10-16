// Package telegram is a small Bot API client used by the API's notification
// worker and by both long-polling bots. Errors are typed (see APIError and the
// Is* helpers) and never contain the bot token; request URLs are never logged.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the public Bot API endpoint.
	DefaultBaseURL = "https://api.telegram.org"

	// requestSlack is added to the long-poll timeout to get the per-request deadline.
	requestSlack = 15 * time.Second

	// maxResponseBytes bounds a single response body (100 updates fit easily).
	maxResponseBytes = 32 << 20
)

// Client calls the Bot API for one bot token. It is safe for concurrent use.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
	log     *slog.Logger
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the Bot API endpoint (tests, local Bot API server).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient sets the HTTP client. Deadlines are applied per request via the
// context, so the client should not set a Timeout shorter than the long-poll timeout.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
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
		http:    &http.Client{},
		log:     slog.New(slog.DiscardHandler),
	}
	for _, o := range opts {
		o(c)
	}
	return c
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
// pollTimeout is the long-poll duration; the request deadline is pollTimeout+15s.
func (c *Client) call(ctx context.Context, method string, params any, out any, pollTimeout time.Duration) error {
	start := time.Now()
	err := c.do(ctx, method, params, out, pollTimeout)
	if err != nil {
		c.log.Debug("telegram call failed", slog.String("method", method),
			slog.Duration("dur", time.Since(start)), slog.String("err", err.Error()))
	} else {
		c.log.Debug("telegram call", slog.String("method", method), slog.Duration("dur", time.Since(start)))
	}
	return err
}

func (c *Client) do(ctx context.Context, method string, params any, out any, pollTimeout time.Duration) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram %s: encode request: %w", method, err)
	}
	ctx, cancel := context.WithTimeout(ctx, pollTimeout+requestSlack)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.transportError(method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return c.transportError(method, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return c.transportError(method, err)
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
