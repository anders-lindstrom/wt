package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitDirFor is the git dir a worktree's .git file points at.
func GitDirFor(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
}
