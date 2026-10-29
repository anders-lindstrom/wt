package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// SweepRepoWith is a main checkout on main with a bare origin beside it,
// fetched, origin/HEAD pointing at main, opened after all of that.
func SweepRepoWith(t *testing.T, conf string) (ctx *kit.Context, main, origin string) {
	t.Helper()
	main = CommittedRepo(t, conf)
	origin = filepath.Join(t.TempDir(), "origin.git")
	GitIn(t, main, "clone", "-q", "--bare", main, origin)
	GitIn(t, main, "remote", "add", "origin", origin)
	GitIn(t, main, "fetch", "-q", "origin")
	GitIn(t, main, "remote", "set-head", "origin", "main")
	ctx, err := kit.Open(main, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, main, origin
}

// SweepRepo is SweepRepoWith the minimal configuration.
func SweepRepo(t *testing.T) (ctx *kit.Context, main, origin string) {
	t.Helper()
	return SweepRepoWith(t, MinimalConf)
}

// AddWork puts n empty commits on branch through update-ref, so no checkout
// moves. Messages are unique: two empty commits with the same parent, message
// and second would otherwise be the same commit.
func AddWork(t *testing.T, main, branch string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		tree := GitOut(t, main, "rev-parse", branch+"^{tree}")
		c := GitOut(t, main, "commit-tree", "-p", branch, "-m", fmt.Sprintf("%s work %d", branch, i), tree)
		GitIn(t, main, "update-ref", "refs/heads/"+branch, c)
	}
}

// BranchWithWork cuts branch from main and gives it n commits of its own.
func BranchWithWork(t *testing.T, main, branch string, n int) {
	t.Helper()
	GitIn(t, main, "branch", branch, "main")
	AddWork(t, main, branch, n)
}

// LandOnMain merges branch into main and pushes, which is what merging its
// pull request amounts to.
func LandOnMain(t *testing.T, main, branch string) {
	t.Helper()
	GitIn(t, main, "merge", "-q", "--no-ff", "-m", "merge "+branch, branch)
	GitIn(t, main, "push", "-q", "origin", "main")
	GitIn(t, main, "fetch", "-q", "origin")
}

// Exists reports whether path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
