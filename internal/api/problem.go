// Package api holds the HTTP plumbing shared by every version of the API:
// problem responses, request middleware, and the authentication and policy
// gates handlers apply.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/bookbee/air-infrastructure-service/internal/schema"
)

// problemMedia is the RFC 9457 content type. Callers distinguish an error body
// from a successful one by it, so it is set on every failure.
const problemMedia = "application/problem+json"

// Error is a failure with an HTTP rendering. The Code values are a parity
// contract — consumers branch on them — so they are fixed here rather than
// derived from the status.
type Error struct {
	Status int
	Code   string
	Title  string
	Detail string
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return e.Title
}

// The failures the service distinguishes. Anything else becomes a 500 with the
// internal_error code and no detail, so an unexpected panic or bug cannot leak
// internals through the response body.
func Unauthorized(detail string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Title: "Unauthorized", Detail: detail}
}

// Forbidden is a policy denial.
func Forbidden(detail string) *Error {
	return &Error{Status: http.StatusForbidden, Code: "policy_denied", Title: "Forbidden", Detail: detail}
}

// ResourceNotFound is an unregistered resource name.
func ResourceNotFound(detail string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "resource_not_found", Title: "Not Found", Detail: detail}
}

// RateLimited is a token-bucket rejection.
func RateLimited(detail string) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Title: "Too Many Requests", Detail: detail}
}

// DependencyUnavailable is a downstream failure.
func DependencyUnavailable(detail string) *Error {
	return &Error{Status: http.StatusServiceUnavailable, Code: "dependency_unavailable", Title: "Service Unavailable", Detail: detail}
}

// ValidationError is a malformed request body.
func ValidationError(detail string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_error", Title: "Unprocessable Entity", Detail: detail}
}

// Internal is the catch-all.
func Internal(detail string) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: "internal_error", Title: "Internal Server Error", Detail: detail}
}

// WriteProblem renders err as problem+json. A non-*Error becomes an opaque
// 500: the underlying message is logged, never served.
func WriteProblem(w http.ResponseWriter, r *http.Request, err error, log *slog.Logger) {
	var appErr *Error
	if !errors.As(err, &appErr) {
		appErr = Internal("unexpected error")
		log.Error("unhandled", "error", err.Error(), "request_id", RequestIDFrom(r.Context()))
	} else if appErr.Status >= http.StatusInternalServerError {
		log.Error("app_error", "code", appErr.Code, "detail", appErr.Detail,
			"request_id", RequestIDFrom(r.Context()))
	}

	rid := RequestIDFrom(r.Context())
	body := schema.Problem{
		Type:      "about:blank",
		Title:     appErr.Title,
		Status:    appErr.Status,
		Code:      appErr.Code,
		Detail:    optional(appErr.Detail),
		RequestID: optional(rid),
	}

	w.Header().Set("Content-Type", problemMedia)
	w.WriteHeader(appErr.Status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already on the wire, so there is nothing left to
		// tell the client; record it and move on.
		log.Error("problem_encode_failed", "error", err.Error())
	}
}

// WriteJSON renders a successful body.
func WriteJSON(w http.ResponseWriter, status int, body any, log *slog.Logger) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Error("response_encode_failed", "error", err.Error())
	}
}

func optional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
