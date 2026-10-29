package verification

import (
	"context"
	"errors"
	"log/slog"
)

// Processing errors deliberately carry only fixed codes and an exit status.
// Subprocess stderr, paths, document text and personal values must never reach logs.
type processingError struct {
	Stage    string
	Code     string
	ExitCode int
}

func (e *processingError) Error() string { return "verification: " + e.Stage + ": " + e.Code }

func failureDetails(err error) (string, string, int) {
	var e *processingError
	if errors.As(err, &e) {
		return e.Stage, e.Code, e.ExitCode
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "processing", "timeout", 0
	}
	if errors.Is(err, context.Canceled) {
		return "processing", "interrupted", 0
	}
	return "processing", "failed", 0
}

func assessmentLog(r Result) slog.Attr {
	// Whitelist field names; serialize comparison states and scores, never values.
	fields := make([]map[string]any, 0, 6)
	for _, field := range []string{"document_type", "first_name", "last_name", "patronymic", "iin", "document_number"} {
		if state, ok := r.Comparisons[field]; ok {
			fields = append(fields, map[string]any{"field": field, "state": state, "score": r.Fields[field]})
		}
	}
	return slog.Any("assessment", fields)
}
