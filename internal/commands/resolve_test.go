package commands

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBranchAppliesDefaultType(t *testing.T) {
	ctx, err := Open(fixtureRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Branch(ctx, "login-crash")
	if err != nil {
		t.Fatal(err)
	}
	if got != "feat_wt/login-crash" {
		t.Errorf("got %q", got)
	}
	if got, _ = Branch(ctx, "fix/login-crash"); got != "fix_wt/login-crash" {
		t.Errorf("explicit type: got %q", got)
	}
}

// Superset offers no type when a workspace is created — every branch it makes
// takes one fixed prefix — so the type has to be readable from the name.
func TestBranchReadsTheTypeOutOfABareName(t *testing.T) {
	ctx, _ := Open(fixtureRepo(t, minimalConf))
	if got, _ := Branch(ctx, "fix_dev-123"); got != "fix_wt/dev-123" {
		t.Errorf("got %q, want fix_wt/dev-123", got)
	}
	if got, _ := Branch(ctx, "review_sentry"); got != "feat_wt/review_sentry" {
		t.Errorf("a name whose head is not a type must survive whole: %q", got)
	}
	if got, _ := Branch(ctx, "feat/fix_dev-123"); got != "feat_wt/fix_dev-123" {
		t.Errorf("an explicit type must win: %q", got)
	}
}

// Reading a type out of the name must not lose the worktrees that already
// carry Superset's prefix, or `wt migrate fix_dev-123` stops finding the very
// worktree the inference is meant to serve.
func TestPathStillFindsAWorktreeNamedTheWayItWasCreated(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	superset := filepath.Join(ctx.Repo.Parent, "demo_wt", "demo", "feat_wt", "fix_dev-123")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/fix_dev-123", superset)

	got, err := Path(ctx, "fix_dev-123")
	if err != nil {
		t.Fatal(err)
	}
	if got != superset {
		t.Errorf("got %q, want %q", got, superset)
	}
}

func TestBranchRejectsUnknownType(t *testing.T) {
	ctx, _ := Open(fixtureRepo(t, minimalConf))
	if _, err := Branch(ctx, "wibble/thing"); err == nil {
		t.Error("want error for a type not in WORKTREE_TYPES")
	}
}

// The WORK column of `wt list` names the worktree whatever its type. The
// default-type fallback used to send a bare name to feat_wt/ and print a path
// that did not exist while the worktree sat under chore_wt/.
func TestPathAndBranchPreferAnExistingWorktreeOfAnotherType(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")
	want, err := Locate(ctx, "chore/wt-migration")
	if err != nil {
		t.Fatal(err)
	}

	for _, arg := range []string{"wt-migration", "chore/wt-migration", "chore_wt/wt-migration", want.Path} {
		if got, err := Path(ctx, arg); err != nil || got != want.Path {
			t.Errorf("Path(%q) = %q, %v; want %q", arg, got, err, want.Path)
		}
		if got, err := Branch(ctx, arg); err != nil || got != "chore_wt/wt-migration" {
			t.Errorf("Branch(%q) = %q, %v; want chore_wt/wt-migration", arg, got, err)
		}
	}
}

// One work name under two types is refused exactly as Locate refuses it,
// rather than resolved to whichever the default type implies.
func TestPathAndBranchRefuseAnAmbiguousWorkName(t *testing.T) {
	ctx := locatable(t, "feat/arch", "fix/arch")

	for name, resolve := range map[string]func(*Context, string) (string, error){"Path": Path, "Branch": Branch} {
		_, err := resolve(ctx, "arch")
		if err == nil {
			t.Fatalf("%s: want an error for a work name under two types", name)
		}
		for _, want := range []string{"matches 2 worktrees", "feat_wt/arch", "fix_wt/arch"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error should contain %q, got: %v", name, want, err)
			}
		}
	}
	if got, err := Path(ctx, "fix/arch"); err != nil || !strings.HasSuffix(got, filepath.Join("fix_wt", "arch")) {
		t.Errorf("an exact type must still resolve: %q, %v", got, err)
	}
}

// With no worktree to find, a bare name still takes the default type, even
// while other worktrees exist.
func TestPathAndBranchFallBackToTheDefaultTypeWhenNothingExists(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")

	if got, err := Branch(ctx, "login-crash"); err != nil || got != "feat_wt/login-crash" {
		t.Errorf("Branch = %q, %v; want feat_wt/login-crash", got, err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "login-crash")
	if got, err := Path(ctx, "login-crash"); err != nil || got != want {
		t.Errorf("Path = %q, %v; want %q", got, err, want)
	}
}

// A worktree on a branch whose type the repository does not declare — adopted
// from another layout, say — is still what `wt list` prints, so naming it
// exactly answers rather than refusing the type. The refusal stays for a spec
// that names nothing.
func TestPathAndBranchAnswerForAnExistingWorktreeOfAnUndeclaredType(t *testing.T) {
	ctx := locatable(t)
	foreign := worktreeAt(t, ctx.Repo.MainRoot, "wibble_wt/thing", filepath.Join(ctx.Repo.Parent, "demo-thing"))

	if got, err := Path(ctx, "wibble/thing"); err != nil || got != foreign {
		t.Errorf("Path = %q, %v; want %q", got, err, foreign)
	}
	if got, err := Branch(ctx, "thing"); err != nil || got != "wibble_wt/thing" {
		t.Errorf("Branch = %q, %v; want wibble_wt/thing", got, err)
	}
	if _, err := Path(ctx, "wibble/other"); err == nil || !strings.Contains(err.Error(), "unknown worktree type") {
		t.Errorf("a spec naming nothing keeps the type check, got: %v", err)
	}
}

// The main checkout is not a piece of work: its path falls through to the
// parse, which has nothing to say about it, as before. "/" and "." from
// inside it are the ways to ask for it (see TestPathAndBranchAnswerForRoot).
func TestPathDoesNotAnswerForTheMainCheckout(t *testing.T) {
	ctx := locatable(t)
	if got, err := Path(ctx, ctx.Repo.MainRoot); err == nil {
		t.Errorf("want an error for the main checkout's path, got %q", got)
	}
}

// Nothing at all is not a spec, and not "." either, whatever filepath.Clean
// makes of an empty string.
func TestPathAndBranchRefuseAnEmptySpec(t *testing.T) {
	ctx := locatable(t, "fix/login-crash")
	wt, err := Locate(ctx, "login-crash")
	if err != nil {
		t.Fatal(err)
	}
	ctx.Cwd = wt.Path

	for _, spec := range []string{"", "  ", "\t"} {
		if got, err := Path(ctx, spec); err == nil || !strings.Contains(err.Error(), "no work name given") {
			t.Errorf("Path(%q) = %q, %v; want no work name given", spec, got, err)
		}
		if got, err := Branch(ctx, spec); err == nil || !strings.Contains(err.Error(), "no work name given") {
			t.Errorf("Branch(%q) = %q, %v; want no work name given", spec, got, err)
		}
	}
}

// "/" is the main checkout by name: its path, and trunk as the repository
// configures it, from wherever the caller stands.
func TestPathAndBranchAnswerForRoot(t *testing.T) {
	ctx := locatable(t, "fix/login-crash")
	wt, err := Locate(ctx, "login-crash")
	if err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{ctx.Repo.MainRoot, wt.Path} {
		ctx.Cwd = cwd
		if got, err := Path(ctx, "/"); err != nil || got != ctx.Repo.MainRoot {
			t.Errorf("Path(/) from %s = %q, %v; want %q", cwd, got, err, ctx.Repo.MainRoot)
		}
		if got, err := Branch(ctx, "/"); err != nil || got != "main" {
			t.Errorf("Branch(/) from %s = %q, %v; want main", cwd, got, err)
		}
	}
	ctx.Config.MainBranch = "development"
	if got, err := Branch(ctx, "/"); err != nil || got != "development" {
		t.Errorf("Branch(/) = %q, %v; want the configured trunk", got, err)
	}
	ctx.Config.MainBranch = ""
	if _, err := Branch(ctx, "/"); err == nil || !strings.Contains(err.Error(), "MAIN_BRANCH") {
		t.Errorf("Branch(/) with no trunk known: want an error naming MAIN_BRANCH, got %v", err)
	}
}

// A detached worktree is found by its path but has no branch to print: an
// error, not an empty line that exits 0.
func TestBranchRefusesADetachedWorktree(t *testing.T) {
	ctx := locatable(t)
	detached := filepath.Join(ctx.Repo.Parent, "demo-detached")
	gitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "--detach", detached)

	got, err := Branch(ctx, detached)
	if err == nil || !strings.Contains(err.Error(), "has no branch") {
		t.Errorf("Branch = %q, %v; want a has-no-branch error", got, err)
	}
	if got, err := Path(ctx, detached); err != nil || got != detached {
		t.Errorf("Path = %q, %v; want %q", got, err, detached)
	}
}

// Branch, like Path, finds a worktree Superset made by the name it was given
// before reading a type out of that name.
func TestBranchStillFindsAWorktreeNamedTheWayItWasCreated(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	superset := filepath.Join(ctx.Repo.Parent, "demo_wt", "demo", "feat_wt", "fix_dev-123")
	gitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/fix_dev-123", superset)

	if got, err := Branch(ctx, "fix_dev-123"); err != nil || got != "feat_wt/fix_dev-123" {
		t.Errorf("Branch = %q, %v; want feat_wt/fix_dev-123", got, err)
	}
}

func TestPathForNonexistentWorkIsCanonical(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	ctx, _ := Open(main)
	got, err := Path(ctx, "fix/login-crash")
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
	main := fixtureRepo(t, minimalConf)
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

	ctx, _ := Open(main)
	got, err := Path(ctx, "fix/oldshape")
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Errorf("got %q, want the existing legacy path %q", got, legacy)
	}
}

// Superset's own shape must resolve too, since it is hardcoded in that tool.
func TestPathResolvesSupersetShape(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
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

	ctx, _ := Open(main)
	got, err := Path(ctx, "feat/arch")
	if err != nil {
		t.Fatal(err)
	}
	if got != superset {
		t.Errorf("got %q, want superset shape %q", got, superset)
	}
}
