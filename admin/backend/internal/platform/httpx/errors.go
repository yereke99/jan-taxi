// Package httpx contains the HTTP conventions shared by every handler: the error
// model, JSON encoding/decoding, parameter parsing and middleware.
package httpx

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// Error is the API error returned to clients as
// {"error":{"code":..., "message":..., "fields":..., "details":...}}.
// Frontends translate by Code; Message is an English fallback.
type Error struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	Details map[string]any    `json:"details,omitempty"`
	Err     error             `json:"-"` // internal cause, never serialised
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// WithDetails returns a copy of e carrying extra details.
func (e *Error) WithDetails(details map[string]any) *Error {
	c := *e
	c.Details = details
	return &c
}

// WithCause returns a copy of e carrying an internal cause for logs.
func (e *Error) WithCause(err error) *Error {
	c := *e
	c.Err = err
	return &c
}

// NewError builds an *Error.
func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func BadRequest(message string) *Error {
	return NewError(http.StatusBadRequest, "bad_request", message)
}

// Validation returns 422 validation_failed with per-field reason codes
// (e.g. {"comment": "too_long"}).
func Validation(fields map[string]string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "validation failed", Fields: fields}
}

func Unauthorized() *Error {
	return NewError(http.StatusUnauthorized, "unauthorized", "authentication required")
}

func Forbidden() *Error {
	return NewError(http.StatusForbidden, "forbidden", "not allowed")
}

func NotFound() *Error {
	return NewError(http.StatusNotFound, "not_found", "not found")
}

func Conflict(code, message string) *Error {
	return NewError(http.StatusConflict, code, message)
}

func Unprocessable(code, message string) *Error {
	return NewError(http.StatusUnprocessableEntity, code, message)
}

func TooManyRequests() *Error {
	return NewError(http.StatusTooManyRequests, "too_many_requests", "too many requests")
}

func Internal(err error) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "internal error", Err: err}
}

// AsError converts any error to *Error; unknown errors become 500 internal.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err)
}

// WriteError writes err as a JSON error response and logs server-side failures.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	e := AsError(err)
	if e.Status >= 500 {
		Logger(r.Context()).Error("request failed",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("code", e.Code),
			slog.Any("err", err),
		)
	} else if e.Err != nil {
		Logger(r.Context()).Debug("request rejected", slog.String("code", e.Code), slog.Any("err", e.Err))
	}
	WriteJSON(w, e.Status, map[string]any{"error": e})
}
