package freelancer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/igun997/freelancer-mcp/internal/session"
)

// maxResponseBody caps how much of a response is buffered (8 MiB).
const maxResponseBody = 8 << 20

// Client talks to freelancer.com with a persisted auth hash.
type Client struct {
	cfg   Config
	http  *http.Client
	store *session.Store

	mu   sync.Mutex
	sess *session.Session
}

// Option customises client construction.
type Option func(*Client)

// WithHTTPClient replaces the transport.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.http = hc
		}
	}
}

// New builds a client bound to a session store. A missing session on disk is
// not an error: login populates it.
func New(cfg Config, store *session.Store, opts ...Option) (*Client, error) {
	if store == nil {
		return nil, errors.New("nil session store")
	}
	c := &Client{
		cfg:   cfg,
		store: store,
		http:  &http.Client{Timeout: cfg.Timeout},
	}
	for _, opt := range opts {
		opt(c)
	}

	sess, err := store.Load()
	switch {
	case err == nil:
		c.sess = sess
	case errors.Is(err, session.ErrNotFound):
		c.sess = &session.Session{Profile: store.Profile()}
	default:
		return nil, err
	}
	return c, nil
}

// Config returns the effective configuration.
func (c *Client) Config() Config { return c.cfg }

// Store returns the bound session store.
func (c *Client) Store() *session.Store { return c.store }

// Session returns a copy of the current session state.
func (c *Client) Session() session.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return *c.sess
}

// HasSession reports whether credentials are present.
func (c *Client) HasSession() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess.Valid()
}

// UserID returns the authenticated user id, zero when logged out.
func (c *Client) UserID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess.UserID
}

// Persist writes the session to disk.
func (c *Client) Persist() error {
	c.mu.Lock()
	snapshot := *c.sess
	c.mu.Unlock()
	return c.store.Save(&snapshot)
}

// Logout clears in-memory state and the session file.
func (c *Client) Logout() error {
	c.mu.Lock()
	c.sess = &session.Session{Profile: c.store.Profile()}
	c.mu.Unlock()
	return c.store.Clear()
}

// SaveCredentials stores login material for unattended re-authentication. The
// password is written to the session file in plain text, so this stays opt-in
// and the file stays owner-only (0600).
func (c *Client) SaveCredentials(user, password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sess.Credentials = &session.Credentials{User: user, Password: password}
}

// ForgetCredentials drops any stored password.
func (c *Client) ForgetCredentials() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sess.Credentials = nil
}

// FileUpload is one multipart file part.
type FileUpload struct {
	Field string
	Name  string
	Data  []byte
}

// Request describes one HTTP call.
type Request struct {
	Method string
	// Path is either absolute or resolved against Base (APIBase by default).
	Path  string
	Base  string
	Query url.Values
	// JSON is marshalled as an application/json body.
	JSON any
	// Form is sent as application/x-www-form-urlencoded.
	Form url.Values
	// Files switch the request to multipart/form-data, carrying Form as fields.
	Files   []FileUpload
	Headers map[string]string
	// NoAuth skips the freelancer-auth-v2 header.
	NoAuth bool
	// NoRetry skips the re-login retry on 401.
	NoRetry bool
}

// Response carries the raw result of a call.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	URL        string
	Method     string
}

type envelope struct {
	Status    string          `json:"status"`
	Result    json.RawMessage `json:"result"`
	Message   string          `json:"message"`
	Code      string          `json:"error_code"`
	RequestID string          `json:"request_id"`
}

// Result returns the `result` member of the Freelancer envelope. Responses
// without an envelope (a few legacy endpoints) are returned verbatim.
func (r *Response) Result() (json.RawMessage, error) {
	if len(r.Body) == 0 {
		return nil, fmt.Errorf("%s %s: empty response body", r.Method, r.URL)
	}
	var env envelope
	if err := json.Unmarshal(r.Body, &env); err != nil {
		return nil, fmt.Errorf("%s %s: decode response: %w", r.Method, r.URL, err)
	}
	if env.Status == "error" {
		return nil, &APIError{
			StatusCode: r.StatusCode, Method: r.Method, URL: r.URL,
			Status: env.Status, Code: env.Code, Message: env.Message, RequestID: env.RequestID,
			Body: string(r.Body),
		}
	}
	if len(env.Result) == 0 {
		return r.Body, nil
	}
	return env.Result, nil
}

// JSON decodes the envelope result into out.
func (r *Response) JSON(out any) error {
	result, err := r.Result()
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(result, out); err != nil {
		return fmt.Errorf("%s %s: decode result: %w", r.Method, r.URL, err)
	}
	return nil
}

// Do performs a request, re-authenticating once on 401 when credentials are
// stored.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	resp, err := c.do(ctx, req)
	if err == nil {
		return resp, nil
	}
	if req.NoRetry || !errors.Is(err, ErrUnauthorized) || !c.canReauth() {
		return resp, err
	}
	if reauthErr := c.Refresh(ctx); reauthErr != nil {
		return resp, fmt.Errorf("%w (re-login failed: %v)", err, reauthErr)
	}
	req.NoRetry = true
	return c.do(ctx, req)
}

// DoJSON performs a request and decodes the envelope result into out.
func (c *Client) DoJSON(ctx context.Context, req Request, out any) (*Response, error) {
	resp, err := c.Do(ctx, req)
	if err != nil {
		return resp, err
	}
	return resp, resp.JSON(out)
}

// DoRaw performs a request and returns the envelope result unparsed.
func (c *Client) DoRaw(ctx context.Context, req Request) (json.RawMessage, error) {
	resp, err := c.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Result()
}

// API issues a request against the versioned REST API.
func (c *Client) API(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	return c.DoRaw(ctx, Request{Method: method, Path: path, Base: c.cfg.APIBase, Query: query, JSON: body})
}

// Web issues a request against www.freelancer.com (ajax-api and friends).
func (c *Client) Web(ctx context.Context, method, path string, query url.Values, form url.Values) (json.RawMessage, error) {
	return c.DoRaw(ctx, Request{Method: method, Path: path, Base: c.cfg.WebBase, Query: query, Form: form})
}

// Ajax issues a GET against /ajax-api with the compatibility flags the web app
// always sends.
func (c *Client) Ajax(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("compact", "true")
	query.Set("new_errors", "true")
	query.Set("new_pools", "true")
	return c.Web(ctx, http.MethodGet, "/ajax-api/"+strings.TrimPrefix(path, "/"), query, nil)
}

func (c *Client) do(ctx context.Context, req Request) (*Response, error) {
	target, err := c.resolveURL(req)
	if err != nil {
		return nil, err
	}
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}

	var body io.Reader
	contentType := ""
	switch {
	case len(req.Files) > 0:
		buf := &bytes.Buffer{}
		mw := multipart.NewWriter(buf)
		for key, values := range req.Form {
			for _, value := range values {
				if err := mw.WriteField(key, value); err != nil {
					return nil, fmt.Errorf("multipart field %s: %w", key, err)
				}
			}
		}
		for _, file := range req.Files {
			field := file.Field
			if field == "" {
				field = "files[]"
			}
			name := file.Name
			if name == "" {
				name = "upload"
			}
			part, err := mw.CreateFormFile(field, filepath.Base(name))
			if err != nil {
				return nil, fmt.Errorf("multipart file %s: %w", name, err)
			}
			if _, err := part.Write(file.Data); err != nil {
				return nil, fmt.Errorf("write file %s: %w", name, err)
			}
		}
		if err := mw.Close(); err != nil {
			return nil, fmt.Errorf("close multipart: %w", err)
		}
		body = buf
		contentType = mw.FormDataContentType()
	case req.Form != nil:
		body = strings.NewReader(req.Form.Encode())
		contentType = "application/x-www-form-urlencoded; charset=UTF-8"
	case req.JSON != nil:
		payload, err := json.Marshal(req.JSON)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		body = bytes.NewReader(payload)
		contentType = "application/json"
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	c.applyHeaders(httpReq, req, contentType)

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, target, err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("%s %s: read response: %w", method, target, err)
	}

	resp := &Response{
		StatusCode: httpResp.StatusCode,
		Header:     httpResp.Header,
		Body:       raw,
		URL:        target,
		Method:     method,
	}
	if httpResp.StatusCode >= 400 {
		return resp, parseAPIError(method, target, httpResp.StatusCode, raw)
	}
	return resp, nil
}

func (c *Client) resolveURL(req Request) (string, error) {
	raw := req.Path
	if raw == "" {
		return "", errors.New("empty request path")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		base := req.Base
		if base == "" {
			base = c.cfg.APIBase
		}
		raw = strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(raw, "/")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url %q: %w", raw, err)
	}
	if len(req.Query) > 0 {
		q := u.Query()
		for key, values := range req.Query {
			for _, value := range values {
				q.Add(key, value)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func (c *Client) applyHeaders(httpReq *http.Request, req Request, contentType string) {
	h := httpReq.Header
	h.Set("accept", "application/json, text/plain, */*")
	h.Set("user-agent", c.cfg.UserAgent)
	h.Set("origin", c.cfg.WebBase)
	h.Set("referer", strings.TrimSuffix(c.cfg.WebBase, "/")+"/")
	h.Set("freelancer-app-name", c.cfg.AppName)
	h.Set("freelancer-app-platform", "web")
	h.Set("freelancer-app-locale", c.cfg.Language)
	if contentType != "" {
		h.Set("content-type", contentType)
	}
	if !req.NoAuth {
		sess := c.Session()
		if value := sess.AuthHeader(); value != "" {
			h.Set(AuthHeader, value)
		}
	}
	for key, value := range req.Headers {
		h.Set(key, value)
	}
}

func (c *Client) canReauth() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	creds := c.sess.Credentials
	return creds != nil && creds.User != "" && creds.Password != ""
}

// requireSession guards calls that cannot work anonymously.
func (c *Client) requireSession() error {
	if !c.HasSession() {
		return ErrNoSession
	}
	return nil
}

func idList(key string, ids []int64) url.Values {
	values := url.Values{}
	for _, id := range ids {
		values.Add(key+"[]", strconv.FormatInt(id, 10))
	}
	return values
}

func merge(dst url.Values, src url.Values) url.Values {
	if dst == nil {
		dst = url.Values{}
	}
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	return dst
}
