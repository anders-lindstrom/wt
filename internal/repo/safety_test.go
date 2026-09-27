package repo

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// linked adds a worktree on a new branch and returns its path.
func linked(t *testing.T, main, branch string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(main), "wt-"+filepath.Base(branch))
	run(t, main, "worktree", "add", "-q", "-b", branch, path)
	return path
}

// withSubmodule gives main a committed submodule at sm, itself holding a
// submodule at n2, and returns a linked worktree with both initialised.
func withSubmodule(t *testing.T, parent, main string) string {
	t.Helper()
	inner2 := gittest.NewRepo(t, parent, "inner2")
	inner := gittest.NewRepo(t, parent, "inner")
	run(t, inner, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner2, "n2")
	run(t, inner, "commit", "-qm", "n2")
	run(t, main, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "sm")
	run(t, main, "commit", "-qm", "sm")
	wt := linked(t, main, "feat_wt/subs")
	run(t, wt, "-c", "protocol.file.allow=always", "submodule", "update", "-q", "--init", "--recursive")
	return wt
}

func strict(t *testing.T, path string) StrictStatus {
	t.Helper()
	s, err := DirtyStrict(path)
	if err != nil {
		t.Fatalf("DirtyStrict: %v", err)
	}
	return s
}

func TestDirtyStrictCleanCheckout(t *testing.T) {
	_, main := fixture(t)
	if s := strict(t, main); s.Dirty || len(s.Hidden) > 0 {
		t.Errorf("a fresh checkout reads %+v, want clean", s)
	}
}

func TestDirtyStrictSeesUntrackedFilesWhateverTheConfigSays(t *testing.T) {
	_, main := fixture(t)
	run(t, main, "config", "status.showUntrackedFiles", "no")
	gittest.WriteFile(t, filepath.Join(main, "notes.txt"), "mine")
	if !strict(t, main).Dirty {
		t.Error("an untracked file is uncommitted work, whatever status.showUntrackedFiles says")
	}
}

// A status that cannot be read is not a clean status.
func TestDirtyStrictFailsOnAnUnreadableStatus(t *testing.T) {
	_, main := fixture(t)
	if err := os.WriteFile(filepath.Join(main, ".git", "index"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DirtyStrict(main); err == nil {
		t.Error("want an error for a corrupt index, not an answer")
	}
}

func TestDirtyStrictSeesAFileInANestedSubmodule(t *testing.T) {
	parent, main := fixture(t)
	wt := withSubmodule(t, parent, main)
	if strict(t, wt).Dirty {
		t.Fatal("freshly initialised submodules read as dirty")
	}
	gittest.WriteFile(t, filepath.Join(wt, "sm", "n2", "scratch"), "x")
	if !strict(t, wt).Dirty {
		t.Error("an untracked file two submodules down is uncommitted work")
	}
}

func TestDirtyStrictSeesAModifiedSubmoduleItsConfigIgnores(t *testing.T) {
	parent, main := fixture(t)
	wt := withSubmodule(t, parent, main)
	run(t, wt, "config", "submodule.sm.ignore", "all")
	run(t, wt, "config", "diff.ignoreSubmodules", "all")
	gittest.WriteFile(t, filepath.Join(wt, "sm", "scratch"), "x")
	if !strict(t, wt).Dirty {
		t.Error("submodule.<name>.ignore=all must not hide a submodule's changes")
	}
}

// A submodule whose own config hides its nested submodule is still read
// through: each initialised submodule is read with the same flags.
func TestDirtyStrictReadsIntoEachSubmodule(t *testing.T) {
	parent, main := fixture(t)
	wt := withSubmodule(t, parent, main)
	run(t, filepath.Join(wt, "sm"), "config", "submodule.n2.ignore", "all")
	run(t, filepath.Join(wt, "sm"), "config", "status.showUntrackedFiles", "no")
	gittest.WriteFile(t, filepath.Join(wt, "sm", "n2", "scratch"), "x")
	if !strict(t, wt).Dirty {
		t.Error("a nested submodule's changes must be seen whatever its parent's config says")
	}
}

func TestDirtyStrictNamesFilesStatusIsToldNotToLookAt(t *testing.T) {
	_, main := fixture(t)
	gittest.WriteFile(t, filepath.Join(main, "a.txt"), "a")
	gittest.WriteFile(t, filepath.Join(main, "b.txt"), "b")
	run(t, main, "add", "a.txt", "b.txt")
	run(t, main, "commit", "-qm", "files")
	run(t, main, "update-index", "--assume-unchanged", "a.txt")
	run(t, main, "update-index", "--skip-worktree", "b.txt")
	gittest.WriteFile(t, filepath.Join(main, "a.txt"), "edited")

	s := strict(t, main)
	if !s.Dirty {
		t.Errorf("a.txt was edited behind status's back, which is still an edit: %+v", s)
	}
	if !slices.Equal(s.Hidden, []string{"b.txt"}) {
		t.Errorf("Hidden = %q, want only the unedited b.txt", s.Hidden)
	}
}

// A hidden file whose content is still what the index has loses nothing.
func TestDirtyStrictNamesUneditedHiddenFilesWithoutCallingThemDirty(t *testing.T) {
	_, main := fixture(t)
	gittest.WriteFile(t, filepath.Join(main, "a.txt"), "a")
	run(t, main, "add", "a.txt")
	run(t, main, "commit", "-qm", "a")
	run(t, main, "update-index", "--assume-unchanged", "a.txt")
	s := strict(t, main)
	if s.Dirty || !slices.Equal(s.Hidden, []string{"a.txt"}) {
		t.Errorf("got %+v, want clean with a.txt hidden", s)
	}
}

// A submodule checked out at another commit than the one recorded is named
// for what it is, not as uncommitted changes.
func TestDirtyStrictNamesASubmoduleAtAnotherCommit(t *testing.T) {
	parent, main := fixture(t)
	wt := withSubmodule(t, parent, main)
	run(t, filepath.Join(wt, "sm"), "checkout", "-q", "--detach")
	run(t, filepath.Join(wt, "sm"), "commit", "-q", "--allow-empty", "-m", "moved on")
	s := strict(t, wt)
	if s.Dirty || !slices.Equal(s.Moved, []string{"sm"}) {
		t.Errorf("got %+v, want sm moved and nothing else", s)
	}
}

// A sparse checkout marks every file outside its cone skip-worktree and
// leaves it off disk: nothing there to lose.
func TestDirtyStrictIgnoresSkipWorktreeFilesThatAreNotThere(t *testing.T) {
	_, main := fixture(t)
	gittest.WriteFile(t, filepath.Join(main, "b.txt"), "b")
	run(t, main, "add", "b.txt")
	run(t, main, "commit", "-qm", "b")
	run(t, main, "update-index", "--skip-worktree", "b.txt")
	if err := os.Remove(filepath.Join(main, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if s := strict(t, main); len(s.Hidden) > 0 {
		t.Errorf("Hidden = %q, want none", s.Hidden)
	}
}

func TestOperationInProgress(t *testing.T) {
	_, main := fixture(t)
	for _, tc := range []struct{ file, want string }{
		{"rebase-merge/head-name", "rebase"},
		{"rebase-apply/head-name", "rebase"},
		{"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
		{"BISECT_LOG", "bisect"},
		{"BISECT_START", "bisect"},
		{"sequencer/todo", "cherry-pick or revert"},
	} {
		t.Run(tc.want+" "+tc.file, func(t *testing.T) {
			wt := linked(t, main, "feat_wt/op-"+filepath.Base(filepath.Dir(tc.file))+filepath.Base(tc.file))
			gitDir, err := gitDirOf(wt)
			if err != nil {
				t.Fatal(err)
			}
			if op, err := OperationInProgress(wt); err != nil || op != "" {
				t.Fatalf("before: %q, %v; want none", op, err)
			}
			gittest.WriteFile(t, filepath.Join(gitDir, tc.file), "x\n")
			if op, err := OperationInProgress(wt); err != nil || op != tc.want {
				t.Errorf("got %q, %v; want %q", op, err, tc.want)
			}
		})
	}
}

// Each operation for real, as git leaves it, not just the file wt looks for.
func TestOperationInProgressAsGitLeavesIt(t *testing.T) {
	_, main := fixture(t)
	conflict := func(t *testing.T, wt string) string {
		gittest.WriteFile(t, filepath.Join(wt, "f"), "base\n")
		run(t, wt, "add", "f")
		run(t, wt, "commit", "-qm", "base")
		base := gittest.Git(t, wt, "rev-parse", "HEAD")
		gittest.WriteFile(t, filepath.Join(wt, "f"), "theirs\n")
		run(t, wt, "commit", "-qam", "theirs")
		theirs := gittest.Git(t, wt, "rev-parse", "HEAD")
		run(t, wt, "reset", "-q", "--hard", base)
		gittest.WriteFile(t, filepath.Join(wt, "f"), "ours\n")
		run(t, wt, "commit", "-qam", "ours")
		return theirs
	}
	for _, tc := range []struct {
		want string
		args func(theirs string) []string
	}{
		{"merge", func(c string) []string { return []string{"merge", c} }},
		{"cherry-pick", func(c string) []string { return []string{"cherry-pick", c} }},
		{"rebase", func(c string) []string { return []string{"rebase", c} }},
	} {
		t.Run(tc.want, func(t *testing.T) {
			wt := linked(t, main, "feat_wt/real-"+tc.want)
			theirs := conflict(t, wt)
			if _, err := gittest.Try(t, wt, tc.args(theirs)...); err == nil {
				t.Fatal("want the operation to stop on its conflict")
			}
			if op, err := OperationInProgress(wt); err != nil || op != tc.want {
				t.Errorf("got %q, %v; want %q", op, err, tc.want)
			}
		})
	}
}

// A worktree whose git dir cannot be found is not one with nothing going on.
func TestOperationInProgressFailsWithoutAGitDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := OperationInProgress(dir); err == nil {
		t.Error("want an error for a directory with no .git")
	}
}

func TestSurvivorsLeaveOutTheDroppedRefAndTheLeavingWorktree(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	a := linked(t, main, "feat_wt/a")
	run(t, a, "commit", "-q", "--allow-empty", "-m", "a")
	aTip := gittest.Git(t, a, "rev-parse", "HEAD")
	b := linked(t, main, "feat_wt/b")
	run(t, b, "checkout", "-q", "--detach")
	run(t, b, "commit", "-q", "--allow-empty", "-m", "b detached")
	bHead := gittest.Git(t, b, "rev-parse", "HEAD")

	keep, err := r.Survivors([]string{"refs/heads/feat_wt/a"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.CountLost(aTip, keep); err != nil || n != 1 {
		t.Errorf("a's commit: %d, %v; want 1 lost", n, err)
	}
	if n, err := r.CountLost(bHead, keep); err != nil || n != 0 {
		t.Errorf("b's detached HEAD survives as b's HEAD: %d, %v; want 0", n, err)
	}

	keep, err = r.Survivors(nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.CountLost(bHead, keep); err != nil || n != 1 {
		t.Errorf("b's detached commit once b goes: %d, %v; want 1", n, err)
	}
}

func TestCountLostFailsOnAnUnknownCommit(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	if _, err := r.CountLost("0123456789012345678901234567890123456789", nil); err == nil {
		t.Error("want an error for a commit that is not there")
	}
}

// A submodule worktree goes by its path alone: git worktree remove --force,
// never a repository-wide prune that would take other registrations with it.
func TestRemoveWorktreeWithSubmodulesPrunesNothingElse(t *testing.T) {
	parent, main := fixture(t)
	wt := withSubmodule(t, parent, main)
	r, _ := Discover(main)
	gone := linked(t, main, "feat_wt/gone")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	if err := r.RemoveWorktree(wt); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("the checkout should be gone")
	}
	list, err := r.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := list.ByPath(wt); ok {
		t.Error("the removed worktree is still registered")
	}
	if _, ok := list.ByPath(gone); !ok {
		t.Error("another worktree's registration was pruned with it")
	}
}

// A filesystem monitor that says nothing changed is not believed: an edit
// it failed to report is still an edit.
func TestDirtyStrictDoesNotTrustAFilesystemMonitor(t *testing.T) {
	_, main := fixture(t)
	gittest.WriteFile(t, filepath.Join(main, "a.txt"), "a")
	run(t, main, "add", "a.txt")
	run(t, main, "commit", "-qm", "a")
	hook := filepath.Join(t.TempDir(), "fsmonitor")
	// Protocol 2: answer with a token and no paths, "nothing changed".
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nprintf 'tok\\0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, main, "config", "core.fsmonitor", hook)
	run(t, main, "status", "--porcelain")
	gittest.WriteFile(t, filepath.Join(main, "a.txt"), "edited, and the monitor never says so")
	if out := gittest.Git(t, main, "status", "--porcelain"); out != "" {
		t.Skipf("this git does not trust the fake monitor, nothing to prove: %q", out)
	}
	if !strict(t, main).Dirty {
		t.Error("an edit a filesystem monitor did not report is still an edit")
	}
}

// git worktree add --relative-paths writes a gitdir relative to the checkout,
// not to wherever wt happens to run.
func TestOperationInProgressReadsARelativeGitDir(t *testing.T) {
	_, main := fixture(t)
	wt := filepath.Join(filepath.Dir(main), "wt-relative")
	run(t, main, "worktree", "add", "-q", "--relative-paths", "-b", "feat_wt/relative", wt)
	if op, err := OperationInProgress(wt); err != nil || op != "" {
		t.Errorf("got %q, %v; want none", op, err)
	}
}

// An unborn worktree has no HEAD to hold anything, and must not stop the
// count for every other one.
func TestSurvivorsSkipAnUnbornWorktree(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	run(t, main, "worktree", "add", "-q", "--orphan", "-b", "feat_wt/unborn", filepath.Join(filepath.Dir(main), "wt-unborn"))
	keep, err := r.Survivors(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	head := gittest.Git(t, main, "rev-parse", "HEAD")
	if _, err := r.CountLost(head, keep); err != nil {
		t.Errorf("CountLost with an unborn worktree about: %v", err)
	}
}

// A path with a newline in it is still the path of the worktree leaving, so
// its HEAD is not counted as surviving.
func TestSurvivorsLeaveOutAWorktreeWhosePathHasANewline(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	odd := filepath.Join(filepath.Dir(main), "odd\nname")
	run(t, main, "worktree", "add", "-q", "--detach", odd)
	run(t, odd, "commit", "-q", "--allow-empty", "-m", "only here")
	head := gittest.Git(t, odd, "rev-parse", "HEAD")
	keep, err := r.Survivors(nil, odd)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.CountLost(head, keep); err != nil || n != 1 {
		t.Errorf("got %d, %v; want its 1 commit lost", n, err)
	}
}

func TestHeadOf(t *testing.T) {
	_, main := fixture(t)
	if h, err := HeadOf(main); err != nil || h == "" {
		t.Errorf("a checkout with a commit: %q, %v", h, err)
	}
	unborn := filepath.Join(filepath.Dir(main), "wt-unborn-head")
	run(t, main, "worktree", "add", "-q", "--orphan", "-b", "feat_wt/unborn-head", unborn)
	if h, err := HeadOf(unborn); err != nil || h != "" {
		t.Errorf("an unborn branch: %q, %v; want no commit and no error", h, err)
	}
	broken := linked(t, main, "feat_wt/broken-head")
	gitDir, _ := gitDirOf(broken)
	gittest.WriteFile(t, filepath.Join(gitDir, "HEAD"), "0123456789012345678901234567890123456789\n")
	if h, err := HeadOf(broken); err == nil {
		t.Errorf("a HEAD naming a missing commit: %q, want an error", h)
	}
}

// A commit made inside a submodule of a linked worktree lives only in that
// worktree's modules/ directory, which the removal deletes.
func TestSubmoduleLossesFindsACommitOnlyTheWorktreeHas(t *testing.T) {
	parent, main := fixture(t)
	wt := withSubmodule(t, parent, main)
	if lost, err := SubmoduleLosses(wt); err != nil || len(lost) > 0 {
		t.Fatalf("freshly initialised: %+v, %v; want none", lost, err)
	}
	sm := filepath.Join(wt, "sm")
	run(t, sm, "checkout", "-q", "--detach")
	run(t, sm, "commit", "-q", "--allow-empty", "-m", "only in this worktree")
	run(t, wt, "add", "sm")
	run(t, wt, "commit", "-qm", "bump sm")

	lost, err := SubmoduleLosses(wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 1 || lost[0].Path != "sm" || lost[0].Count != 1 {
		t.Errorf("got %+v, want sm's one commit", lost)
	}
}

// The same commit fetched into the main checkout's copy of the submodule
// survives the removal.
func TestSubmoduleLossesCountsTheMainCheckoutsCopy(t *testing.T) {
	parent, main := fixture(t)
	wt := withSubmodule(t, parent, main)
	sm := filepath.Join(wt, "sm")
	run(t, sm, "checkout", "-q", "-b", "shared")
	run(t, sm, "commit", "-q", "--allow-empty", "-m", "fetched into main too")
	run(t, filepath.Join(main, "sm"), "fetch", "-q", sm, "shared:shared")

	if lost, err := SubmoduleLosses(wt); err != nil || len(lost) > 0 {
		t.Errorf("got %+v, %v; want none", lost, err)
	}
}

// Work on a nested submodule's own branch is lost the same way.
func TestSubmoduleLossesLooksIntoNestedSubmodules(t *testing.T) {
	parent, main := fixture(t)
	wt := withSubmodule(t, parent, main)
	n2 := filepath.Join(wt, "sm", "n2")
	run(t, n2, "checkout", "-q", "-b", "local-work")
	run(t, n2, "commit", "-q", "--allow-empty", "-m", "nested")

	lost, err := SubmoduleLosses(wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 1 || lost[0].Path != "sm/n2" {
		t.Errorf("got %+v, want sm/n2", lost)
	}
}
