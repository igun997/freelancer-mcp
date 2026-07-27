// Command freelancer-mcp serves freelancer.com account tools over the Model
// Context Protocol on stdio. Authenticate first with `freelancer login`.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/igun997/freelancer-mcp/internal/mcpserver"
	"github.com/igun997/freelancer-mcp/internal/session"
)

// version is overridable at build time with -ldflags.
var version = "dev"

func main() {
	profile := flag.String("profile", envOr("FREELANCER_PROFILE", session.DefaultProfile), "session profile name")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("freelancer-mcp", version)
		return
	}

	logger := log.New(os.Stderr, "freelancer-mcp: ", log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server, err := mcpserver.New(os.Stdin, os.Stdout, mcpserver.Options{
		Profile: *profile,
		Version: version,
		Logf:    func(format string, args ...any) { logger.Printf(format, args...) },
	})
	if err != nil {
		logger.Fatalf("startup: %v", err)
	}
	defer server.Close()

	if err := server.Serve(ctx); err != nil {
		logger.Fatalf("serve: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
