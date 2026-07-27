package freelancer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// DeviceToken is the short-lived JWT that /auth/device hands out. The login
// endpoint requires it to fingerprint the client.
type DeviceToken struct {
	Token string `json:"token"`
}

// LoginResult is the payload of POST /ajax-api/auth/login.php.
type LoginResult struct {
	Token    string `json:"token"`
	UserID   int64  `json:"user"`
	UserRole string `json:"userRole"`
}

// DeviceToken fetches a fresh device token.
func (c *Client) DeviceToken(ctx context.Context) (string, error) {
	var out DeviceToken
	_, err := c.DoJSON(ctx, Request{
		Method:  http.MethodGet,
		Path:    "/auth/device",
		Base:    c.cfg.WebBase,
		NoAuth:  true,
		NoRetry: true,
		Headers: map[string]string{"referer": c.cfg.WebBase + "/login"},
	}, &out)
	if err != nil {
		return "", fmt.Errorf("device token: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("device token: empty response token")
	}
	return out.Token, nil
}

// LoginOptions carries optional login material.
type LoginOptions struct {
	// OTP is the one-time code when two-factor authentication is enabled.
	OTP string
	// Captcha and V3Captcha are passthroughs for reCAPTCHA challenges. The web
	// app sends empty strings for a normal password login.
	Captcha   string
	V3Captcha string
}

// Login exchanges credentials for the long-lived auth hash and persists it.
//
//  1. GET  {web}/auth/device                  -> device token
//  2. POST {web}/ajax-api/auth/login.php      -> {token, user, userRole}
//
// The token is then sent as `freelancer-auth-v2: <user>;<token>` on every call.
func (c *Client) Login(ctx context.Context, user, password string, opts LoginOptions) (*LoginResult, error) {
	if user == "" || password == "" {
		return nil, errors.New("user and password are required")
	}
	device, err := c.DeviceToken(ctx)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("user", user)
	form.Set("password", password)
	form.Set("device_token", device)
	form.Set("captcha", opts.Captcha)
	form.Set("v3Captcha", opts.V3Captcha)
	if opts.OTP != "" {
		form.Set("otp", opts.OTP)
	}

	query := url.Values{}
	query.Set("compact", "true")
	query.Set("new_errors", "true")
	query.Set("new_pools", "true")

	var out LoginResult
	resp, err := c.DoJSON(ctx, Request{
		Method:  http.MethodPost,
		Path:    "/ajax-api/auth/login.php",
		Base:    c.cfg.WebBase,
		Query:   query,
		Form:    form,
		NoAuth:  true,
		NoRetry: true,
		Headers: map[string]string{"referer": c.cfg.WebBase + "/login"},
	}, &out)
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	if out.Token == "" || out.UserID == 0 {
		body := ""
		if resp != nil {
			body = truncate(string(resp.Body), 300)
		}
		if body == "" {
			return nil, errors.New("login: response carried no token")
		}
		return nil, fmt.Errorf("login: response carried no token: %s", body)
	}

	c.mu.Lock()
	c.sess.UserID = out.UserID
	c.sess.Token = out.Token
	c.sess.Role = out.UserRole
	c.sess.DeviceToken = device
	c.mu.Unlock()

	if err := c.Persist(); err != nil {
		return &out, fmt.Errorf("save session: %w", err)
	}
	return &out, nil
}

// Refresh re-runs the login chain with stored credentials.
func (c *Client) Refresh(ctx context.Context) error {
	c.mu.Lock()
	creds := c.sess.Credentials
	c.mu.Unlock()
	if creds == nil || creds.User == "" || creds.Password == "" {
		return errors.New("no stored credentials: run `freelancer login --save-credentials` to enable refresh")
	}
	_, err := c.Login(ctx, creds.User, creds.Password, LoginOptions{})
	return err
}

// EnsureSession verifies the stored auth hash against the API, refreshing once
// when credentials are stored. It returns the authenticated user.
func (c *Client) EnsureSession(ctx context.Context) (*Self, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	self, err := c.Self(ctx)
	if err == nil {
		c.mu.Lock()
		c.sess.Username = self.Username
		c.sess.Email = self.Email
		if self.Role != "" {
			c.sess.Role = self.Role
		}
		c.mu.Unlock()
		return self, nil
	}
	if !errors.Is(err, ErrUnauthorized) || !c.canReauth() {
		return nil, err
	}
	if reauthErr := c.Refresh(ctx); reauthErr != nil {
		return nil, fmt.Errorf("%w (re-login failed: %v)", err, reauthErr)
	}
	return c.Self(ctx)
}
