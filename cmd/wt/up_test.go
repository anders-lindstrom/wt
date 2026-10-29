package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wt up --json opens its repository leniently, and still says what it could
// not read of the user file: on stderr, never in the one object on stdout.
func TestUpJSONWarnsAboutAnUnreadableUserFile(t *testing.T) {
	path := userConfigIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("this is = = not toml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repoWithWorktrees(t)
	args := []string{"up", "fix_wt/login-crash", "--json", "--no-fetch", "--no-push"}
	var stdout, stderr bytes.Buffer
	root := newRootCmd(args...)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	_ = root.Execute() // the run itself may refuse; the warning comes first
	if !strings.Contains(stderr.String(), "wt: every integration is off for this run") {
		t.Fatalf("no warning about the user file on stderr:\n%s\nstdout:\n%s", stderr.String(), stdout.String())
	}
	if strings.Contains(stdout.String(), "every integration is off") {
		t.Fatalf("the warning went into the JSON on stdout:\n%s", stdout.String())
	}
}
