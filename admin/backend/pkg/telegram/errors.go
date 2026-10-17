package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// APIError is an error response from the Bot API ({"ok":false,...}) or a non-JSON
// HTTP failure in front of it (e.g. a 502 from a proxy).
type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration // from parameters.retry_after on 429
}

func (e *APIError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("telegram api error %d: %s (retry after %s)", e.Code, e.Description, e.RetryAfter)
	}
	return fmt.Sprintf("telegram api error %d: %s", e.Code, e.Description)
}

// requestError is a transport-level failure (DNS, TCP, TLS, timeout, malformed
// response). Its message is redacted so it can never carry the bot token.
type requestError struct {
	method string
	msg    string
	cause  error
}

func (e *requestError) Error() string { return "telegram " + e.method + ": " + e.msg }

func (e *requestError) Unwrap() error { return e.cause }

// IsBlocked reports a 403: the user blocked the bot or never started it.
func IsBlocked(err error) bool { return apiCode(err) == http.StatusForbidden }

// IsConflict reports a 409: another getUpdates consumer or an active webhook.
func IsConflict(err error) bool { return apiCode(err) == http.StatusConflict }

// IsMessageNotModified reports the 400 returned when an edit would not change the message.
func IsMessageNotModified(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(ae.Description), "message is not modified")
}

// IsRetryable reports errors worth retrying later: 429, 5xx, and transport
// failures (including per-request timeouts). Cancellation by the caller is not
// retryable.
func IsRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code == http.StatusTooManyRequests || ae.Code >= 500
	}
	var re *requestError
	return errors.As(err, &re)
}

// IsNetworkError reports a transport-level failure (DNS, TCP, TLS, timeout,
// a connection dropped mid-response, a malformed response) as opposed to an
// answer of the Bot API.
// Cancellation by the caller is not a network error.
func IsNetworkError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var re *requestError
	return errors.As(err, &re)
}

// RetryAfter returns the server-requested delay of a 429 response.
func RetryAfter(err error) (time.Duration, bool) {
	var ae *APIError
	if errors.As(err, &ae) && ae.RetryAfter > 0 {
		return ae.RetryAfter, true
	}
	return 0, false
}

func apiCode(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return 0
}
