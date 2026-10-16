package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

// UnconfiguredRepo is a repository with a commit on main and no worktree
// configuration: what a clone of someone else's project is.
func UnconfiguredRepo(t *testing.T) string {
	t.Helper()
	main := CommittedRepo(t, MinimalConf)
	if err := os.RemoveAll(filepath.Join(main, "bin")); err != nil {
		t.Fatal(err)
	}
	return main
}
