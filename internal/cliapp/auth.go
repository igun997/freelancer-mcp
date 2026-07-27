package cliapp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/igun997/freelancer-mcp/internal/freelancer"
)

func runVersion(_ context.Context, e *env, _ []string) error {
	fmt.Fprintf(e.stdout, "freelancer %s\n", Version)
	return nil
}

func runLogin(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "login")
	user := fs.String("user", envOr("FREELANCER_USER", ""), "email or username")
	password := fs.String("password", envOr("FREELANCER_PASSWORD", ""), "account password (prefer --password-stdin)")
	passwordStdin := fs.Bool("password-stdin", false, "read the password from stdin")
	otp := fs.String("otp", "", "one-time code when two-factor auth is enabled")
	saveCreds := fs.Bool("save-credentials", false, "store credentials for unattended re-login (plain text on disk)")
	fs.Usage = func() {
		fmt.Fprintln(e.stderr, "usage: freelancer login [--user EMAIL] [--password-stdin] [--otp CODE] [--save-credentials]")
		fs.PrintDefaults()
	}
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}

	if *passwordStdin {
		reader := bufio.NewReader(e.stdin)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("read password from stdin: %w", err)
		}
		*password = strings.TrimRight(line, "\r\n")
	}
	if *user == "" {
		value, err := prompt(e, "email or username: ")
		if err != nil {
			return err
		}
		*user = value
	}
	if *password == "" {
		value, err := promptSecret(e, "password: ")
		if err != nil {
			return err
		}
		*password = value
	}
	if *user == "" || *password == "" {
		return errors.New("user and password are required")
	}

	client, store, err := e.newClient()
	if err != nil {
		return err
	}
	result, err := client.Login(ctx, *user, *password, freelancer.LoginOptions{OTP: *otp})
	if err != nil {
		return err
	}
	if *saveCreds {
		client.SaveCredentials(*user, *password)
		if err := client.Persist(); err != nil {
			return err
		}
	}
	self, err := client.EnsureSession(ctx)
	if err != nil {
		return err
	}
	if err := client.Persist(); err != nil {
		return err
	}

	if e.jsonOut {
		return writeJSON(e, map[string]any{
			"profile":      store.Profile(),
			"session_file": store.Path(),
			"user_id":      result.UserID,
			"role":         result.UserRole,
			"user":         self,
		})
	}
	fmt.Fprintf(e.stdout, "logged in as %s (%s)\n", self.Username, self.Email)
	fmt.Fprintf(e.stdout, "user id      %d\n", self.ID)
	fmt.Fprintf(e.stdout, "role         %s\n", result.UserRole)
	fmt.Fprintf(e.stdout, "profile      %s\n", store.Profile())
	fmt.Fprintf(e.stdout, "session file %s\n", store.Path())
	if *saveCreds {
		fmt.Fprintf(e.stderr, "\nWarning: credentials are stored in plain text at %s (file mode 0600).\n", store.Path())
		fmt.Fprintln(e.stderr, "Anyone who can read that file, or a backup of it, can log in as you.")
		fmt.Fprintln(e.stderr, "Run `freelancer session forget-credentials` to remove them.")
	}
	return nil
}

func runWhoami(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "whoami")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, store, err := e.newClient()
	if err != nil {
		return err
	}
	self, err := client.EnsureSession(ctx)
	if err != nil {
		return err
	}
	if err := client.Persist(); err != nil {
		return err
	}
	if e.jsonOut {
		return writeJSON(e, self)
	}
	fmt.Fprintf(e.stdout, "profile      %s\n", store.Profile())
	fmt.Fprintf(e.stdout, "user id      %d\n", self.ID)
	fmt.Fprintf(e.stdout, "username     %s\n", self.Username)
	fmt.Fprintf(e.stdout, "email        %s\n", self.Email)
	fmt.Fprintf(e.stdout, "role         %s (chosen: %s)\n", self.Role, self.ChosenRole)
	if self.Location != nil {
		country := ""
		if self.Location.Country != nil {
			country = self.Location.Country.Name
		}
		fmt.Fprintf(e.stdout, "location     %s %s\n", self.Location.City, country)
	}
	if self.PrimaryCurrency != nil {
		fmt.Fprintf(e.stdout, "currency     %s\n", self.PrimaryCurrency.Code)
	}
	if self.Status != nil {
		fmt.Fprintf(e.stdout, "verified     email=%t phone=%t payment=%t identity=%t\n",
			self.Status.EmailVerified, self.Status.PhoneVerified,
			self.Status.PaymentVerified, self.Status.IdentityVerified)
	}
	return nil
}

func runSession(ctx context.Context, e *env, args []string) error {
	sub := "show"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "show":
		return sessionShow(ctx, e, args)
	case "path":
		return sessionPath(e, args)
	case "token":
		return sessionToken(e, args)
	case "refresh":
		return sessionRefresh(ctx, e, args)
	case "clear":
		return runLogout(ctx, e, args)
	case "forget-credentials":
		return sessionForget(e, args)
	default:
		fmt.Fprintf(e.stderr, "unknown session subcommand %q\n", sub)
		fmt.Fprintln(e.stderr, "available: show, path, token, refresh, clear, forget-credentials")
		return ErrUsage
	}
}

func sessionShow(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "session show")
	check := fs.Bool("check", false, "validate the token against the API")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, store, err := e.newClient()
	if err != nil {
		return err
	}
	sess := client.Session()
	payload := map[string]any{
		"profile":           store.Profile(),
		"session_file":      store.Path(),
		"user_id":           sess.UserID,
		"username":          sess.Username,
		"email":             sess.Email,
		"role":              sess.Role,
		"has_token":         sess.Token != "",
		"saved_credentials": sess.Credentials != nil,
		"updated_at":        sess.UpdatedAt,
	}
	if *check {
		if _, err := client.Self(ctx); err != nil {
			payload["check"] = err.Error()
		} else {
			payload["check"] = "ok"
		}
	}
	if e.jsonOut {
		return writeJSON(e, payload)
	}
	fmt.Fprintf(e.stdout, "profile      %s\n", store.Profile())
	fmt.Fprintf(e.stdout, "session file %s\n", store.Path())
	fmt.Fprintf(e.stdout, "user id      %d\n", sess.UserID)
	fmt.Fprintf(e.stdout, "username     %s\n", sess.Username)
	fmt.Fprintf(e.stdout, "token        %s\n", boolLabel(sess.Token != "", "stored", "missing"))
	fmt.Fprintf(e.stdout, "credentials  %s\n", boolLabel(sess.Credentials != nil, "stored (plain text)", "not stored"))
	if value, ok := payload["check"]; ok {
		fmt.Fprintf(e.stdout, "check        %v\n", value)
	}
	return nil
}

func sessionPath(e *env, args []string) error {
	fs := newFlagSet(e, "session path")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	_, store, err := e.newClient()
	if err != nil {
		return err
	}
	fmt.Fprintln(e.stdout, store.Path())
	return nil
}

func sessionToken(e *env, args []string) error {
	fs := newFlagSet(e, "session token")
	header := fs.Bool("header", false, "print the full freelancer-auth-v2 header value")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	sess := client.Session()
	if !sess.Valid() {
		return freelancer.ErrNoSession
	}
	if *header {
		fmt.Fprintln(e.stdout, sess.AuthHeader())
		return nil
	}
	fmt.Fprintln(e.stdout, sess.Token)
	return nil
}

func sessionRefresh(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "session refresh")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, _, err := e.newClient()
	if err != nil {
		return err
	}
	if err := client.Refresh(ctx); err != nil {
		return err
	}
	self, err := client.EnsureSession(ctx)
	if err != nil {
		return err
	}
	if err := client.Persist(); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "refreshed session for %s (%d)\n", self.Username, self.ID)
	return nil
}

func sessionForget(e *env, args []string) error {
	fs := newFlagSet(e, "session forget-credentials")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, store, err := e.newClient()
	if err != nil {
		return err
	}
	client.ForgetCredentials()
	if err := client.Persist(); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "removed stored credentials from %s\n", store.Path())
	return nil
}

func runLogout(_ context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "logout")
	if err := parseFlags(fs, args); err != nil {
		return usageOrHelp(err)
	}
	client, store, err := e.newClient()
	if err != nil {
		return err
	}
	if err := client.Logout(); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "cleared session %s\n", store.Path())
	return nil
}

func prompt(e *env, label string) (string, error) {
	fmt.Fprint(e.stderr, label)
	reader := bufio.NewReader(e.stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func promptSecret(e *env, label string) (string, error) {
	if file, ok := e.stdin.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		fmt.Fprint(e.stderr, label)
		data, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(e.stderr)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return prompt(e, label)
}

func boolLabel(value bool, yes, no string) string {
	if value {
		return yes
	}
	return no
}
