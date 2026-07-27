// Package session persists Freelancer authentication state between CLI and MCP
// invocations. Freelancer issues an opaque auth hash that is sent as the
// `freelancer-auth-v2: <user_id>;<token>` header, so the whole session is just
// a user id plus that token.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// ErrNotFound reports a missing session file for the requested profile.
var ErrNotFound = errors.New("no stored session")

// DefaultProfile is used when no profile is requested.
const DefaultProfile = "default"

var profilePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// Credentials holds login material. Storing a password on disk is opt-in only.
type Credentials struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// Session is the persisted authentication state for one Freelancer account.
type Session struct {
	Profile     string       `json:"profile"`
	UserID      int64        `json:"user_id,omitempty"`
	Token       string       `json:"token,omitempty"`
	Role        string       `json:"role,omitempty"`
	Username    string       `json:"username,omitempty"`
	Email       string       `json:"email,omitempty"`
	DeviceToken string       `json:"device_token,omitempty"`
	Credentials *Credentials `json:"credentials,omitempty"`
	CreatedAt   time.Time    `json:"created_at,omitempty"`
	UpdatedAt   time.Time    `json:"updated_at,omitempty"`
}

// Valid reports whether the session carries usable credentials. The auth hash
// is opaque and carries no expiry, so freshness can only be proven by a call.
func (s *Session) Valid() bool {
	return s != nil && s.UserID != 0 && s.Token != ""
}

// AuthHeader returns the value for the freelancer-auth-v2 header.
func (s *Session) AuthHeader() string {
	if !s.Valid() {
		return ""
	}
	return strconv.FormatInt(s.UserID, 10) + ";" + s.Token
}

// Store reads and writes session files under a config directory.
type Store struct {
	dir     string
	profile string
}

// NewStore resolves the session directory. Precedence:
// FREELANCER_SESSION_DIR, XDG_CONFIG_HOME/freelancer, ~/.config/freelancer.
func NewStore(profile string) (*Store, error) {
	if profile == "" {
		profile = DefaultProfile
	}
	if !profilePattern.MatchString(profile) {
		return nil, fmt.Errorf("invalid profile %q: allowed characters are letters, digits, dot, dash, underscore", profile)
	}

	dir := os.Getenv("FREELANCER_SESSION_DIR")
	if dir == "" {
		if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
			dir = filepath.Join(base, "freelancer")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("resolve home directory: %w", err)
			}
			dir = filepath.Join(home, ".config", "freelancer")
		}
	}
	return &Store{dir: dir, profile: profile}, nil
}

// Profile returns the active profile name.
func (s *Store) Profile() string { return s.profile }

// Path returns the session file path for the active profile.
func (s *Store) Path() string {
	return filepath.Join(s.dir, fmt.Sprintf("session-%s.json", s.profile))
}

// Load reads the stored session, returning ErrNotFound when absent.
func (s *Store) Load() (*Session, error) {
	data, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w for profile %q", ErrNotFound, s.profile)
	}
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", s.Path(), err)
	}
	if sess.Profile == "" {
		sess.Profile = s.profile
	}
	return &sess, nil
}

// Save writes the session atomically with owner-only permissions.
func (s *Store) Save(sess *Session) error {
	if sess == nil {
		return errors.New("nil session")
	}
	sess.Profile = s.profile
	now := time.Now().UTC()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	sess.UpdatedAt = now

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp session: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp session: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp session: %w", err)
	}
	if err := os.Rename(tmpName, s.Path()); err != nil {
		return fmt.Errorf("replace session: %w", err)
	}
	return nil
}

// Clear removes the stored session file. Missing files are not an error.
func (s *Store) Clear() error {
	if err := os.Remove(s.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove session: %w", err)
	}
	return nil
}
