package main

import (
	"bytes"
	"strings"
	"testing"
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root := newRootCmd()
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
	if !strings.Contains(out, "<type>/<work>") {
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
	if !strings.Contains(msg, "<type>/<work>") {
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
