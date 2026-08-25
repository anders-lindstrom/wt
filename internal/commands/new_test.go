package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func committedRepo(t *testing.T, conf string) string {
	t.Helper()
	main := fixtureRepo(t, conf)
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "T")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	return main
}

func TestNewCreatesCanonicalWorktreeAndBranch(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	path, err := New(ctx, "fix/login-crash", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "login-crash")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("worktree not created: %v", err)
	}
	if got := ctx.Repo.BranchAt(path); got != "fix_wt/login-crash" {
		t.Errorf("branch = %q", got)
	}
}

func TestNewBareWorkNameTakesDefaultType(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "thing", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := ctx.Repo.BranchAt(path); got != "feat_wt/thing" {
		t.Errorf("branch = %q, want feat_wt/thing", got)
	}
}

func TestNewRefusesDuplicateWork(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if _, err := New(ctx, "fix/dup", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := New(ctx, "fix/dup", NewOptions{NoSetup: true}, &buf); err == nil {
		t.Error("want an error creating the same work twice")
	}
}

func TestNewRejectsUnknownType(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if _, err := New(ctx, "wibble/thing", NewOptions{NoSetup: true}, &buf); err == nil {
		t.Error("want an error for an unknown type")
	}
}
