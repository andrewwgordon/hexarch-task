// Package httpconv provides conversions shared between the HTTP adapters
// (httpapi and httpweb): parsing user input strings into domain types and
// mapping domain errors to HTTP status codes. Keeping these here avoids
// duplicating parsing and error-mapping logic across the JSON and HTML
// adapters.
//
// This file (httpconv.go) is the whole package.
//
// Public API:
//   - Functions: ParseDeadline, ParseStatus, StatusForKind,
//     ExtractStatusAndMessage
//
// Private:
//   - deadlineLayout
package httpconv

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"hexarch/internal/domain"
)

// deadlineLayout is the single accepted date format for deadlines, used by
// both HTTP adapters ("YYYY-MM-DD").
const deadlineLayout = time.DateOnly

// ParseDeadline converts a "YYYY-MM-DD" string into a time.Time.
// It returns an Invalid domain error so callers surface a 400.
func ParseDeadline(s string) (time.Time, error) {
	t, err := time.Parse(deadlineLayout, s)
	if err != nil {
		return time.Time{}, domain.Invalid(fmt.Sprintf("deadline must look like 2026-08-01, got %q", s))
	}
	return t, nil
}

// ParseStatus converts a status string into a domain.Status.
// It returns an Invalid domain error on unknown values.
func ParseStatus(s string) (domain.Status, error) {
	switch domain.Status(s) {
	case domain.StatusTodo, domain.StatusInProgress, domain.StatusDone, domain.StatusArchived:
		return domain.Status(s), nil
	default:
		return "", domain.Invalid(fmt.Sprintf("invalid status %q (want todo|in_progress|done|archived)", s))
	}
}

// StatusForKind maps a domain.Kind to an HTTP status code.
func StatusForKind(k domain.Kind) int {
	switch k {
	case domain.KindNotFound:
		return http.StatusNotFound
	case domain.KindInvalid:
		return http.StatusBadRequest
	case domain.KindConflict:
		return http.StatusConflict
	case domain.KindStorage:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// ExtractStatusAndMessage inspects an error and extracts the HTTP status,
// a canonical error code, and a human-readable message.
// If err is nil it returns zeros; if err is not a domain error it returns
// a generic 500 / "INTERNAL" / "internal error".
func ExtractStatusAndMessage(err error) (status int, code, msg string) {
	if err == nil {
		return 0, "", ""
	}
	code = "INTERNAL"
	status = http.StatusInternalServerError
	msg = "internal error"
	var de *domain.DomainError
	if errors.As(err, &de) {
		code = string(de.Kind())
		status = StatusForKind(de.Kind())
		msg = de.Error()
	}
	return status, code, msg
}
