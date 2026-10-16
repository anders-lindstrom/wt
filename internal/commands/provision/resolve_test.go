package provision

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// The WORK column of `wt list` names the worktree whatever its type. The
// default-type fallback used to send a bare name to feat_wt/ and print a path
// that did not exist while the worktree sat under chore_wt/.
func TestPathAndBranchPreferAnExistingWorktreeOfAnotherType(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")
	want, err := kit.Locate(ctx, "chore/wt-migration")
	if err != nil {
		t.Fatal(err)
	}

	for _, arg := range []string{"wt-migration", "chore/wt-migration", "chore_wt/wt-migration", want.Path} {
		if got, err := kit.Path(ctx, arg); err != nil || got != want.Path {
			t.Errorf("Path(%q) = %q, %v; want %q", arg, got, err, want.Path)
		}
		if got, err := kit.Branch(ctx, arg); err != nil || got != "chore_wt/wt-migration" {
			t.Errorf("Branch(%q) = %q, %v; want chore_wt/wt-migration", arg, got, err)
		}
	}
}

// One work name under two types is refused exactly as Locate refuses it,
// rather than resolved to whichever the default type implies.
func TestPathAndBranchRefuseAnAmbiguousWorkName(t *testing.T) {
	ctx := locatable(t, "feat/arch", "fix/arch")

	for name, resolve := range map[string]func(*kit.Context, string) (string, error){"Path": kit.Path, "Branch": kit.Branch} {
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
	if got, err := kit.Path(ctx, "fix/arch"); err != nil || !strings.HasSuffix(got, filepath.Join("fix_wt", "arch")) {
		t.Errorf("an exact type must still resolve: %q, %v", got, err)
	}
}

// With no worktree to find, a bare name still takes the default type, even
// while other worktrees exist.
func TestPathAndBranchFallBackToTheDefaultTypeWhenNothingExists(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")

	if got, err := kit.Branch(ctx, "login-crash"); err != nil || got != "feat_wt/login-crash" {
		t.Errorf("Branch = %q, %v; want feat_wt/login-crash", got, err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "login-crash")
	if got, err := kit.Path(ctx, "login-crash"); err != nil || got != want {
		t.Errorf("Path = %q, %v; want %q", got, err, want)
	}
}

// A worktree on a branch whose type the repository does not declare — adopted
// from another layout, say — is still what `wt list` prints, so naming it
// exactly answers rather than refusing the type. The refusal stays for a spec
// that names nothing.
func TestPathAndBranchAnswerForAnExistingWorktreeOfAnUndeclaredType(t *testing.T) {
	ctx := locatable(t)
	foreign := testkit.WorktreeAt(t, ctx.Repo.MainRoot, "wibble_wt/thing", filepath.Join(ctx.Repo.Parent, "demo-thing"))

	if got, err := kit.Path(ctx, "wibble/thing"); err != nil || got != foreign {
		t.Errorf("Path = %q, %v; want %q", got, err, foreign)
	}
	if got, err := kit.Branch(ctx, "thing"); err != nil || got != "wibble_wt/thing" {
		t.Errorf("Branch = %q, %v; want wibble_wt/thing", got, err)
	}
	if _, err := kit.Path(ctx, "wibble/other"); err == nil || !strings.Contains(err.Error(), "unknown worktree type") {
		t.Errorf("a spec naming nothing keeps the type check, got: %v", err)
	}
}

// The main checkout is not a piece of work: its path falls through to the
// parse, which has nothing to say about it, as before. "/" and "." from
// inside it are the ways to ask for it (see TestPathAndBranchAnswerForRoot).
func TestPathDoesNotAnswerForTheMainCheckout(t *testing.T) {
	ctx := locatable(t)
	if got, err := kit.Path(ctx, ctx.Repo.MainRoot); err == nil {
		t.Errorf("want an error for the main checkout's path, got %q", got)
	}
}

// Nothing at all is not a spec, and not "." either, whatever filepath.Clean
// makes of an empty string.
func TestPathAndBranchRefuseAnEmptySpec(t *testing.T) {
	ctx := locatable(t, "fix/login-crash")
	wt, err := kit.Locate(ctx, "login-crash")
	if err != nil {
		t.Fatal(err)
	}
	ctx.Cwd = wt.Path

	for _, spec := range []string{"", "  ", "\t"} {
		if got, err := kit.Path(ctx, spec); err == nil || !strings.Contains(err.Error(), "no work name given") {
			t.Errorf("Path(%q) = %q, %v; want no work name given", spec, got, err)
		}
		if got, err := kit.Branch(ctx, spec); err == nil || !strings.Contains(err.Error(), "no work name given") {
			t.Errorf("Branch(%q) = %q, %v; want no work name given", spec, got, err)
		}
	}
}

// "/" is the main checkout by name: its path, and trunk as the repository
// configures it, from wherever the caller stands.
func TestPathAndBranchAnswerForRoot(t *testing.T) {
	ctx := locatable(t, "fix/login-crash")
	wt, err := kit.Locate(ctx, "login-crash")
	if err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{ctx.Repo.MainRoot, wt.Path} {
		ctx.Cwd = cwd
		if got, err := kit.Path(ctx, "/"); err != nil || got != ctx.Repo.MainRoot {
			t.Errorf("Path(/) from %s = %q, %v; want %q", cwd, got, err, ctx.Repo.MainRoot)
		}
		if got, err := kit.Branch(ctx, "/"); err != nil || got != "main" {
			t.Errorf("Branch(/) from %s = %q, %v; want main", cwd, got, err)
		}
	}
	ctx.Config.MainBranch = "development"
	if got, err := kit.Branch(ctx, "/"); err != nil || got != "development" {
		t.Errorf("Branch(/) = %q, %v; want the configured trunk", got, err)
	}
	ctx.Config.MainBranch = ""
	if _, err := kit.Branch(ctx, "/"); err == nil || !strings.Contains(err.Error(), "MAIN_BRANCH") {
		t.Errorf("Branch(/) with no trunk known: want an error naming MAIN_BRANCH, got %v", err)
	}
}

// A detached worktree is found by its path but has no branch to print: an
// error, not an empty line that exits 0.
func TestBranchRefusesADetachedWorktree(t *testing.T) {
	ctx := locatable(t)
	detached := filepath.Join(ctx.Repo.Parent, "demo-detached")
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "--detach", detached)

	got, err := kit.Branch(ctx, detached)
	if err == nil || !strings.Contains(err.Error(), "has no branch") {
		t.Errorf("Branch = %q, %v; want a has-no-branch error", got, err)
	}
	if got, err := kit.Path(ctx, detached); err != nil || got != detached {
		t.Errorf("Path = %q, %v; want %q", got, err, detached)
	}
}
