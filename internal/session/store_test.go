package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePathHonoursSessionDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FREELANCER_SESSION_DIR", dir)

	store, err := NewStore("work")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	want := filepath.Join(dir, "session-work.json")
	if store.Path() != want {
		t.Errorf("path = %q, want %q", store.Path(), want)
	}
	if store.Profile() != "work" {
		t.Errorf("profile = %q", store.Profile())
	}
}

func TestNewStoreRejectsBadProfile(t *testing.T) {
	t.Setenv("FREELANCER_SESSION_DIR", t.TempDir())
	if _, err := NewStore("../escape"); err == nil {
		t.Fatal("expected an error for a path-like profile")
	}
}

func TestSaveLoadRoundTripWithOwnerOnlyPermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FREELANCER_SESSION_DIR", dir)
	store, err := NewStore(DefaultProfile)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	if _, err := store.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("load on empty dir = %v, want ErrNotFound", err)
	}

	in := &Session{UserID: 26605882, Token: "hash==", Username: "me", Role: "freelancer"}
	if err := store.Save(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("session file mode = %o, want 600", perm)
	}

	out, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.UserID != in.UserID || out.Token != in.Token || out.Profile != DefaultProfile {
		t.Fatalf("round trip mismatch: %+v", out)
	}
	if out.CreatedAt.IsZero() || out.UpdatedAt.IsZero() {
		t.Error("timestamps not stamped on save")
	}

	if err := store.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("clear on missing file must be a no-op, got %v", err)
	}
}

func TestSessionValidAndAuthHeader(t *testing.T) {
	var nilSession *Session
	if nilSession.Valid() {
		t.Error("nil session must not be valid")
	}
	incomplete := &Session{UserID: 1}
	if incomplete.Valid() || incomplete.AuthHeader() != "" {
		t.Error("session without a token must not be valid")
	}
	full := &Session{UserID: 26605882, Token: "abc=="}
	if !full.Valid() {
		t.Error("session with id and token must be valid")
	}
	if got := full.AuthHeader(); got != "26605882;abc==" {
		t.Errorf("auth header = %q", got)
	}
}
