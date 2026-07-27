package freelancer

import (
	"os"
	"time"
)

// Defaults captured from the freelancer.com web app (main bundle
// `freelancerHttpConfig` and `authConfig`).
const (
	DefaultWebBase   = "https://www.freelancer.com"
	DefaultAPIBase   = "https://www.freelancer.com/api"
	DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	DefaultLanguage  = "en"
	DefaultAppName   = "main"

	// AuthHeader is the header the web app uses: "<user_id>;<auth_hash>".
	AuthHeader = "freelancer-auth-v2"
)

// Config controls endpoints and request identity.
type Config struct {
	// WebBase serves /auth/device and every /ajax-api/... endpoint.
	WebBase string
	// APIBase serves the versioned REST API (/api/projects/0.1/... etc).
	APIBase   string
	UserAgent string
	Language  string
	AppName   string
	Timeout   time.Duration
}

// DefaultConfig returns defaults, overridable through environment variables.
func DefaultConfig() Config {
	cfg := Config{
		WebBase:   envOr("FREELANCER_WEB_BASE", DefaultWebBase),
		APIBase:   envOr("FREELANCER_API_BASE", DefaultAPIBase),
		UserAgent: envOr("FREELANCER_USER_AGENT", DefaultUserAgent),
		Language:  envOr("FREELANCER_LANGUAGE", DefaultLanguage),
		AppName:   envOr("FREELANCER_APP_NAME", DefaultAppName),
		Timeout:   30 * time.Second,
	}
	if raw := os.Getenv("FREELANCER_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			cfg.Timeout = d
		}
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
