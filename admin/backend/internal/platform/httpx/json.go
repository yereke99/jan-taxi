package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// MaxJSONBody is the default request body limit for JSON endpoints.
const MaxJSONBody = 64 << 10

// HandlerFunc is an http handler that returns an error; see H.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// H adapts a HandlerFunc, writing any returned error with WriteError.
func H(fn HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			WriteError(w, r, err)
		}
	}
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil || status == http.StatusNoContent {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// OK writes 200 with v.
func OK(w http.ResponseWriter, v any) error {
	WriteJSON(w, http.StatusOK, v)
	return nil
}

// Created writes 201 with v.
func Created(w http.ResponseWriter, v any) error {
	WriteJSON(w, http.StatusCreated, v)
	return nil
}

// NoContent writes 204.
func NoContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// DecodeJSON strictly decodes a single JSON object into dst: body limited to
// MaxJSONBody, unknown fields rejected, trailing data rejected.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		return NewError(http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxJSONBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
		case errors.Is(err, io.EOF):
			return BadRequest("request body is empty")
		default:
			return BadRequest("invalid JSON: " + err.Error()).WithCause(err)
		}
	}
	if dec.More() {
		return BadRequest("request body must contain a single JSON object")
	}
	return nil
}

// UUIDParam parses a chi URL parameter as a UUID; invalid values yield 404 so
// that malformed IDs are indistinguishable from unknown ones.
func UUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, NotFound()
	}
	return id, nil
}

// QueryUUID parses an optional UUID query parameter.
func QueryUUID(r *http.Request, name string) (*uuid.UUID, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, Validation(map[string]string{name: "invalid"})
	}
	return &id, nil
}

// QueryInt parses an optional integer query parameter bounded to [min,max].
func QueryInt(r *http.Request, name string, def, min, max int) (int, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, Validation(map[string]string{name: "invalid"})
	}
	return n, nil
}

// PageParams are admin list parameters (?page=1&page_size=25).
type PageParams struct {
	Page     int
	PageSize int
}

// Offset returns the SQL OFFSET for the page.
func (p PageParams) Offset() int { return (p.Page - 1) * p.PageSize }

// ParsePage parses admin pagination with page_size capped at 100.
func ParsePage(r *http.Request) (PageParams, error) {
	page, err := QueryInt(r, "page", 1, 1, 100000)
	if err != nil {
		return PageParams{}, err
	}
	size, err := QueryInt(r, "page_size", 25, 1, 100)
	if err != nil {
		return PageParams{}, err
	}
	return PageParams{Page: page, PageSize: size}, nil
}

// Page is the admin list envelope.
type Page[T any] struct {
	Items    []T `json:"items"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Total    int `json:"total"`
}

// CursorList is the Mini App list envelope.
type CursorList[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
