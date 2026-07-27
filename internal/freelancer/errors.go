package freelancer

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNoSession reports a client used before a session was established.
var ErrNoSession = errors.New("no active session: run `freelancer login`")

// ErrUnauthorized reports a rejected or expired auth hash.
var ErrUnauthorized = errors.New("unauthorized")

// APIError is the Freelancer error envelope:
// {"status":"error","message":"…","error_code":"…","request_id":"…"}.
type APIError struct {
	StatusCode int    `json:"-"`
	Method     string `json:"-"`
	URL        string `json:"-"`
	Status     string `json:"status"`
	Code       string `json:"error_code"`
	Message    string `json:"message"`
	RequestID  string `json:"request_id"`
	Body       string `json:"-"`
}

func (e *APIError) Error() string {
	parts := []string{fmt.Sprintf("%s %s: http %d", e.Method, e.URL, e.StatusCode)}
	if e.Code != "" {
		parts = append(parts, e.Code)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	if e.Code == "" && e.Message == "" && e.Body != "" {
		parts = append(parts, truncate(e.Body, 300))
	}
	return strings.Join(parts, ": ")
}

// Unwrap maps 401 responses to ErrUnauthorized so callers can retry auth.
func (e *APIError) Unwrap() error {
	if e.StatusCode == 401 {
		return ErrUnauthorized
	}
	return nil
}

func parseAPIError(method, url string, status int, body []byte) *APIError {
	apiErr := &APIError{StatusCode: status, Method: method, URL: url, Body: string(body)}
	_ = json.Unmarshal(body, apiErr)
	return apiErr
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
