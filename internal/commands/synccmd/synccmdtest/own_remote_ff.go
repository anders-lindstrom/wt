package synccmdtest

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// NothingToRebase is a trunkFixture whose trunk declares a sync
// configuration, with worktree x pushed and then, on another machine, brought
// onto a trunk that moved and pushed again: x here is behind its own remote,
// and at the remote's commit it is on trunk already. local is x's tip here,
// remote the one on origin, which the main checkout has fetched.
func NothingToRebase(t *testing.T) (f *TrunkFixture, x string, opts synccmd.RunOptions, local, remote string) {
	t.Helper()
	f = NewTrunkFixture(t)
	f.Declare(t)
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	x, opts = f.PushedWork(t)
	local = testkit.GitOut(t, x, "rev-parse", "HEAD")
	f.AdvanceOrigin(t, 1)
	remote = f.OnTrunkElsewhere(t, "feat_wt/x")
	return f, x, opts, local, remote
}

// OnTrunkElsewhere merges trunk into branch in the second clone and pushes
// it, then fetches: origin's branch is on trunk, the one here is not.
func (f *TrunkFixture) OnTrunkElsewhere(t *testing.T, branch string) string {
	t.Helper()
	testkit.GitIn(t, f.Other, "fetch", "-q", "origin")
	testkit.GitIn(t, f.Other, "checkout", "-q", "-B", branch, "origin/"+branch)
	testkit.GitIn(t, f.Other, "merge", "-q", "--no-edit", "origin/main")
	testkit.GitIn(t, f.Other, "push", "-q", "origin", branch)
	sha := testkit.GitOut(t, f.Other, "rev-parse", "HEAD")
	testkit.GitIn(t, f.Other, "checkout", "-q", "-B", "main", "origin/main")
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	return sha
}
