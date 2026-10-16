package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/about"
	"github.com/anders-lindstrom/wt/internal/commands/synckeep"
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root := newRootCmd(args...)
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestRootListsSubcommands(t *testing.T) {
	out, err := runCmd(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"config", "path", "branch"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help does not mention %q:\n%s", want, out)
		}
	}
}

func TestVersion(t *testing.T) {
	out, err := runCmd(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("version printed nothing")
	}
}

func TestUnknownCommandIsAnError(t *testing.T) {
	if _, err := runCmd(t, "wibble"); err == nil {
		t.Error("want an error for an unknown command")
	}
}

// An argument mistake is exactly when usage helps, so it must be shown.
func TestArgErrorsShowUsage(t *testing.T) {
	for _, args := range [][]string{{"path"}, {"branch"}, {"new"}, {"adopt"}, {"migrate"}} {
		out, err := runCmd(t, args...)
		if err == nil {
			t.Errorf("%v: want an error", args)
			continue
		}
		if !strings.Contains(out, "Usage:") {
			t.Errorf("%v: no usage shown:\n%s", args, out)
		}
	}
}

func TestArgErrorNamesWhatIsNeeded(t *testing.T) {
	out, _ := runCmd(t, "path")
	if !strings.Contains(out, "<work>") {
		t.Errorf("usage does not name the argument:\n%s", out)
	}
}

// A runtime failure is not an argument mistake; dumping usage there is noise.
func TestRuntimeErrorsDoNotShowUsage(t *testing.T) {
	t.Chdir(t.TempDir())
	out, err := runCmd(t, "config")
	if err == nil {
		t.Fatal("want an error outside a repository")
	}
	if strings.Contains(out, "Usage:") {
		t.Errorf("usage should be suppressed for a runtime error:\n%s", out)
	}
}

func TestArgErrorMessageIsHelpful(t *testing.T) {
	out, err := runCmd(t, "path")
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "wt ") {
		t.Errorf("message repeats the command name, which Execute already prefixes: %q", msg)
	}
	if !strings.Contains(msg, "<work>") {
		t.Errorf("message does not name what is needed: %q", msg)
	}
	if !strings.Contains(msg, "for example") {
		t.Errorf("message gives no example: %q", msg)
	}
	if strings.Contains(msg, "accepts 1 arg(s)") {
		t.Errorf("still cobra's default message: %q", msg)
	}
	_ = out
}

func TestTooManyArgsIsAlsoExplained(t *testing.T) {
	_, err := runCmd(t, "path", "a", "b")
	if err == nil || !strings.Contains(err.Error(), "takes") {
		t.Errorf("got %v", err)
	}
}

// cd and exec are real commands — they just happen to be implemented in the
// shell layer. Hiding them from help means the two most-used commands are
// undiscoverable.
func TestHelpListsTheShellCommands(t *testing.T) {
	out, err := runCmd(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cd", "exec"} {
		if !strings.Contains(out, "\n  "+want+" ") {
			t.Errorf("--help does not list %q:\n%s", want, out)
		}
	}
}

func TestShellCommandsSayWhereTheyLive(t *testing.T) {
	for _, name := range []string{"cd", "exec"} {
		out, err := runCmd(t, name, "--help")
		if err != nil {
			t.Fatalf("%s --help: %v", name, err)
		}
		if !strings.Contains(out, "wt.sh") {
			t.Errorf("%s --help does not say how to enable it:\n%s", name, out)
		}
	}
}

// Reaching the binary means the shell layer was never sourced; say so.
func TestShellCommandsExplainThemselvesWhenRunFromTheBinary(t *testing.T) {
	for _, name := range []string{"cd", "exec"} {
		_, err := runCmd(t, name, "something")
		if err == nil {
			t.Fatalf("%s: want an error from the binary", name)
		}
		if !strings.Contains(err.Error(), "wt.sh") {
			t.Errorf("%s: error does not point at the shell layer: %v", name, err)
		}
	}
}

// `wt about` is the human surface: the version, and what landed recently.
func TestAboutPrintsTheVersionAndWhatIsNew(t *testing.T) {
	out, err := runCmd(t, "about")
	if err != nil {
		t.Fatalf("about: %v", err)
	}
	if !strings.Contains(out, version) {
		t.Errorf("about does not print the version %q:\n%s", version, out)
	}
	if !strings.Contains(out, about.NewestHeading()) {
		t.Errorf("about does not print the what's-new heading %q:\n%s",
			about.NewestHeading(), out)
	}
}

// `wt version` is the machine surface, and stays one bare line so a script can
// read it.
func TestVersionStaysOneBareLine(t *testing.T) {
	out, err := runCmd(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.TrimSpace(out) != version {
		t.Errorf("version printed %q, want just %q", out, version)
	}
}

func TestVersionFlagMatchesCommand(t *testing.T) {
	flag, err := runCmd(t, "--version")
	if err != nil {
		t.Fatal(err)
	}
	command, err := runCmd(t, "version")
	if err != nil || flag != command {
		t.Fatalf("flag %q, command %q, error %v", flag, command, err)
	}
}

func TestHelpWinsOverFlagErrors(t *testing.T) {
	for _, args := range [][]string{
		{"new", "--bogus", "--help"}, {"new", "--bogus", "-h"},
		{"new", "--expect", "--help"},
	} {
		out, err := runCmd(t, args...)
		if err != nil || !strings.Contains(out, "Usage:") {
			t.Errorf("%v: error %v, output %q", args, err, out)
		}
	}
	_, err := runCmd(t, "new", "--bogus", "--", "--help")
	if err == nil {
		t.Fatal("help after -- hid an invalid flag")
	}
}

func TestErrorStatus(t *testing.T) {
	for _, args := range [][]string{{"new"}, {"version", "extra"}, {"new", "--bogus"}, {"wibble"}, {"remove"}, {"sync", "--push"}, {"config", "set", "superset", "sometimes"}} {
		_, err := runCmd(t, args...)
		if err == nil || errorStatus(err) != 2 {
			t.Errorf("%v: error %v, want usage status 2", args, err)
		}
	}
	if got := errorStatus(synckeep.ErrNoLaunchd); got != 3 {
		t.Errorf("launchd status %d, want 3", got)
	}
	t.Chdir(t.TempDir())
	_, err := runCmd(t, "config")
	if err == nil || errorStatus(err) != 1 {
		t.Fatalf("runtime error %v, want status 1", err)
	}
}

func TestMutuallyExclusiveFlagsAreUsageErrors(t *testing.T) {
	out, err := runCmd(t, "remove", "foo", "--yes", "--dry-run")
	if err == nil || errorStatus(err) != 2 {
		t.Fatalf("error %v, want usage status 2", err)
	}
	if !strings.Contains(err.Error(), "yes") || !strings.Contains(err.Error(), "dry-run") || !strings.Contains(out, "Usage:") {
		t.Fatalf("error %v, output %q", err, out)
	}
}
