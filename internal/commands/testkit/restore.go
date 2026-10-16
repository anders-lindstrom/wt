package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// AssertBack checks the worktree is registered at path, unlocked, clean,
// with the quarantine empty of it; head is what its HEAD must name.
func AssertBack(t *testing.T, ctx *kit.Context, path, dir, head string) {
	t.Helper()
	list, err := ctx.Repo.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	wt, ok := list.ByPath(path)
	if !ok {
		t.Fatalf("the worktree must be registered again: %+v", list)
	}
	if wt.Locked {
		t.Errorf("the worktree must be unlocked: %q", wt.LockReason)
	}
	if Exists(filepath.Join(dir, "checkout")) || Exists(filepath.Join(dir, "admin")) {
		t.Error("nothing of it may be left in the quarantine")
	}
	if got := strings.TrimSpace(GitOut(t, path, "status", "--porcelain")); got != "" {
		t.Errorf("the worktree must be clean: %q", got)
	}
	got, _ := os.ReadFile(filepath.Join(GitDirFor(t, path), "HEAD"))
	if strings.TrimSpace(string(got)) != head {
		t.Errorf("HEAD is %q, want %q", strings.TrimSpace(string(got)), head)
	}
}
