package repo

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// resolved mirrors what git reports: on macOS /var is a symlink to /private/var,
// so git canonicalises paths that t.TempDir() hands back unresolved.
func resolved(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// fixture builds a repo with one commit and one linked worktree.
func fixture(t *testing.T) (parent, main string) {
	t.Helper()
	parent = resolved(t, t.TempDir())
	main = filepath.Join(parent, "demo")
	run(t, parent, "init", "-q", "-b", "main", "demo")
	run(t, main, "config", "user.email", "t@example.com")
	run(t, main, "config", "user.name", "T")
	run(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, main, "worktree", "add", "-q", "-b", "feat_wt/thing",
		filepath.Join(parent, "demo_wt", "feat_wt", "thing"))
	return parent, main
}

func TestDiscoverFromMainCheckout(t *testing.T) {
	parent, main := fixture(t)
	r, err := Discover(main)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if r.Name != "demo" {
		t.Errorf("Name = %q, want demo", r.Name)
	}
	if r.Parent != parent {
		t.Errorf("Parent = %q, want %q", r.Parent, parent)
	}
}

// Discovery must give the same answer from inside a linked worktree, which is
// where agents and wt_cd usually leave you.
func TestDiscoverFromLinkedWorktree(t *testing.T) {
	parent, main := fixture(t)
	r, err := Discover(filepath.Join(parent, "demo_wt", "feat_wt", "thing"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if r.Name != "demo" || r.MainRoot != main {
		t.Errorf("got %+v, want name demo and MainRoot %q", r, main)
	}
}

func TestWorktreesListsMainFirstAndBranches(t *testing.T) {
	_, main := fixture(t)
	r, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	wts, err := r.Worktrees()
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("got %d worktrees, want 2", len(wts))
	}
	if !wts[0].IsMain || wts[0].Branch != "main" {
		t.Errorf("first = %+v, want main worktree on main", wts[0])
	}
	if wts[1].Branch != "feat_wt/thing" {
		t.Errorf("second branch = %q", wts[1].Branch)
	}
}

func TestDetectMainBranchSurvivesUnbornHead(t *testing.T) {
	parent := resolved(t, t.TempDir())
	run(t, parent, "init", "-q", "-b", "trunk", "fresh")
	r, err := Discover(filepath.Join(parent, "fresh"))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.DetectMainBranch(); got != "trunk" {
		t.Errorf("got %q, want trunk", got)
	}
}

func TestDiscoverOutsideRepo(t *testing.T) {
	if _, err := Discover(t.TempDir()); err == nil {
		t.Error("want error outside a repository")
	}
}
