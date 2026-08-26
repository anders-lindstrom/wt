package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddAndRemoveWorktree(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	dst := filepath.Join(r.Parent, "demo_wt", "fix_wt", "thing2")

	if err := r.AddWorktree(dst, "fix_wt/thing2", "main"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if got := r.BranchAt(dst); got != "fix_wt/thing2" {
		t.Errorf("BranchAt = %q", got)
	}
	if !r.BranchExists("fix_wt/thing2") {
		t.Error("branch should exist")
	}
	if err := r.RemoveWorktree(dst); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("worktree directory should be gone")
	}
}

func TestBranchAtDetachedIsEmpty(t *testing.T) {
	parent, main := fixture(t)
	r, _ := Discover(main)
	dst := filepath.Join(parent, "detached")
	run(t, main, "worktree", "add", "-q", "--detach", dst)
	if got := r.BranchAt(dst); got != "" {
		t.Errorf("BranchAt on detached = %q, want empty", got)
	}
}

func TestIsMerged(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	if !r.IsMerged("feat_wt/thing", "main") {
		t.Error("an unchanged branch should count as merged")
	}
	run(t, main, "commit", "-q", "--allow-empty", "-m", "second")
	if r.IsMerged("main", "feat_wt/thing") {
		t.Error("main has moved ahead; it is not merged into the branch")
	}
}

func TestRenameAndDeleteBranch(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	run(t, main, "branch", "fix_wt/temp")
	if err := r.RenameBranch("fix_wt/temp", "temp"); err != nil {
		t.Fatalf("RenameBranch: %v", err)
	}
	if r.BranchExists("fix_wt/temp") || !r.BranchExists("temp") {
		t.Error("rename did not take effect")
	}
	if err := r.DeleteBranch("temp"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if r.BranchExists("temp") {
		t.Error("branch should be deleted")
	}
}

func TestHasSubmodules(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	if r.HasSubmodules(main) {
		t.Error("fixture has no submodules")
	}
	if err := os.WriteFile(filepath.Join(main, ".gitmodules"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if !r.HasSubmodules(main) {
		t.Error("should detect .gitmodules")
	}
}

// git worktree move behaves like mv: given an existing destination directory it
// moves the worktree *inside* it, silently landing somewhere other than the
// caller asked for. Refuse instead — a wrong-path move that reports success is
// how a worktree carrying real work ends up somewhere nobody looks.
func TestMoveWorktreeRefusesExistingDestination(t *testing.T) {
	parent, main := fixture(t)
	r, _ := Discover(main)
	src := filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	dst := filepath.Join(parent, "occupied")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "other.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := r.MoveWorktree(src, dst)
	if err == nil {
		t.Fatal("want a refusal when the destination exists")
	}
	if _, statErr := os.Stat(filepath.Join(src, ".git")); statErr != nil {
		t.Error("the source worktree must be left where it was")
	}
	if _, statErr := os.Stat(filepath.Join(dst, "demo_wt")); statErr == nil {
		t.Error("nothing should have been moved into the destination")
	}
}

// An empty directory at the destination is harmless — MkdirAll may have made it.
func TestMoveWorktreeAllowsEmptyDestination(t *testing.T) {
	parent, main := fixture(t)
	r, _ := Discover(main)
	src := filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	dst := filepath.Join(parent, "empty-dest")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.MoveWorktree(src, dst); err != nil {
		t.Fatalf("an empty destination should be fine: %v", err)
	}
}
