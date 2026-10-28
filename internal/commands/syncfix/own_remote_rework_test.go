package syncfix

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// Behind, fast-forwarded and rebased by a run, then undone.
func TestAFastForwardThatWasUndoneLeavesNoFormerTip(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	theirs := f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.AdvanceOrigin(t, 1)
	if r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts); err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded {
		t.Fatalf("%v\n%s", err, human)
	}
	var out bytes.Buffer
	if err := SyncUndo(f.Ctx, "x", UndoOptions{VerbOptions: opts.VerbOptions}, &out); err != nil || testkit.GitOut(t, x, "rev-parse", "HEAD") != before {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	synccmdtest.MustBeDiverged(t, f, x, opts, theirs)
}

// The same for the fast-forward that is all a run did.
func TestAFastForwardAloneThatWasUndoneLeavesNoFormerTip(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f, x, opts, local, remote := synccmdtest.NothingToRebase(t)
	if r, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts); err != nil || r.Worktrees[0].Result != synccmd.ResultFastForwarded {
		t.Fatalf("%v %+v", err, r.Worktrees)
	}
	var out bytes.Buffer
	if err := SyncUndo(f.Ctx, "x", UndoOptions{VerbOptions: opts.VerbOptions}, &out); err != nil || testkit.GitOut(t, x, "rev-parse", "HEAD") != local {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	synccmdtest.MustBeDiverged(t, f, x, opts, remote)
}

// A fast-forward that is all a run did counts as finished: a commit made on
// top is plainly ahead, and once the run is undone the branch is behind
// again, nothing else.
func TestAFastForwardAloneIsFinishedAndItsUndoIsBehindAgain(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f, x, opts, local, remote := synccmdtest.NothingToRebase(t)
	if r, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts); err != nil || r.Worktrees[0].Result != synccmd.ResultFastForwarded {
		t.Fatalf("%v %+v", err, r.Worktrees)
	}
	if refs := testkit.GitOut(t, f.Main, "for-each-ref", "--format=%(refname) %(objectname)", "refs/wt-sync-result/", "refs/wt-sync-ff/"); strings.Count(refs, remote) != 2 {
		t.Fatalf("a finished fast-forward pins where it left the branch twice over:\n%s", refs)
	}
	testkit.Mine(t, x, "on-top.txt", "on top\n")
	if o := synccmdtest.PlanOfWork(t, f.Ctx, "x").Worktree.OwnRemote; o.State != "ahead" || *o.Ahead != 1 {
		t.Fatalf("a commit on top: %+v", o)
	}
	testkit.GitIn(t, x, "reset", "-q", "--hard", remote)
	var out bytes.Buffer
	if err := SyncUndo(f.Ctx, "x", UndoOptions{VerbOptions: opts.VerbOptions}, &out); err != nil || testkit.GitOut(t, x, "rev-parse", "HEAD") != local {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if o := synccmdtest.PlanOfWork(t, f.Ctx, "x").Worktree.OwnRemote; o.State != "behind" || o.Blocks != nil {
		t.Fatalf("after the undo: %+v", o)
	}
	if refs := testkit.GitOut(t, f.Main, "for-each-ref", "--format=%(refname)", "refs/wt-sync-result/", "refs/wt-sync-ff/"); refs != "" {
		t.Fatalf("the undo left the run's word that the branch was there:\n%s", refs)
	}
}

// A fast-forward, a conflict handed over and a resume that finishes it leave
// the remote's commit a former tip, and a second run goes on; the same
// handover undone leaves none.
func TestAHandoverAfterAFastForwardKeepsOrDropsTheFormerTip(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	handedOver := func(t *testing.T) (f *synccmdtest.TrunkFixture, x string, opts synccmd.RunOptions, theirs string) {
		t.Helper()
		f = synccmdtest.NewTrunkFixture(t)
		f.Declare(t)
		testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
		x, opts = f.PushedWork(t)
		theirs = f.Theirs(t, "feat_wt/x", "c.txt", "theirs\n")
		f.Theirs(t, "main", "c.txt", "trunk\n")
		run, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
		if err == nil || run.Worktrees[0].Result != synccmd.ResultHandedOver || !run.Worktrees[0].OwnRemoteSync.FastForwarded {
			t.Fatalf("%v %+v", err, run.Worktrees[0])
		}
		return f, x, opts, theirs
	}
	t.Run("resumed", func(t *testing.T) {
		f, x, opts, _ := handedOver(t)
		testkit.WriteIn(t, x, "c.txt", "both\n")
		testkit.GitIn(t, x, "add", "c.txt")
		if r, err := syncResumeJSON(t, f.Ctx, "x", ResumeOptions{PushOptions: syncrun.PushOptions{Push: syncrun.PushNever}, VerbOptions: opts.VerbOptions}); err != nil || r.Worktrees[0].Result != synccmd.ResultRebased {
			t.Fatalf("resume: %v %+v", err, r.Worktrees)
		}
		if o := synccmdtest.PlanOfWork(t, f.Ctx, "x").Worktree.OwnRemote; o.State != "rebased" {
			t.Fatalf("after the resume: %+v", o)
		}
		f.AdvanceOrigin(t, 1)
		if r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", synccmdtest.At(opts, 500)); err != nil || r.Worktrees[0].Result != synccmd.ResultRebased || r.Worktrees[0].OwnRemoteSync.State != "rebased" {
			t.Fatalf("a second run: %v %+v\n%s", err, r.Worktrees, human)
		}
	})
	t.Run("undone", func(t *testing.T) {
		f, x, opts, theirs := handedOver(t)
		var out bytes.Buffer
		if err := SyncUndo(f.Ctx, "x", UndoOptions{VerbOptions: opts.VerbOptions}, &out); err != nil {
			t.Fatalf("undo: %v\n%s", err, out.String())
		}
		// Trunk's c.txt is in the way of nothing here: the run that follows
		// is refused for the divergence before it rebases anything.
		synccmdtest.MustBeDiverged(t, f, x, opts, theirs)
	})
}

// A forced undo of a handover that followed a fast-forward keeps what a
// person committed inside the rebase, as it does without a fast-forward:
// the branch ref sits at the remote's commit there, not at the tip the run
// found, and the pin must not depend on which.
func TestSyncUndoForcedPinsACommitInsideAHandoverThatFollowedAFastForward(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	f.Declare(t)
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	x, opts := f.PushedWork(t)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	f.Theirs(t, "feat_wt/x", "c.txt", "theirs\n")
	f.Theirs(t, "main", "c.txt", "trunk\n")
	if run, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts); err == nil || run.Worktrees[0].Result != synccmd.ResultHandedOver || !run.Worktrees[0].OwnRemoteSync.FastForwarded {
		t.Fatalf("%v %+v", err, run.Worktrees[0])
	}
	testkit.WriteIn(t, x, "c.txt", "merged by hand\n")
	testkit.GitIn(t, x, "add", "--", "c.txt")
	testkit.GitIn(t, x, "commit", "-q", "-m", "resolved by hand")
	inside := testkit.GitOut(t, x, "rev-parse", "HEAD")

	undo := UndoOptions{VerbOptions: synccmdtest.At(opts, 100).VerbOptions}
	var out bytes.Buffer
	if err := SyncUndo(f.Ctx, "x", undo, &out); err == nil || !strings.Contains(err.Error(), "made inside the handed-over rebase") {
		t.Fatalf("a plain undo: %v\n%s", err, out.String())
	}
	undo.Force = true
	out.Reset()
	if err := SyncUndo(f.Ctx, "x", undo, &out); err != nil {
		t.Fatalf("forced: %v\n%s", err, out.String())
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatalf("the branch is at %s, want the tip the run found %s", got, before)
	}
	if pinned, err := gittest.Try(t, f.Main, "rev-parse", "--verify", "--quiet", "refs/wt-sync/feat_wt/x/100"); err != nil || strings.TrimSpace(pinned) != inside {
		t.Fatalf("the commit made inside the rebase is not pinned: %q %v\n%s", pinned, err, out.String())
	}
	if !strings.Contains(out.String(), inside[:7]+" kept under refs/wt-sync/feat_wt/x/100") {
		t.Fatalf("the row does not say where it kept it:\n%s", out.String())
	}
}
