package repo

import (
	"os"
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

// runIgnoringFailure runs git and discards a non-zero exit, for commands like
// `git rebase` that are expected to stop with a conflict.
func runIgnoringFailure(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	_, _ = cmd.CombinedOutput()
}

func TestWorktreesNameTheBranchOfAStoppedRebase(t *testing.T) {
	// main + a linked worktree on feature, with a conflict between them.
	parent := resolved(t, t.TempDir())
	main := filepath.Join(parent, "demo")
	run(t, parent, "init", "-q", "-b", "main", "demo")
	run(t, main, "config", "user.email", "t@example.com")
	run(t, main, "config", "user.name", "T")
	run(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	writeFile(t, filepath.Join(main, "v.txt"), "1\n")
	run(t, main, "add", "v.txt")
	run(t, main, "commit", "-q", "-m", "add v.txt")

	wtPath := filepath.Join(parent, "demo_wt", "feature")
	run(t, main, "worktree", "add", "-q", "-b", "feature", wtPath)

	writeFile(t, filepath.Join(main, "v.txt"), "2\n")
	run(t, main, "commit", "-q", "-am", "trunk")

	writeFile(t, filepath.Join(wtPath, "v.txt"), "3\n")
	run(t, wtPath, "commit", "-q", "-am", "branch")

	// Start the rebase in the worktree and let it stop.
	runIgnoringFailure(t, wtPath, "rebase", "main")

	r, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	list, err := r.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	var wt Worktree
	for _, w := range list {
		if !w.IsMain {
			wt = w
		}
	}
	if wt.Branch != "feature" {
		t.Fatalf("Branch = %q, want feature: git says detached during a rebase", wt.Branch)
	}
	if wt.Detached {
		t.Fatal("Detached = true: a worktree whose branch we can name is not detached")
	}
	if !wt.Rebasing {
		t.Fatal("Rebasing = false, want true")
	}
}

func TestWorktreesLeaveAGenuinelyDetachedWorktreeAlone(t *testing.T) {
	// A worktree checked out at a bare SHA, no rebase.
	parent, main := fixture(t)
	r, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(parent, "detached")
	run(t, main, "worktree", "add", "-q", "--detach", dst)

	list, err := r.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	var wt Worktree
	found := false
	for _, w := range list {
		if w.Path == resolved(t, dst) {
			wt, found = w, true
		}
	}
	if !found {
		t.Fatal("detached worktree not found in list")
	}
	if wt.Branch != "" {
		t.Fatalf("Branch = %q, want empty for a genuinely detached worktree", wt.Branch)
	}
	if !wt.Detached {
		t.Fatal("Detached = false, want true")
	}
	if wt.Rebasing {
		t.Fatal("Rebasing = true, want false: no rebase is in progress")
	}
}

// A rebase started from a HEAD that was already detached (not on any
// branch) records the literal "detached HEAD" in head-name, not a ref: there
// is no branch to recover, and the worktree must be left genuinely detached.
func TestWorktreesIgnoreADetachedHeadRebase(t *testing.T) {
	parent := resolved(t, t.TempDir())
	main := filepath.Join(parent, "demo")
	run(t, parent, "init", "-q", "-b", "main", "demo")
	run(t, main, "config", "user.email", "t@example.com")
	run(t, main, "config", "user.name", "T")
	run(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	writeFile(t, filepath.Join(main, "v.txt"), "1\n")
	run(t, main, "add", "v.txt")
	run(t, main, "commit", "-q", "-m", "add v.txt")

	// Worktree checked out detached at the current tip of main.
	wtPath := filepath.Join(parent, "demo_wt", "detached")
	run(t, main, "worktree", "add", "-q", "--detach", wtPath)

	// Trunk moves on.
	writeFile(t, filepath.Join(main, "v.txt"), "2\n")
	run(t, main, "commit", "-q", "-am", "trunk")

	// The detached worktree also moves on, so it diverges from trunk.
	writeFile(t, filepath.Join(wtPath, "v.txt"), "3\n")
	run(t, wtPath, "commit", "-q", "-am", "branch")

	// Rebase onto trunk from the already-detached HEAD; let it stop on the
	// conflict.
	runIgnoringFailure(t, wtPath, "rebase", "main")

	r, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	list, err := r.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	var wt Worktree
	found := false
	for _, w := range list {
		if w.Path == resolved(t, wtPath) {
			wt, found = w, true
		}
	}
	if !found {
		t.Fatal("detached worktree not found in list")
	}
	if wt.Branch != "" {
		t.Fatalf("Branch = %q, want empty: rebase from a detached HEAD names no branch", wt.Branch)
	}
	if !wt.Detached {
		t.Fatal("Detached = false, want true")
	}
	if wt.Rebasing {
		t.Fatal("Rebasing = true, want false: there is no branch to resume")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverOutsideRepo(t *testing.T) {
	if _, err := Discover(t.TempDir()); err == nil {
		t.Error("want error outside a repository")
	}
}

// The original bash resolved worktree.conf against `git rev-parse --show-toplevel`
// — the current worktree — so a branch that changes the configuration was
// honoured where it was checked out. Root must therefore be the worktree you
// are standing in, not the main checkout.
func TestDiscoverRecordsCurrentWorktreeRoot(t *testing.T) {
	parent, main := fixture(t)
	linked := filepath.Join(parent, "demo_wt", "feat_wt", "thing")

	r, err := Discover(linked)
	if err != nil {
		t.Fatal(err)
	}
	if r.Root != linked {
		t.Errorf("Root = %q, want the current worktree %q", r.Root, linked)
	}
	if r.MainRoot != main {
		t.Errorf("MainRoot = %q, want %q", r.MainRoot, main)
	}

	rm, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	if rm.Root != main || rm.MainRoot != main {
		t.Errorf("from the main checkout both should be %q, got %q / %q", main, rm.Root, rm.MainRoot)
	}
}

// "not merged" says nothing about what is at stake; the count does.
func TestCommitsAhead(t *testing.T) {
	parent, main := fixture(t)
	r, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	thing := filepath.Join(parent, "demo_wt", "feat_wt", "thing")

	if n, ok := r.CommitsAhead("feat_wt/thing", "main"); !ok || n != 0 {
		t.Errorf("a branch level with main is 0 ahead, got %d (ok=%v)", n, ok)
	}
	run(t, thing, "commit", "-q", "--allow-empty", "-m", "one")
	run(t, thing, "commit", "-q", "--allow-empty", "-m", "two")
	if n, ok := r.CommitsAhead("feat_wt/thing", "main"); !ok || n != 2 {
		t.Errorf("got %d ahead (ok=%v), want 2", n, ok)
	}
	// A ref that is not there is unknown, which is not the same as zero.
	if _, ok := r.CommitsAhead("no-such-branch", "main"); ok {
		t.Error("an unreadable ref must report not-ok, not a count")
	}
}
