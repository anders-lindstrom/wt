package repo

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The delete is update-ref's, which unlike `git branch -D` will take a branch
// out from under a checkout, so DeleteBranchAt asks the worktrees first.
func TestDeleteBranchAtRefusesABranchAWorktreeHasCheckedOut(t *testing.T) {
	r, dir := sweepFixture(t)
	wt := filepath.Join(t.TempDir(), "in-use")
	sweepGit(t, dir, "worktree", "add", "-q", "-b", "busy", wt)

	err := r.DeleteBranchAt("busy", sweepGit(t, dir, "rev-parse", "busy"))
	var inUse *BranchInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("DeleteBranchAt = %v, want a BranchInUseError", err)
	}
	if !SamePath(inUse.Path, wt) || inUse.By != "" {
		t.Errorf("got %+v, want the checkout at %s held by nothing", inUse, wt)
	}
	if !r.BranchExists("busy") {
		t.Error("the refused delete removed the branch")
	}
}

// A bisect holds the branch it started from even though no worktree has it
// checked out, and git refuses to delete it: the refusal names the operation.
func TestDeleteBranchAtRefusesABranchABisectHolds(t *testing.T) {
	r, dir := sweepFixture(t)
	wt := filepath.Join(t.TempDir(), "bisecting")
	sweepGit(t, dir, "worktree", "add", "-q", "-b", "elsewhere", wt)
	sweepGit(t, dir, "branch", "started-here")
	gitDir := sweepGit(t, wt, "rev-parse", "--absolute-git-dir")
	if err := os.WriteFile(filepath.Join(gitDir, "BISECT_START"),
		[]byte("refs/heads/started-here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := r.DeleteBranchAt("started-here", sweepGit(t, dir, "rev-parse", "started-here"))
	var inUse *BranchInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("DeleteBranchAt = %v, want a BranchInUseError", err)
	}
	if inUse.By != "bisect" || !SamePath(inUse.Path, wt) {
		t.Errorf("got %+v, want the bisect in %s", inUse, wt)
	}
	if !r.BranchExists("started-here") {
		t.Error("the refused delete removed the branch")
	}
}

// A branch nothing is using is deleted as before.
func TestDeleteBranchAtDeletesABranchNobodyIsUsing(t *testing.T) {
	r, dir := sweepFixture(t)
	sweepGit(t, dir, "branch", "spare")
	if err := r.DeleteBranchAt("spare", sweepGit(t, dir, "rev-parse", "spare")); err != nil {
		t.Fatalf("DeleteBranchAt: %v", err)
	}
	if r.BranchExists("spare") {
		t.Error("the branch should be gone")
	}
}
