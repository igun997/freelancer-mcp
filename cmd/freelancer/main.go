// Command freelancer is a CLI for freelancer.com accounts: profile, projects,
// bids, messages, milestones, and money.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/igun997/freelancer-mcp/internal/cliapp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cliapp.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Stdin)
	switch {
	case err == nil:
		return
	case errors.Is(err, cliapp.ErrUsage):
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
