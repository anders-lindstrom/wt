package testkit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// FixtureRepo is a fresh repository at demo/ with conf as its .wt.conf.
func FixtureRepo(t *testing.T, conf string) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(parent, "demo")
	gittest.Git(t, parent, "init", "-q", "-b", "main", "demo")
	dir := filepath.Join(main, "bin", "worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worktree.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return main
}

// MinimalConf is the smallest valid .wt.conf.
const MinimalConf = "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\n"
