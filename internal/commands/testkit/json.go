package testkit

import "testing"

// RepoState is everything a read-only command must leave as it was: every
// ref, the loose objects, and each worktree's index.
func RepoState(t *testing.T, main string) string {
	t.Helper()
	refs := GitOut(t, main, "for-each-ref", "--format=%(refname) %(objectname)")
	objects := GitOut(t, main, "count-objects", "-v")
	return refs + "\n" + objects
}
