package cliapp

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("FREELANCER_SESSION_DIR", t.TempDir())
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	err := Run(context.Background(), args, stdout, stderr, strings.NewReader(""))
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
