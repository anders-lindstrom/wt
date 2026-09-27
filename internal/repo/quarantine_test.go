package repo

import (
	"path/filepath"
	"testing"
)

func TestAdminDirIsTheLinkedWorktreesGitDir(t *testing.T) {
	parent, main := fixture(t)
	r, _ := Discover(main)
	dst := filepath.Join(parent, "linked")
	run(t, main, "worktree", "add", "-q", "--detach", dst)
	admin, err := AdminDir(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !SamePath(filepath.Dir(admin), filepath.Join(main, ".git", "worktrees")) {
		t.Errorf("admin dir %s", admin)
	}
	if _, err := AdminDir(r.MainRoot); err == nil {
		t.Error("the main checkout has no admin dir of its own")
	}
}

func TestLockWorktreeLeavesTheReason(t *testing.T) {
	parent, main := fixture(t)
	r, _ := Discover(main)
	dst := filepath.Join(parent, "locked")
	run(t, main, "worktree", "add", "-q", "--detach", dst)
	if err := r.LockWorktree(dst, "wt quarantine /q"); err != nil {
		t.Fatal(err)
	}
	list, _ := r.Worktrees()
	if wt, _ := list.ByPath(dst); !wt.Locked || wt.LockReason != "wt quarantine /q" {
		t.Errorf("worktree %+v", wt)
	}
}

// The capture is the local file's section of exactly that branch, every
// value in order, and a name with regular expression characters in it
// matches only itself.
func TestBranchConfigIsExactlyThatSection(t *testing.T) {
	_, main := fixture(t)
	r, _ := Discover(main)
	run(t, main, "config", "branch.a.b.merge", "refs/heads/x")
	run(t, main, "config", "--add", "branch.a.b.note", "one")
	run(t, main, "config", "--add", "branch.a.b.note", "two")
	run(t, main, "config", "branch.aXb.merge", "refs/heads/other")
	run(t, main, "config", "branch.a.b.c.merge", "refs/heads/other")
	got, err := r.BranchConfig("a.b")
	if err != nil {
		t.Fatal(err)
	}
	want := []ConfigEntry{{"branch.a.b.merge", "refs/heads/x"}, {"branch.a.b.note", "one"}, {"branch.a.b.note", "two"}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	none, err := r.BranchConfig("nothing")
	if err != nil || len(none) != 0 {
		t.Errorf("a branch with no section: %v %v", none, err)
	}
}
