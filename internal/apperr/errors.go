// Package apperr defines transport-agnostic application errors with HTTP mapping.
package apperr

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/getcontact/backend/internal/domain"
)

// Error is an application error carrying an HTTP status, a stable machine code
// and optional field-level details.
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// New builds an application error.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Invalid reports a validation failure for one or more fields.
func Invalid(fields map[string]string) *Error {
	return &Error{
		Status:  http.StatusUnprocessableEntity,
		Code:    "VALIDATION_ERROR",
		Message: "request validation failed",
		Details: fields,
	}
}

// BadRequest reports a malformed request.
func BadRequest(message string) *Error {
	return New(http.StatusBadRequest, "BAD_REQUEST", message)
}

// Unauthorized reports missing or invalid credentials.
func Unauthorized(message string) *Error {
	if message == "" {
		message = "authentication required"
	}
	return New(http.StatusUnauthorized, "UNAUTHORIZED", message)
}

// Forbidden reports an authorization failure.
func Forbidden(message string) *Error {
	if message == "" {
		message = "insufficient permissions"
	}
	return New(http.StatusForbidden, "FORBIDDEN", message)
}

// Blocked reports a blocked account with its own stable machine code.
func Blocked() *Error {
	return New(http.StatusForbidden, "USER_BLOCKED", "user is blocked")
}

// ServiceUnavailable reports a dependency that cannot be checked safely.
func ServiceUnavailable(code, message string) *Error {
	return New(http.StatusServiceUnavailable, code, message)
}

// NotFound reports a missing resource.
func NotFound(resource string) *Error {
	return New(http.StatusNotFound, "NOT_FOUND", resource+" not found")
}

// Conflict reports a uniqueness or state conflict.
func Conflict(message string) *Error {
	return New(http.StatusConflict, "CONFLICT", message)
}

// TooManyRequests reports rate limiting.
func TooManyRequests() *Error {
	return New(http.StatusTooManyRequests, "RATE_LIMITED", "too many requests, retry later")
}

// Internal reports an unexpected server failure.
func Internal(cause error) *Error {
	return &Error{
		Status:  http.StatusInternalServerError,
		Code:    "INTERNAL_ERROR",
		Message: "internal server error",
		Cause:   cause,
	}
}

// From normalizes any error into an *Error. Unknown errors become 500 without
// leaking implementation details to the client.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var appErr *Error
	if errors.As(err, &appErr) {
		if appErr.Status >= 500 && errors.Is(appErr.Cause, context.DeadlineExceeded) {
			return New(http.StatusGatewayTimeout, "REQUEST_TIMEOUT", "request timed out")
		}
		return appErr
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return NotFound("resource")
	case errors.Is(err, domain.ErrConflict):
		return Conflict("resource already exists")
	case errors.Is(err, domain.ErrUnauthorized):
		return Unauthorized("")
	case errors.Is(err, domain.ErrForbidden):
		return Forbidden("")
	case errors.Is(err, domain.ErrBlocked):
		return Blocked()
	case errors.Is(err, domain.ErrInvalidToken):
		return Unauthorized("invalid or expired token")
	default:
		return Internal(err)
	}
}
