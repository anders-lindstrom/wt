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
