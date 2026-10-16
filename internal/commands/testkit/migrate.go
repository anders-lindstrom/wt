package testkit

import "testing"

// WorktreeAt adds a worktree on a new branch at path.
func WorktreeAt(t *testing.T, main, branch, path string) string {
	t.Helper()
	GitIn(t, main, "worktree", "add", "-q", "-b", branch, path)
	return path
}
