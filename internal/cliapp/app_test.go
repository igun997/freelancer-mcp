package cliapp

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return runWithInput(t, "", args...)
}

func runWithInput(t *testing.T, input string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("FREELANCER_SESSION_DIR", t.TempDir())
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	err := Run(context.Background(), args, stdout, stderr, strings.NewReader(input))
	return stdout.String(), stderr.String(), err
}

func TestNoArgsPrintsUsage(t *testing.T) {
	_, stderr, err := run(t)
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("err = %v, want ErrUsage", err)
	}
	for _, want := range []string{"login", "profile", "projects", "bid", "messages", "api"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("usage missing %q:\n%s", want, stderr)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	_, stderr, err := run(t, "teleport")
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("err = %v, want ErrUsage", err)
	}
	if !strings.Contains(stderr, `unknown command "teleport"`) {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestVersionCommand(t *testing.T) {
	stdout, _, err := run(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.HasPrefix(stdout, "freelancer ") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestCommandsWithoutSessionFail(t *testing.T) {
	for _, args := range [][]string{
		{"whoami"},
		{"quota"},
		{"bids"},
		{"messages", "threads"},
	} {
		if _, _, err := run(t, args...); err == nil {
			t.Errorf("%v should fail without a session", args)
		}
	}
}

func TestUsageErrorsForMissingRequiredFlags(t *testing.T) {
	for _, args := range [][]string{
		{"project", "get"},
		{"bid", "place"},
		{"bid", "retract"},
		{"messages", "send"},
		{"profile", "update"},
		{"profile", "currency"},
		{"api"},
	} {
		_, _, err := run(t, args...)
		if !errors.Is(err, ErrUsage) {
			t.Errorf("%v err = %v, want ErrUsage", args, err)
		}
	}
}

func TestSessionSubcommandValidation(t *testing.T) {
	_, stderr, err := run(t, "session", "explode")
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("err = %v, want ErrUsage", err)
	}
	if !strings.Contains(stderr, "unknown session subcommand") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestProfileSubcommandValidation(t *testing.T) {
	_, stderr, err := run(t, "profile", "moon")
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("err = %v, want ErrUsage", err)
	}
	if !strings.Contains(stderr, "unknown profile subcommand") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestLogoutOnCleanProfileSucceeds(t *testing.T) {
	stdout, _, err := run(t, "logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(stdout, "cleared session") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestLoginPromptsForOTPAfterServerChallenge(t *testing.T) {
	testLoginPromptsForOTP(t, http.StatusUnauthorized, `{"status":"error","message":"Two-factor authentication code required","error_code":"AUTH_OTP_REQUIRED"}`)
}

func TestLoginPromptsForOTPAfterNoTokenChallenge(t *testing.T) {
	// Freelancer can answer the first login attempt with HTTP 200 and an OTP
	// challenge body instead of an error envelope. That must still prompt for the
	// code, not stop at "response carried no token".
	testLoginPromptsForOTP(t, http.StatusOK, `{"status":"success","result":{"otp_required":true,"message":"One-time code required"}}`)
}

func testLoginPromptsForOTP(t *testing.T, challengeStatus int, challengeBody string) {
	t.Helper()
	loginCalls := 0
	var otpSeen string
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/device", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","result":{"token":"device-jwt"}}`))
	})
	mux.HandleFunc("/ajax-api/auth/login.php", func(w http.ResponseWriter, r *http.Request) {
		loginCalls++
		_ = r.ParseForm()
		otpSeen = r.PostForm.Get("otp")
		if otpSeen == "" {
			w.WriteHeader(challengeStatus)
			_, _ = w.Write([]byte(challengeBody))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","result":{"token":"hash==","user":42,"userRole":"freelancer"}}`))
	})
	mux.HandleFunc("/api/users/0.1/self/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","result":{"id":42,"username":"me","email":"me@example.com","role":"freelancer"}}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Setenv("FREELANCER_WEB_BASE", server.URL)
	t.Setenv("FREELANCER_API_BASE", server.URL+"/api")

	stdout, stderr, err := runWithInput(t, "secret\n654321\n", "login", "--user", "me@example.com")
	if err != nil {
		t.Fatalf("login: %v\nstderr:\n%s", err, stderr)
	}
	if loginCalls != 2 {
		t.Fatalf("login calls = %d, want 2", loginCalls)
	}
	if otpSeen != "654321" {
		t.Fatalf("otp = %q, want 654321", otpSeen)
	}
	if !strings.Contains(stderr, "one-time code") {
		t.Fatalf("stderr missing OTP prompt: %q", stderr)
	}
	if !strings.Contains(stdout, "logged in as me") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestIntList(t *testing.T) {
	got, err := intList("3, 248 ,305")
	if err != nil {
		t.Fatalf("intList: %v", err)
	}
	if len(got) != 3 || got[1] != 248 {
		t.Errorf("intList = %v", got)
	}
	if _, err := intList("3,abc"); err == nil {
		t.Error("expected an error for a non-numeric id")
	}
	if got, err := intList("  "); err != nil || got != nil {
		t.Errorf("empty input = %v, %v", got, err)
	}
}

func TestStringList(t *testing.T) {
	got := stringList("fixed, hourly ,")
	if len(got) != 2 || got[1] != "hourly" {
		t.Errorf("stringList = %v", got)
	}
	if stringList("") != nil {
		t.Error("empty input should return nil")
	}
}

func TestPermuteArgsMovesFlagsFirst(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("method", "GET", "")
	fs.Bool("json", false, "")

	got := permuteArgs(fs, []string{"/users/0.1/self/", "--method", "PUT", "--json"})
	want := []string{"--method", "PUT", "--json", "--", "/users/0.1/self/"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("permuteArgs = %v, want %v", got, want)
	}
}

func TestMessagesActionAcceptsActionAndThreadTogether(t *testing.T) {
	// Regression: --action was parsed in one flag set and the same args were
	// re-parsed by another that had no --thread, so every non-read action failed
	// with "flag provided but not defined".
	_, stderr, err := run(t, "messages", "action", "--action", "star", "--thread", "1")
	if strings.Contains(stderr, "not defined") {
		t.Fatalf("flag parsing rejected valid flags: %s", stderr)
	}
	if errors.Is(err, ErrUsage) {
		t.Fatalf("valid invocation reported a usage error: %s", stderr)
	}
	// Without a session the command must fail on auth, not on parsing.
	if err == nil {
		t.Fatal("expected a session error")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("err = %v, want a session error", err)
	}
}

func TestMessagesActionStillRequiresThreadAndAction(t *testing.T) {
	if _, _, err := run(t, "messages", "action", "--action", "star"); !errors.Is(err, ErrUsage) {
		t.Errorf("missing thread err = %v, want ErrUsage", err)
	}
	if _, _, err := run(t, "messages", "action", "--thread", "1"); !errors.Is(err, ErrUsage) {
		t.Errorf("missing action err = %v, want ErrUsage", err)
	}
}

func TestProfileCVListAndSchoolsAreWired(t *testing.T) {
	for _, args := range [][]string{
		{"profile", "cv", "--section", "experience", "--list"},
		{"profile", "schools", "--country", "ID", "--query", "komputer"},
	} {
		_, stderr, err := run(t, args...)
		if errors.Is(err, ErrUsage) {
			t.Errorf("%v reported a usage error: %s", args, stderr)
		}
	}
	if _, _, err := run(t, "profile", "cv", "--section", "hobbies", "--list"); err == nil {
		t.Error("expected an error for an unknown cv section")
	}
}
