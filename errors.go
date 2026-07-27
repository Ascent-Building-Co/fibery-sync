package fibery

import (
	"fmt"
	"net/http"
)

type IntegrationError struct {
	Status   int    `json:"status"`
	Message  string `json:"message"`
	TryLater bool   `json:"tryLater,omitempty"`
}

// Error implements the error interface.
func (i *IntegrationError) Error() string {
	if i == nil {
		return ""
	}
	return i.Message
}

// NewIntegrationError creates a new Integration error with status and message.
func NewError(status int, message string) *IntegrationError {
	return &IntegrationError{Status: status, Message: message}
}

// BadRequest is a helper for HTTP 400.
func BadRequest(message string) *IntegrationError {
	return NewError(http.StatusBadRequest, message)
}

// Unauthorized is a helper for HTTP 401.
func Unauthorized(message string) *IntegrationError {
	return NewError(http.StatusUnauthorized, message)
}

// NotFound is a helper for HTTP 404.
func NotFound(message string) *IntegrationError {
	return NewError(http.StatusNotFound, message)
}

// Forbidden is a helper for HTTP 403.
func Forbidden(message string) *IntegrationError {
	return NewError(http.StatusForbidden, message)
}

// UnprocessableEntity is a helper for HTTP 422.
func UnprocessableEntity(message string) *IntegrationError {
	return NewError(http.StatusUnprocessableEntity, message)
}

// Internal is a helper for HTTP 500.
func InternalError(message string) *IntegrationError {
	return NewError(http.StatusInternalServerError, message)
}

// BadGateway is a helper for HTTP 502.
func BadGateway(message string) *IntegrationError {
	return NewError(http.StatusBadGateway, message)
}

// ServiceUnavailable is a helper for HTTP 503.
func ServiceUnavailable(message string) *IntegrationError {
	return NewError(http.StatusServiceUnavailable, message)
}

// RateLimited is a helper for HTTP 429. Set tryLater to true if fibery should wait before requesting again.
func RateLimited(message string, tryLater bool) *IntegrationError {
	err := NewError(http.StatusTooManyRequests, message)
	err.TryLater = tryLater
	return err
}

func FullSyncRequired(message string) *IntegrationError {
	return NewError(http.StatusConflict, fmt.Sprintf("full sync required: %s", message))
}
