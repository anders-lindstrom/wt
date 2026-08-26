package commands

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestWorkNameFromBranch(t *testing.T) {
	for in, want := range map[string]string{
		"feature/pr-123":     "feature-pr-123",
		"fix_wt/login-crash": "login-crash",
		"main":               "main",
		"a//b":               "a-b",
		"--weird--":          "weird",
	} {
		if got := WorkNameFromBranch(in, "_wt"); got != want {
			t.Errorf("WorkNameFromBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckoutPutsAWorktreeOnAnExistingBranch(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	gitIn(t, ctx.Repo.MainRoot, "branch", "feature/pr-123")

	var buf bytes.Buffer
	path, err := Checkout(ctx, "feature/pr-123", "", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "feature-pr-123")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if got := ctx.Repo.BranchAt(path); got != "feature/pr-123" {
		t.Errorf("branch = %q, want the existing branch untouched", got)
	}
}

func TestCheckoutHonoursAnExplicitWorkName(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	gitIn(t, ctx.Repo.MainRoot, "branch", "feature/pr-123")

	var buf bytes.Buffer
	path, err := Checkout(ctx, "feature/pr-123", "review", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "review" {
		t.Errorf("path = %q, want it to end in review", path)
	}
}

// Checkout must never create a branch: it exists to work on one that is there.
func TestCheckoutRefusesAMissingBranch(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if _, err := Checkout(ctx, "no-such-branch", "", NewOptions{NoSetup: true}, &buf); err == nil {
		t.Fatal("want a refusal for a branch that does not exist")
	}
	if ctx.Repo.BranchExists("no-such-branch") {
		t.Error("checkout must not create the branch")
	}
}

func TestCheckoutRefusesAnOccupiedPath(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	gitIn(t, ctx.Repo.MainRoot, "branch", "dup")
	var buf bytes.Buffer
	if _, err := Checkout(ctx, "dup", "", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := Checkout(ctx, "dup", "", NewOptions{NoSetup: true}, &buf); err == nil {
		t.Error("want a refusal when the worktree already exists")
	}
}
