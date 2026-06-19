package backend

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidConfig means Client configuration is incomplete or unsafe.
	ErrInvalidConfig = errors.New("backend: invalid client configuration")
	// ErrInvalidArgument means a method argument cannot form a valid request.
	ErrInvalidArgument = errors.New("backend: invalid argument")
	// ErrAPI marks a non-2xx response returned by the gateway.
	ErrAPI = errors.New("backend: gateway API error")
	// ErrInvalidResponse means a successful HTTP response was not valid JSON.
	ErrInvalidResponse = errors.New("backend: invalid gateway response")
	// ErrResponseTooLarge means the response exceeded the configured limit.
	ErrResponseTooLarge = errors.New("backend: gateway response too large")
	// ErrRedirect means the gateway attempted to redirect an internal request.
	ErrRedirect = errors.New("backend: redirects are disabled")
)

// APIError describes a non-2xx gateway response.
type APIError struct {
	StatusCode int
	Code       string
	Reason     string
}

func (e *APIError) Error() string {
	if e == nil {
		return ErrAPI.Error()
	}
	if e.Code != "" && e.Reason != "" {
		return fmt.Sprintf("backend: gateway returned HTTP %d (%s): %s", e.StatusCode, e.Code, e.Reason)
	}
	if e.Code != "" {
		return fmt.Sprintf("backend: gateway returned HTTP %d (%s)", e.StatusCode, e.Code)
	}
	return fmt.Sprintf("backend: gateway returned HTTP %d", e.StatusCode)
}

// Unwrap makes errors.Is(err, ErrAPI) work.
func (e *APIError) Unwrap() error {
	return ErrAPI
}

// Retryable reports whether retrying later may succeed.
func (e *APIError) Retryable() bool {
	return e != nil && (e.StatusCode == 429 || e.StatusCode >= 500)
}

// RequestError wraps failures before a valid gateway response is received.
// Its cause remains available through errors.Is and errors.As.
type RequestError struct {
	Method string
	URL    string
	Err    error
}

func (e *RequestError) Error() string {
	if e == nil {
		return "backend: request failed"
	}
	return fmt.Sprintf("backend: %s %s failed: %v", e.Method, e.URL, e.Err)
}

// Unwrap exposes the transport or context error.
func (e *RequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
