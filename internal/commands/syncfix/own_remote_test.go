package syncfix

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A branch behind its own remote is fast-forwarded and then rebased, with
// the remote's commit in it; undo puts it back where it stood before both.
func TestUpFastForwardsABranchBehindItsRemoteThenRebases(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	theirs := f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.AdvanceOrigin(t, 1)

	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err != nil {
		t.Fatalf("up: %v\n%s", err, human)
	}
	w := r.Worktrees[0]
	o := w.OwnRemoteSync
	if w.Result != synccmd.ResultRebased || testkit.Str(w.Before) != before || o == nil {
		t.Fatalf("participant %+v", w)
	}
	if o.State != "behind" || testkit.Str(o.Ref) != "origin/feat_wt/x" || testkit.Str(o.Local) != before || testkit.Str(o.Remote) != theirs ||
		*o.RemoteAhead != 1 || *o.LocalAhead != 0 || !o.FastForwarded || o.Allowed || o.SkippedReason != nil {
		t.Fatalf("ownRemoteSync %+v", o)
	}
	if !strings.Contains(human, "✓ fast-forwarded to origin/feat_wt/x  "+before[:7]+" → "+theirs[:7]+" (1 commit)\n") {
		t.Fatalf("no line about the fast-forward:\n%s", human)
	}
	if strings.Index(human, "fast-forwarded to origin/feat_wt/x") > strings.Index(human, "✓ rebased 2 commits") {
		t.Fatalf("the fast-forward is not before the rebase:\n%s", human)
	}
	if testkit.GitOut(t, x, "show", "HEAD:t.txt") != "t" || !testkit.GitAncestor(t, f.Main, "origin/main", "feat_wt/x") {
		t.Fatal("the rebased branch lacks the remote's commit, or is not on trunk")
	}
	// What is on the remote now is the old version of what the branch has.
	if got := synccmdtest.PlanOfWork(t, f.Ctx, "x").Worktree.OwnRemote; got.State != "rebased" || got.Blocks != nil || got.Fetched {
		t.Fatalf("after the run: %+v", got)
	}
	var out bytes.Buffer
	if err := SyncUndo(f.Ctx, "x", UndoOptions{VerbOptions: opts.VerbOptions}, &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatalf("undo left the branch at %s, want %s as before the fast-forward\n%s", got, before, out.String())
	}
}

// wt up promises a rebase that goes through without anybody: a branch that
// is behind its remote is judged at the remote's commit, the one the run
// would rebase, so a conflict that commit brings refuses before anything
// moves. wt sync rebase takes the same branch, fast-forwards it, hands the
// conflict over, and undo puts the branch back where it stood before both.
func TestABranchBehindItsRemoteIsJudgedAtTheRemotesCommit(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	f.Declare(t)
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	x, opts := f.PushedWork(t)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	theirs := f.Theirs(t, "feat_wt/x", "c.txt", "theirs\n")
	f.Theirs(t, "main", "c.txt", "trunk\n")

	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil || r.Worktrees[0].Result != synccmd.ResultRefused || !strings.Contains(testkit.Str(r.Worktrees[0].Reason), "would not sync cleanly") {
		t.Fatalf("wt up: %v %+v\n%s", err, r.Worktrees, human)
	}
	if o := r.Worktrees[0].OwnRemoteSync; o.State != "behind" || o.FastForwarded || o.SkippedReason != nil {
		t.Fatalf("ownRemoteSync %+v", o)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before || testkit.SafetyRefs(t, f.Main) != "" {
		t.Fatal("a refused wt up touched something")
	}

	run, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, synccmdtest.At(opts, 100))
	w := run.Worktrees[0]
	if err == nil || w.Result != synccmd.ResultHandedOver || !w.OwnRemoteSync.FastForwarded || testkit.Str(w.OwnRemoteSync.Local) != before {
		t.Fatalf("wt sync rebase: %v %+v %+v", err, w, w.OwnRemoteSync)
	}
	// The fast-forward stands under the rebase in progress.
	if got := testkit.GitOut(t, f.Main, "rev-parse", "refs/heads/feat_wt/x"); got != theirs || testkit.Str(w.After) != theirs {
		t.Fatalf("the branch is at %s, after %s, want the remote's %s", got, testkit.Str(w.After), theirs)
	}
	// The handover says which tip the safety ref pins, so a resume neither
	// refuses it nor forgets where undo goes.
	testkit.WriteIn(t, x, "c.txt", "both\n")
	testkit.GitIn(t, x, "add", "c.txt")
	resumed, err := syncResumeJSON(t, f.Ctx, "x", ResumeOptions{PushOptions: synccmd.PushOptions{Push: synccmd.PushNever}, VerbOptions: opts.VerbOptions})
	if err != nil || resumed.Worktrees[0].Result != synccmd.ResultRebased || resumed.Worktrees[0].OwnRemoteSync != nil {
		t.Fatalf("resume: %v %+v", err, resumed.Worktrees[0])
	}
	if testkit.GitOut(t, x, "show", "HEAD:c.txt") != "both" || !testkit.GitAncestor(t, f.Main, "origin/main", "feat_wt/x") {
		t.Fatal("the resumed rebase did not finish on trunk with the resolution")
	}
	undo, err := syncUndoJSON(t, f.Ctx, "x", UndoOptions{VerbOptions: opts.VerbOptions})
	if err != nil || undo.Worktrees[0].Result != synccmd.ResultUndone || testkit.Str(undo.Worktrees[0].After) != before || undo.Worktrees[0].OwnRemoteSync != nil {
		t.Fatalf("undo: %v %+v", err, undo.Worktrees[0])
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatalf("undo left the branch at %s, want %s", got, before)
	}
	if busy, _ := wtsync.RebaseInProgress(x); busy {
		t.Fatal("still mid-rebase after undo")
	}
}

// A rebase that is put back after the fast-forward takes the branch back to
// the tip the run found: fastForwarded stays as a record, after is local,
// and nothing is left for undo to trip over.
func TestSyncRunRestoresAStackParentToBeforeItsFastForward(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	f.Declare(t)
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	x, opts := f.PushedWork(t)
	var buf bytes.Buffer
	y, err := provision.New(f.Ctx, "feat/y", provision.NewOptions{NoSetup: true, Base: "feat_wt/x"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	testkit.Mine(t, y, "y.txt", "y\n")
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	f.Theirs(t, "feat_wt/x", "c.txt", "theirs\n")
	f.Theirs(t, "main", "c.txt", "trunk\n")

	run, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
	if err == nil || len(run.Worktrees) != 2 {
		t.Fatalf("%v %+v", err, run.Worktrees)
	}
	px := run.Worktrees[0]
	if px.Result != synccmd.ResultRestored || !px.OwnRemoteSync.FastForwarded || testkit.Str(px.After) != before || testkit.Str(px.OwnRemoteSync.Local) != before {
		t.Fatalf("parent %+v %+v", px, px.OwnRemoteSync)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatalf("restored to %s, want %s", got, before)
	}
	if run.Worktrees[1].Result != synccmd.ResultNotRun {
		t.Fatalf("child %+v", run.Worktrees[1])
	}
	if refs := testkit.GitOut(t, f.Main, "for-each-ref", "--format=%(refname)", "refs/wt-sync-result/"); refs != "" {
		t.Fatalf("a result ref outlived the restore:\n%s", refs)
	}
	var out bytes.Buffer
	if err := SyncUndo(f.Ctx, "x", UndoOptions{VerbOptions: opts.VerbOptions}, &out); err != nil || !strings.Contains(out.String(), "already at") {
		t.Fatalf("undo after a restore: %v\n%s", err, out.String())
	}
}
