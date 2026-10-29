package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// envReader reads typed values and accumulates errors so that one Load call
// reports every problem at once. Error messages name the variable but never
// echo its value, because the value may be a secret.
type envReader struct {
	getenv func(string) string
	errs   []error
}

func (e *envReader) fail(key, format string, args ...any) {
	e.errs = append(e.errs, fmt.Errorf("%s: %s", key, fmt.Sprintf(format, args...)))
}

func (e *envReader) raw(key string) string { return strings.TrimSpace(e.getenv(key)) }

func (e *envReader) str(key, def string) string {
	if v := e.raw(key); v != "" {
		return v
	}
	return def
}

func (e *envReader) integer(key string, def int) int {
	v := e.raw(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.fail(key, "must be an integer")
		return def
	}
	return n
}

func (e *envReader) int64(key string, def int64) int64 {
	v := e.raw(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		e.fail(key, "must be an integer")
		return def
	}
	return n
}

func (e *envReader) decimal(key string, def float64) float64 {
	v := e.raw(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		e.fail(key, "must be a finite number")
		return def
	}
	return n
}

// duration accepts Go duration syntax ("24h", "90s") or a bare number of seconds.
func (e *envReader) duration(key string, def time.Duration) time.Duration {
	v := e.raw(key)
	if v == "" {
		return def
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n <= 0 {
			e.fail(key, "must be positive")
			return def
		}
		return time.Duration(n) * time.Second
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.fail(key, "must be a duration such as 30s, 12h or a number of seconds")
		return def
	}
	if d <= 0 {
		e.fail(key, "must be positive")
		return def
	}
	return d
}

func (e *envReader) boolean(key string, def bool) bool {
	v := strings.ToLower(e.raw(key))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		e.fail(key, "must be true or false")
		return def
	}
}

// list splits a comma-separated value, dropping empty items.
func (e *envReader) list(key string) []string {
	v := e.raw(key)
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
