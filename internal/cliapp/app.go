// Package cliapp implements the freelancer CLI.
package cliapp

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/igun997/freelancer-mcp/internal/freelancer"
	"github.com/igun997/freelancer-mcp/internal/session"
)

// ErrUsage signals invalid invocation; callers should exit with status 2.
var ErrUsage = errors.New("usage")

// Version is set at build time with -ldflags.
var Version = "dev"

type env struct {
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader

	profile string
	jsonOut bool
}

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, e *env, args []string) error
}

func commands() []command {
	return []command{
		{"login", "authenticate and store a session", runLogin},
		{"whoami", "show the authenticated account", runWhoami},
		{"session", "inspect, refresh, or clear stored sessions", runSession},
		{"logout", "delete the stored session", runLogout},
		{"profile", "read and update bio, tagline, hourly rate, skills", runProfile},
		{"projects", "search the active project feed", runProjects},
		{"project", "project detail, bids, own postings", runProject},
		{"bids", "list your bids", runBids},
		{"bid", "place, edit, retract, award, or highlight a bid", runBid},
		{"quota", "remaining monthly bid allowance", runQuota},
		{"messages", "threads, history, send, mark read", runMessages},
		{"milestones", "escrow milestones and payment requests", runMilestones},
		{"money", "balances, invoices, payout accounts, membership", runMoney},
		{"notifications", "notification and activity feed", runNotifications},
		{"reviews", "reviews written about you", runReviews},
		{"skills", "search the skill (job) catalogue", runSkills},
		{"currencies", "list supported currencies", runCurrencies},
		{"freelancers", "search the freelancer directory", runFreelancers},
		{"api", "send an authenticated request to any endpoint", runAPI},
		{"version", "print the CLI version", runVersion},
	}
}

// Run dispatches a subcommand.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, stdin io.Reader) error {
	e := &env{stdout: stdout, stderr: stderr, stdin: stdin, profile: session.DefaultProfile}

	root := flag.NewFlagSet("freelancer", flag.ContinueOnError)
	root.SetOutput(stderr)
	root.StringVar(&e.profile, "profile", envOr("FREELANCER_PROFILE", session.DefaultProfile), "session profile name")
	root.BoolVar(&e.jsonOut, "json", false, "emit JSON output where supported")
	root.Usage = func() { printUsage(stderr, root) }

	if err := root.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return ErrUsage
	}
	rest := root.Args()
	if len(rest) == 0 {
		printUsage(stderr, root)
		return ErrUsage
	}

	name := rest[0]
	for _, cmd := range commands() {
		if cmd.name == name {
			return cmd.run(ctx, e, rest[1:])
		}
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n", name)
	printUsage(stderr, root)
	return ErrUsage
}

func printUsage(w io.Writer, root *flag.FlagSet) {
	fmt.Fprintf(w, "freelancer %s - freelancer.com CLI\n\n", Version)
	fmt.Fprintln(w, "usage: freelancer [global flags] <command> [flags]")
	fmt.Fprintln(w, "\ncommands:")
	cmds := commands()
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].name < cmds[j].name })
	for _, cmd := range cmds {
		fmt.Fprintf(w, "  %-14s %s\n", cmd.name, cmd.summary)
	}
	fmt.Fprintln(w, "\nglobal flags:")
	root.PrintDefaults()
	fmt.Fprintln(w, "\nenvironment:")
	fmt.Fprintln(w, "  FREELANCER_USER, FREELANCER_PASSWORD   login credentials")
	fmt.Fprintln(w, "  FREELANCER_PROFILE                    default profile name")
	fmt.Fprintln(w, "  FREELANCER_SESSION_DIR                session storage directory")
	fmt.Fprintln(w, "  FREELANCER_API_BASE, FREELANCER_WEB_BASE, FREELANCER_USER_AGENT")
	fmt.Fprintln(w, "  FREELANCER_TIMEOUT                    request timeout, e.g. 45s")
}

// newClient builds a client for the active profile.
func (e *env) newClient() (*freelancer.Client, *session.Store, error) {
	store, err := session.NewStore(e.profile)
	if err != nil {
		return nil, nil, err
	}
	client, err := freelancer.New(freelancer.DefaultConfig(), store)
	if err != nil {
		return nil, nil, err
	}
	return client, store, nil
}

// newFlagSet builds a subcommand flag set that also accepts the global flags,
// so `freelancer whoami --json` and `freelancer --json whoami` both work.
func newFlagSet(e *env, name string) *flag.FlagSet {
	fs := flag.NewFlagSet("freelancer "+name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.StringVar(&e.profile, "profile", e.profile, "session profile name")
	fs.BoolVar(&e.jsonOut, "json", e.jsonOut, "emit JSON output where supported")
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(permuteArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return flag.ErrHelp
		}
		return ErrUsage
	}
	return nil
}

// permuteArgs moves flags ahead of positional arguments. The standard flag
// package stops parsing at the first positional, so `api GET /path --limit 5`
// would otherwise silently ignore --limit.
func permuteArgs(fs *flag.FlagSet, args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case len(arg) > 1 && strings.HasPrefix(arg, "-"):
			flags = append(flags, arg)
			name := strings.TrimLeft(arg, "-")
			if strings.Contains(name, "=") || isBoolFlag(fs, name) {
				continue
			}
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) == 0 {
		return flags
	}
	return append(flags, append([]string{"--"}, positional...)...)
}

func isBoolFlag(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	boolFlag, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && boolFlag.IsBoolFlag()
}

func usageOrHelp(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func writeJSON(e *env, value any) error {
	enc := json.NewEncoder(e.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func writeRaw(e *env, raw json.RawMessage) error {
	var pretty any
	if err := json.Unmarshal(raw, &pretty); err != nil {
		fmt.Fprintln(e.stdout, string(raw))
		return nil
	}
	return writeJSON(e, pretty)
}

// intList parses "3,77,305" into ids.
func intList(value string) ([]int64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid id %q", part)
		}
		out = append(out, id)
	}
	return out, nil
}

// stringList parses "fixed,hourly" into a slice.
func stringList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
