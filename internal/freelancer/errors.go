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
	if hint := e.Hint(); hint != "" {
		parts = append(parts, "hint: "+hint)
	}
	return strings.Join(parts, ": ")
}

// hints turn Freelancer's error codes into the next action to take. Agents see
// these appended to the error, so a refusal explains itself instead of looking
// like a transport failure.
var hints = map[string]string{
	"ProjectExceptionCodes.RESTRICTED_FROM_BIDDING_PREMIUM_VERIFIED": "this project is $2500 USD or more, which needs Verified by Freelancer status on the account. " +
		"Do not retry: pick a project under that ceiling, or ask the account owner to complete verification.",
	"ProjectExceptionCodes.RESTRICTED_FROM_BIDDING_ON_FEATURED": "featured projects need 5 reviews, a paid membership, or Verified by Freelancer. " +
		"Do not retry: skip featured projects for this account.",
	"ProjectExceptionCodes.BID_LIMIT_EXCEEDED":         "the monthly bid allowance is spent. Check freelancer_account_limits for the refill time; do not retry until then.",
	"ProjectExceptionCodes.ALREADY_BID_ON_PROJECT":     "a bid already exists on this project. Use bid update instead of placing another.",
	"UserExceptionCodes.PROFILE_DESCRIPTION_TOO_SHORT": "profile_description must be at least 100 characters.",
	"UserExceptionCodes.GAF_EXCEPTION": "this endpoint can fail while still writing a row. Read the collection back before retrying, " +
		"otherwise you create duplicates.",
	"RestExceptionCodes.NOT_AUTHENTICATED": "the stored auth hash was rejected. Re-run `freelancer login`.",
	"RestExceptionCodes.BAD_FORM":          "a required parameter is missing or the body encoding is wrong: some endpoints read form fields, not JSON.",
	"TOO_MANY_REQUESTS":                    "stop retrying. Freelancer rate-limited this action; wait for the cooldown before trying again.",
}

// Hint returns actionable guidance for known error codes, empty when unknown.
func (e *APIError) Hint() string {
	if e == nil {
		return ""
	}
	return hints[e.Code]
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
	if apiErr.Code != "" || apiErr.Message != "" {
		return apiErr
	}

	var nested struct {
		Status    string `json:"status"`
		RequestID string `json:"request_id"`
		Error     struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &nested); err == nil {
		apiErr.Status = nested.Status
		apiErr.RequestID = nested.RequestID
		apiErr.Code = nested.Error.Code
		apiErr.Message = nested.Error.Detail
	}
	return apiErr
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
