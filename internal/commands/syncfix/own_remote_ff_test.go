package syncfix

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// A branch behind its own remote with nothing to rebase once it is there is
// fast-forwarded and that is all: the look files it under ready, the run
// reports fastForwarded with nothing to push, and undo puts it back.
func TestSyncRunFastForwardsABranchWithNothingToRebase(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f, x, opts, local, remote := synccmdtest.NothingToRebase(t)

	row := synccmdtest.OverviewRow(t, synccmdtest.OverviewJSON(t, f.Ctx), "x")
	if row.Class != "current" || row.Verdict != synccmd.VerdictProceed || !row.Runnable || row.Group != synccmd.GroupReady || row.Reason != nil ||
		row.OwnRemote.State != "behind" || row.OwnRemote.Blocks != nil || testkit.Str(row.OwnRemote.Commit) != remote {
		t.Fatalf("overview row: %+v", row)
	}
	if p := synccmdtest.PlanOfWork(t, f.Ctx, "x"); !p.UpEligible || p.Token == nil || p.Worktree.OwnRemote.State != "behind" || p.Worktree.OwnRemote.Blocks != nil {
		t.Fatalf("plan: eligible %v %+v", p.UpEligible, p.Worktree.OwnRemote)
	}
	var out bytes.Buffer
	if err := synccmd.Sync(f.Ctx, synccmd.SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	n := testkit.GitOut(t, f.Main, "rev-list", "--count", local+".."+remote)
	if s := out.String(); !strings.Contains(s, "\nready") || strings.Contains(s, "every worktree is on trunk") ||
		!strings.Contains(s, "\n    "+n+" behind origin/feat_wt/x, where there is nothing to rebase: a run fast-forwards it and is done (as last fetched)\n") {
		t.Fatalf("overview:\n%s", s)
	}

	opts.IfReady = true
	r, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
	if err != nil || r.Outcome != kit.OutcomeDone || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	w := r.Worktrees[0]
	o := w.OwnRemoteSync
	if w.Result != synccmd.ResultFastForwarded || testkit.Str(w.Before) != local || testkit.Str(w.After) != remote || w.Reason != nil ||
		len(w.PushCommand) != 0 || w.Pushed || len(w.Deferred) != 0 ||
		testkit.Str(w.SafetyRef) != "refs/wt-sync/feat_wt/x/99" || strings.Join(w.UndoCommand, " ") != "wt sync undo x" {
		t.Fatalf("participant %+v", w)
	}
	if o.State != "behind" || !o.FastForwarded || o.SkippedReason != nil || testkit.Str(o.Local) != local || testkit.Str(o.Remote) != remote {
		t.Fatalf("ownRemoteSync %+v", o)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != remote {
		t.Fatalf("the branch is at %s, want its remote's %s", got, remote)
	}
	if st := testkit.GitOut(t, x, "status", "--porcelain"); st != "" || testkit.GitOut(t, x, "symbolic-ref", "HEAD") != "refs/heads/feat_wt/x" {
		t.Fatalf("the checkout is not clean on its branch:\n%s", st)
	}
	// Equal to its remote now, and on trunk: nothing for a run to do.
	if row := synccmdtest.OverviewRow(t, synccmdtest.OverviewJSON(t, f.Ctx), "x"); row.Group != synccmd.GroupCurrent || row.OwnRemote.State != "inSync" {
		t.Fatalf("after the run: %+v", row)
	}

	undo, err := syncUndoJSON(t, f.Ctx, "x", UndoOptions{VerbOptions: opts.VerbOptions})
	if err != nil || undo.Worktrees[0].Result != synccmd.ResultUndone || testkit.Str(undo.Worktrees[0].After) != local {
		t.Fatalf("undo: %v %+v", err, undo.Worktrees)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != local {
		t.Fatalf("undo left the branch at %s, want %s", got, local)
	}
}

// In a stack: a parent that only needs the fast-forward gets it, and its
// child is rebased onto where that leaves the parent.
func TestUpFastForwardsAStackParentWithNothingToRebaseAndRebasesItsChild(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	var buf bytes.Buffer
	y, err := provision.New(f.Ctx, "feat/y", provision.NewOptions{NoSetup: true, Base: "feat_wt/x"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	testkit.Mine(t, y, "y.txt", "y\n")
	local := testkit.GitOut(t, x, "rev-parse", "HEAD")
	f.AdvanceOrigin(t, 1)
	remote := f.OnTrunkElsewhere(t, "feat_wt/x")

	r, human, err := synccmdtest.UpOut(t, f.Ctx, "y", opts)
	if err != nil || len(r.Worktrees) != 2 || r.Outcome != kit.OutcomeDone {
		t.Fatalf("%v %+v\n%s", err, r.Worktrees, human)
	}
	if px := r.Worktrees[0]; px.Branch != "feat_wt/x" || px.Result != synccmd.ResultFastForwarded || testkit.Str(px.Before) != local || testkit.Str(px.After) != remote {
		t.Fatalf("parent %+v", px)
	}
	if py := r.Worktrees[1]; py.Result != synccmd.ResultRebased || py.OwnRemoteSync.FastForwarded {
		t.Fatalf("child %+v %+v", py, py.OwnRemoteSync)
	}
	if testkit.GitOut(t, x, "rev-parse", "HEAD") != remote || testkit.GitOut(t, y, "rev-parse", "HEAD~1") != remote {
		t.Fatalf("the child is not one commit on the parent's fast-forwarded tip:\n%s", human)
	}
	if !testkit.GitAncestor(t, f.Main, "origin/main", "feat_wt/y") {
		t.Fatal("the child is not on trunk")
	}
	// Not a torn stack: the child is not rebased onto trunk with a copy of
	// the parent's commit under it, which is what skipping the parent did.
	if n := testkit.GitOut(t, f.Main, "rev-list", "--count", "feat_wt/x..feat_wt/y"); n != "1" {
		t.Fatalf("the child carries %s commits the parent lacks, want its own one", n)
	}
	// Undoing the child's run takes back the whole run, the parent's
	// fast-forward with it.
	var out bytes.Buffer
	if err := SyncUndo(f.Ctx, "y", UndoOptions{VerbOptions: opts.VerbOptions}, &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != local {
		t.Fatalf("undo left the parent at %s, want %s", got, local)
	}
}
