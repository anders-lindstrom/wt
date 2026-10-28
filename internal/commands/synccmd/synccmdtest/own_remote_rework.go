package synccmdtest

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// MustBeDiverged is the check every shape below ends with: one commit made
// on a branch that no longer has its remote's commit, though a wt run once
// fast-forwarded it there. The remote's commit is not a former tip of the
// branch: wt put it there and wt took it away. A run refuses, and git's own
// --force-if-includes refuses the push a person might try by hand.
func MustBeDiverged(t *testing.T, f *TrunkFixture, x string, opts synccmd.RunOptions, theirs string) {
	t.Helper()
	testkit.Mine(t, x, "after.txt", "after\n")
	p := PlanOfWork(t, f.Ctx, "x")
	if o := p.Worktree.OwnRemote; o.State != "diverged" || testkit.Str(o.Blocks) != wtsync.OwnBlockDiverged || testkit.Str(p.UpIneligibleCode) != synccmd.IneligibleOwnDiverged {
		t.Fatalf("the remote's commit reads as a former tip: %+v, code %s", o, testkit.Str(p.UpIneligibleCode))
	}
	r, human, err := UpOut(t, f.Ctx, "x", At(opts, 500))
	if err == nil || r.Worktrees[0].Result != syncrun.ResultRefused || testkit.Str(r.Worktrees[0].OwnRemoteSync.SkippedReason) != wtsync.OwnBlockDiverged {
		t.Fatalf("a run went on over the remote's commit: %v %+v\n%s", err, r.Worktrees, human)
	}
	if out, err := gittest.Try(t, x, "push", "--force-with-lease", "--force-if-includes", "origin", "feat_wt/x"); err == nil {
		t.Fatalf("git's own --force-if-includes let the push through:\n%s", out)
	}
	if got := testkit.GitOut(t, f.Origin, "rev-parse", "refs/heads/feat_wt/x"); got != theirs {
		t.Fatalf("origin's branch is at %s, want it still at %s", got, theirs)
	}
}
