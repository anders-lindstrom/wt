package commands

import (
	"io"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

func TestBranchAppliesDefaultType(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.FixtureRepo(t, testkit.MinimalConf), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got, err := kit.Branch(ctx, "login-crash")
	if err != nil {
		t.Fatal(err)
	}
	if got != "feat_wt/login-crash" {
		t.Errorf("got %q", got)
	}
	if got, _ = kit.Branch(ctx, "fix/login-crash"); got != "fix_wt/login-crash" {
		t.Errorf("explicit type: got %q", got)
	}
}

// Superset offers no type when a workspace is created — every branch it makes
// takes one fixed prefix — so the type has to be readable from the name.
func TestBranchReadsTheTypeOutOfABareName(t *testing.T) {
	t.Parallel()
	ctx, _ := kit.Open(testkit.FixtureRepo(t, testkit.MinimalConf), io.Discard)
	if got, _ := kit.Branch(ctx, "fix_dev-123"); got != "fix_wt/dev-123" {
		t.Errorf("got %q, want fix_wt/dev-123", got)
	}
	if got, _ := kit.Branch(ctx, "review_sentry"); got != "feat_wt/review_sentry" {
		t.Errorf("a name whose head is not a type must survive whole: %q", got)
	}
	if got, _ := kit.Branch(ctx, "feat/fix_dev-123"); got != "feat_wt/fix_dev-123" {
		t.Errorf("an explicit type must win: %q", got)
	}
}

// Reading a type out of the name must not lose the worktrees that already
// carry Superset's prefix, or `wt migrate fix_dev-123` stops finding the very
// worktree the inference is meant to serve.
func TestPathStillFindsAWorktreeNamedTheWayItWasCreated(t *testing.T) {
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main, io.Discard)
	superset := filepath.Join(ctx.Repo.Parent, "demo_wt", "demo", "feat_wt", "fix_dev-123")
	testkit.GitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/fix_dev-123", superset)

	got, err := kit.Path(ctx, "fix_dev-123")
	if err != nil {
		t.Fatal(err)
	}
	if got != superset {
		t.Errorf("got %q, want %q", got, superset)
	}
}

func TestBranchRejectsUnknownType(t *testing.T) {
	t.Parallel()
	ctx, _ := kit.Open(testkit.FixtureRepo(t, testkit.MinimalConf), io.Discard)
	if _, err := kit.Branch(ctx, "wibble/thing"); err == nil {
		t.Error("want error for a type not in WORKTREE_TYPES")
	}
}

// Branch, like Path, finds a worktree Superset made by the name it was given
// before reading a type out of that name.
func TestBranchStillFindsAWorktreeNamedTheWayItWasCreated(t *testing.T) {
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main, io.Discard)
	superset := filepath.Join(ctx.Repo.Parent, "demo_wt", "demo", "feat_wt", "fix_dev-123")
	testkit.GitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/fix_dev-123", superset)

	if got, err := kit.Branch(ctx, "fix_dev-123"); err != nil || got != "feat_wt/fix_dev-123" {
		t.Errorf("Branch = %q, %v; want feat_wt/fix_dev-123", got, err)
	}
}

func TestPathForNonexistentWorkIsCanonical(t *testing.T) {
	t.Parallel()
	main := testkit.FixtureRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main, io.Discard)
	got, err := kit.Path(ctx, "fix/login-crash")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(main), "demo_wt", "fix_wt", "login-crash")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An existing worktree wins over the canonical path, whatever layout it is in.
// This is what keeps the legacy path shapes working with no migration —
// including Superset's <repo>_wt/<repo>/<type>_wt/<work>.
func TestPathPrefersAnExistingWorktreeInAnyLayout(t *testing.T) {
	t.Parallel()
	main := testkit.FixtureRepo(t, testkit.MinimalConf)
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(main, "config", "user.email", "t@example.com")
	run(main, "config", "user.name", "T")
	run(main, "commit", "-q", "--allow-empty", "-m", "init")
	legacy := filepath.Join(filepath.Dir(main), "demo-oldshape")
	run(main, "worktree", "add", "-q", "-b", "fix_wt/oldshape", legacy)

	ctx, _ := kit.Open(main, io.Discard)
	got, err := kit.Path(ctx, "fix/oldshape")
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Errorf("got %q, want the existing legacy path %q", got, legacy)
	}
}

// Superset's own shape must resolve too, since it is hardcoded in that tool.
func TestPathResolvesSupersetShape(t *testing.T) {
	t.Parallel()
	main := testkit.FixtureRepo(t, testkit.MinimalConf)
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(main, "config", "user.email", "t@example.com")
	run(main, "config", "user.name", "T")
	run(main, "commit", "-q", "--allow-empty", "-m", "init")
	superset := filepath.Join(filepath.Dir(main), "demo_wt", "demo", "feat_wt", "arch")
	run(main, "worktree", "add", "-q", "-b", "feat_wt/arch", superset)

	ctx, _ := kit.Open(main, io.Discard)
	got, err := kit.Path(ctx, "feat/arch")
	if err != nil {
		t.Fatal(err)
	}
	if got != superset {
		t.Errorf("got %q, want superset shape %q", got, superset)
	}
}
