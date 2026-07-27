package mcpserver

import (
	"context"
	"encoding/json"
	"io"

	"github.com/igun997/freelancer-mcp/internal/freelancer"
	"github.com/igun997/freelancer-mcp/internal/session"
)

// Options configure the MCP server.
type Options struct {
	Profile string
	Version string
	Logf    func(format string, args ...any)
}

// Server serves freelancer.com tools over MCP.
type Server struct {
	version   string
	transport *Transport
	client    *freelancer.Client
	store     *session.Store
	logf      func(format string, args ...any)
}

// New builds a server bound to a session profile.
func New(r io.Reader, w io.Writer, opts Options) (*Server, error) {
	store, err := session.NewStore(opts.Profile)
	if err != nil {
		return nil, err
	}
	client, err := freelancer.New(freelancer.DefaultConfig(), store)
	if err != nil {
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	return &Server{
		version:   version,
		transport: NewTransport(r, w),
		client:    client,
		store:     store,
		logf:      logf,
	}, nil
}

// Close persists the session.
func (s *Server) Close() {
	if err := s.client.Persist(); err != nil {
		s.logf("persist session: %v", err)
	}
}

func rawOrNull(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// ensureSession verifies the stored token, refreshing when credentials allow.
func (s *Server) ensureSession(ctx context.Context) error {
	_, err := s.client.EnsureSession(ctx)
	return err
}
