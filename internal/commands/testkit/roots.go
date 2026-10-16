package testkit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// ManagedRepo is a repository at parent/name with the configuration wt reads.
func ManagedRepo(t *testing.T, parent, name string) string {
	t.Helper()
	dir := gittest.NewRepo(t, parent, name)
	conf := filepath.Join(dir, "bin", "worktree")
	if err := os.MkdirAll(conf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conf, "worktree.conf"), []byte(MinimalConf), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// RealTempDir is t.TempDir with its symlinks resolved.
func RealTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
