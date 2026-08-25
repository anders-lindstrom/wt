package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 5. A merged branch is deleted.
func TestRemoveDeletesMergedBranch(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if _, err := New(ctx, "fix/merged", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, "fix/merged", &buf); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ctx.Repo.BranchExists("fix_wt/merged") {
		t.Error("merged branch should have been deleted")
	}
}

// 6. An unmerged branch is renamed to strip the prefix, never deleted.
func TestRemoveRenamesUnmergedBranch(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/unmerged", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")

	if err := Remove(ctx, "fix/unmerged", &buf); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ctx.Repo.BranchExists("fix_wt/unmerged") {
		t.Error("prefixed branch should be gone")
	}
	if !ctx.Repo.BranchExists("unmerged") {
		t.Error("unmerged work must be kept under the stripped name")
	}
}

// 2. A detached worktree has no branch to clean up; touch none.
func TestRemoveDetachedTouchesNoBranch(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	dst := filepath.Join(ctx.Repo.Parent, "detached")
	gitIn(t, main, "worktree", "add", "-q", "--detach", dst)

	var buf bytes.Buffer
	if err := RemoveAt(ctx, dst, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("worktree should be gone")
	}
	if !strings.Contains(buf.String(), "no branch") {
		t.Errorf("want a note about having no branch:\n%s", buf.String())
	}
}

// 3. A branch this tooling does not own is never deleted or renamed.
func TestRemoveLeavesForeignBranchAlone(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	gitIn(t, main, "branch", "someones-work")
	dst := filepath.Join(ctx.Repo.Parent, "foreign")
	gitIn(t, main, "worktree", "add", "-q", dst, "someones-work")

	var buf bytes.Buffer
	if err := RemoveAt(ctx, dst, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if !ctx.Repo.BranchExists("someones-work") {
		t.Fatal("a branch this tooling did not create must survive removal")
	}
}

// 1. The branch is read from the worktree, not rebuilt from the work name.
func TestRemoveReadsBranchFromWorktree(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/renamed", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	// Rename the branch by hand: name and branch now disagree.
	gitIn(t, path, "branch", "-m", "fix_wt/renamed", "spike_wt/actually")

	if err := RemoveAt(ctx, path, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if ctx.Repo.BranchExists("spike_wt/actually") {
		t.Error("the branch actually checked out should have been handled")
	}
	if ctx.Repo.BranchExists("fix_wt/renamed") {
		t.Error("the rebuilt-from-name branch must never be resurrected")
	}
}

// 4. A worktree with submodules cannot go through `git worktree remove`.
func TestRemoveHandlesSubmodulesWorktree(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/subs", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(path, ".gitmodules"), "")

	if err := RemoveAt(ctx, path, &buf); err != nil {
		t.Fatalf("RemoveAt with submodules: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("worktree should be gone")
	}
}

func TestRemoveUnknownWorkIsAnError(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if err := Remove(ctx, "fix/nope", &buf); err == nil {
		t.Error("want an error removing work that does not exist")
	}
}
