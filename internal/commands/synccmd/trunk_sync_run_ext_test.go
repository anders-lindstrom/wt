package synccmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

func TestUpFastForwardsLocalTrunkAfterItsFetch(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	f := synccmdtest.NewTrunkFixture(t)
	opts := f.WithWork(t)
	before := f.Local(t)
	remote := f.AdvanceOrigin(t, 2)
	testkit.GitIn(t, f.Main, "update-ref", "refs/remotes/origin/main", before) // as if never fetched

	r, err := synccmdtest.UpJSON(t, f.Ctx, "x", opts)
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if got := f.Local(t); got != remote {
		t.Fatalf("local main at %s, want origin's %s", got, remote)
	}
	ts := r.TrunkSync
	if ts == nil || !ts.FastForwarded || ts.SkippedReason != nil || *ts.Local != remote || *ts.RemoteAhead != 2 {
		t.Fatalf("trunkSync = %+v", ts)
	}
	if len(r.Worktrees) != 1 || r.Worktrees[0].Result != synccmd.ResultRebased {
		t.Fatalf("worktrees = %+v, want x rebased", r.Worktrees)
	}
}

func TestUpSaysWhatHappenedToLocalTrunk(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	f := synccmdtest.NewTrunkFixture(t)
	opts := f.WithWork(t)
	f.AdvanceOrigin(t, 1)
	var out bytes.Buffer
	if err := synccmd.Up(f.Ctx, "x", opts, &out); err != nil {
		t.Fatalf("up: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "local main  ") || !strings.Contains(out.String(), "fast-forwarded to origin/main") {
		t.Fatalf("no line about local main:\n%s", out.String())
	}
}

func TestUpNoFetchLeavesLocalTrunkAndReportsNull(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	f := synccmdtest.NewTrunkFixture(t)
	opts := f.WithWork(t)
	before := f.Local(t)
	f.AdvanceOrigin(t, 1)
	opts.NoFetch = true

	r, err := synccmdtest.UpJSON(t, f.Ctx, "x", opts)
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if r.TrunkSync != nil {
		t.Fatalf("trunkSync = %+v, want null without a fetch", r.TrunkSync)
	}
	if got := f.Local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
}

func TestUpOptedOutLeavesLocalTrunk(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	for _, how := range []string{"flag", "config"} {
		t.Run(how, func(t *testing.T) {
			f := synccmdtest.NewTrunkFixture(t)
			opts := f.WithWork(t)
			before := f.Local(t)
			f.AdvanceOrigin(t, 1)
			if how == "flag" {
				opts.NoFFTrunk = true
			} else {
				f.Ctx.UserConfig().FFTrunk = false
			}
			r, err := synccmdtest.UpJSON(t, f.Ctx, "x", opts)
			if err != nil {
				t.Fatalf("up: %v", err)
			}
			if r.TrunkSync == nil || r.TrunkSync.SkippedReason == nil || *r.TrunkSync.SkippedReason != syncrun.TrunkSkipOptedOut {
				t.Fatalf("trunkSync = %+v, want optedOut", r.TrunkSync)
			}
			if got := f.Local(t); got != before {
				t.Fatalf("local main moved to %s", got)
			}
		})
	}
}

// A fast-forward that fails never fails the rebase: it is reported.
func TestSyncRunReportsASkippedTrunkAndStillRebases(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	f := synccmdtest.NewTrunkFixture(t)
	opts := f.WithWork(t)
	testkit.GitIn(t, f.Main, "commit", "-q", "--allow-empty", "-m", "unpushed")
	f.AdvanceOrigin(t, 1)
	x := testkit.GitOut(t, f.Main, "rev-parse", "feat_wt/x")
	f.Declare(t)

	r, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
	if err != nil {
		t.Fatalf("sync rebase: %v", err)
	}
	if r.TrunkSync == nil || r.TrunkSync.SkippedReason == nil || *r.TrunkSync.SkippedReason != syncrun.TrunkSkipDiverged {
		t.Fatalf("trunkSync = %+v, want diverged", r.TrunkSync)
	}
	if len(r.Worktrees) != 1 || r.Worktrees[0].Result != synccmd.ResultRebased {
		t.Fatalf("worktrees = %+v, want x rebased", *r.Worktrees[0])
	}
	if now := testkit.GitOut(t, f.Main, "rev-parse", "feat_wt/x"); now == x {
		t.Fatal("x did not move")
	}
}

// wt status says what a run would do to local trunk and moves nothing.
func TestStatusJSONSaysWhatARunWouldDoToLocalTrunk(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	f := synccmdtest.NewTrunkFixture(t)
	f.WithWork(t)
	before := f.Local(t)
	f.AdvanceOrigin(t, 3)

	p := synccmdtest.PlanOfWork(t, f.Ctx, "x")

	ts := p.TrunkSync
	if ts == nil || ts.FastForwarded || ts.SkippedReason != nil || *ts.RemoteAhead != 3 || *ts.Local != before {
		t.Fatalf("trunkSync = %+v", ts)
	}
	if got := f.Local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
	testkit.WriteIn(t, f.Main, "scratch.txt", "mine\n")
	if p = synccmdtest.PlanOfWork(t, f.Ctx, "x"); p.TrunkSync.SkippedReason == nil || *p.TrunkSync.SkippedReason != syncrun.TrunkSkipDirty {
		t.Fatalf("trunkSync = %+v, want dirty", p.TrunkSync)
	}
}

// An --expect that no longer holds touches nothing: local trunk included.
func TestExpectRefusedLeavesLocalTrunk(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	for _, verb := range []string{"up", "sync rebase"} {
		t.Run(verb, func(t *testing.T) {
			f := synccmdtest.NewTrunkFixture(t)
			f.Declare(t)
			opts := f.WithWork(t)
			before := f.Local(t)
			f.AdvanceOrigin(t, 1)
			opts.Expect = "1:stale"
			var err error
			if verb == "up" {
				_, err = synccmdtest.UpJSON(t, f.Ctx, "x", opts)
			} else {
				_, err = synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
			}
			if err == nil || !strings.Contains(err.Error(), "changed since it was read") {
				t.Fatalf("want the stale token refused, got %v", err)
			}
			if got := f.Local(t); got != before {
				t.Fatalf("local main moved to %s on a refused run", got)
			}
		})
	}
}

// A repository whose trunk declares nothing has nothing for wt sync rebase to
// rebase, but it fetched: local trunk still follows.
func TestSyncRunUndeclaredStillFastForwardsLocalTrunk(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	f := synccmdtest.NewTrunkFixture(t)
	opts := f.WithWork(t)
	remote := f.AdvanceOrigin(t, 1)
	var out bytes.Buffer
	if err := synccmd.SyncRebase(f.Ctx, []string{"x"}, opts, &out); err == nil {
		t.Fatalf("an undeclared trunk ran:\n%s", out.String())
	}
	if got := f.Local(t); got != remote {
		t.Fatalf("local main at %s, want %s\n%s", got, remote, out.String())
	}
}
