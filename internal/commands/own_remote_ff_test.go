package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// nothingToRebase is a trunkFixture whose trunk declares a sync
// configuration, with worktree x pushed and then, on another machine, brought
// onto a trunk that moved and pushed again: x here is behind its own remote,
// and at the remote's commit it is on trunk already. local is x's tip here,
// remote the one on origin, which the main checkout has fetched.
func nothingToRebase(t *testing.T) (f *trunkFixture, x string, opts RunOptions, local, remote string) {
	t.Helper()
	f = newTrunkFixture(t)
	f.declare(t)
	gitIn(t, f.main, "fetch", "-q", "origin")
	x, opts = f.pushedWork(t)
	local = gitOut(t, x, "rev-parse", "HEAD")
	f.advanceOrigin(t, 1)
	remote = f.onTrunkElsewhere(t, "feat_wt/x")
	return f, x, opts, local, remote
}

// onTrunkElsewhere merges trunk into branch in the second clone and pushes
// it, then fetches: origin's branch is on trunk, the one here is not.
func (f *trunkFixture) onTrunkElsewhere(t *testing.T, branch string) string {
	t.Helper()
	gitIn(t, f.other, "fetch", "-q", "origin")
	gitIn(t, f.other, "checkout", "-q", "-B", branch, "origin/"+branch)
	gitIn(t, f.other, "merge", "-q", "--no-edit", "origin/main")
	gitIn(t, f.other, "push", "-q", "origin", branch)
	sha := gitOut(t, f.other, "rev-parse", "HEAD")
	gitIn(t, f.other, "checkout", "-q", "-B", "main", "origin/main")
	gitIn(t, f.main, "fetch", "-q", "origin")
	return sha
}

// A branch behind its own remote with nothing to rebase once it is there is
// fast-forwarded and that is all: the look files it under ready, the run
// reports fastForwarded with nothing to push, and undo puts it back.
func TestSyncRunFastForwardsABranchWithNothingToRebase(t *testing.T) {
	f, x, opts, local, remote := nothingToRebase(t)

	row := overviewRow(t, overviewJSON(t, f.ctx), "x")
	if row.Class != "current" || row.Verdict != VerdictProceed || !row.Runnable || row.Group != GroupReady || row.Reason != nil ||
		row.OwnRemote.State != "behind" || row.OwnRemote.Blocks != nil || str(row.OwnRemote.Commit) != remote {
		t.Fatalf("overview row: %+v", row)
	}
	if p := planOfWork(t, f.ctx, "x"); !p.UpEligible || p.Token == nil || p.Worktree.OwnRemote.State != "behind" || p.Worktree.OwnRemote.Blocks != nil {
		t.Fatalf("plan: eligible %v %+v", p.UpEligible, p.Worktree.OwnRemote)
	}
	var out bytes.Buffer
	if err := Sync(f.ctx, SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	n := gitOut(t, f.main, "rev-list", "--count", local+".."+remote)
	if s := out.String(); !strings.Contains(s, "\nready") || strings.Contains(s, "every worktree is on trunk") ||
		!strings.Contains(s, "\n    "+n+" behind origin/feat_wt/x, where there is nothing to rebase: a run fast-forwards it and is done (as last fetched)\n") {
		t.Fatalf("overview:\n%s", s)
	}

	opts.IfReady = true
	r, err := syncRunJSON(t, f.ctx, []string{"x"}, opts)
	if err != nil || r.Outcome != OutcomeDone || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	w := r.Worktrees[0]
	o := w.OwnRemoteSync
	if w.Result != ResultFastForwarded || str(w.Before) != local || str(w.After) != remote || w.Reason != nil ||
		len(w.PushCommand) != 0 || w.Pushed || len(w.Deferred) != 0 ||
		str(w.SafetyRef) != "refs/wt-sync/feat_wt/x/99" || strings.Join(w.UndoCommand, " ") != "wt sync undo x" {
		t.Fatalf("participant %+v", w)
	}
	if o.State != "behind" || !o.FastForwarded || o.SkippedReason != nil || str(o.Local) != local || str(o.Remote) != remote {
		t.Fatalf("ownRemoteSync %+v", o)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != remote {
		t.Fatalf("the branch is at %s, want its remote's %s", got, remote)
	}
	if st := gitOut(t, x, "status", "--porcelain"); st != "" || gitOut(t, x, "symbolic-ref", "HEAD") != "refs/heads/feat_wt/x" {
		t.Fatalf("the checkout is not clean on its branch:\n%s", st)
	}
	// Equal to its remote now, and on trunk: nothing for a run to do.
	if row := overviewRow(t, overviewJSON(t, f.ctx), "x"); row.Group != GroupCurrent || row.OwnRemote.State != "inSync" {
		t.Fatalf("after the run: %+v", row)
	}

	undo, err := syncUndoJSON(t, f.ctx, "x", UndoOptions{verbOptions: opts.verbOptions})
	if err != nil || undo.Worktrees[0].Result != ResultUndone || str(undo.Worktrees[0].After) != local {
		t.Fatalf("undo: %v %+v", err, undo.Worktrees)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != local {
		t.Fatalf("undo left the branch at %s, want %s", got, local)
	}
}

// A run with nothing named takes it with the other ready ones, says what it
// did in one line and how to take it back, and offers nothing to push.
func TestSyncRunWithNothingNamedTakesAFastForwardOnlyWorktree(t *testing.T) {
	f, x, opts, local, remote := nothingToRebase(t)
	n := gitOut(t, f.main, "rev-list", "--count", local+".."+remote)
	var out bytes.Buffer
	if err := SyncRun(f.ctx, nil, opts, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		"every ready worktree: x\n",
		"  ✓ fast-forwarded to origin/feat_wt/x  " + local[:7] + " → " + remote[:7] + " (" + n + " commits); already on trunk, nothing to rebase\n",
		"  ↩ wt sync undo x puts it back (was " + local[:7] + ")\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "push:") || strings.Contains(s, "rebased") {
		t.Fatalf("a fast-forward is neither rebased nor pushed:\n%s", s)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != remote {
		t.Fatalf("the branch is at %s", got)
	}
}

// wt up does the same, and with --no-fetch it goes by the remote as last
// fetched and says so.
func TestUpFastForwardsABranchWithNothingToRebase(t *testing.T) {
	f, x, opts, local, remote := nothingToRebase(t)
	opts.NoFetch = true
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err != nil || r.Outcome != OutcomeDone {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	w := r.Worktrees[0]
	if w.Result != ResultFastForwarded || str(w.After) != remote || !w.OwnRemoteSync.FastForwarded || len(w.PushCommand) != 0 {
		t.Fatalf("%+v %+v", w, w.OwnRemoteSync)
	}
	if !strings.Contains(human, "✓ fast-forwarded to origin/feat_wt/x  "+local[:7]+" → "+remote[:7]) ||
		!strings.Contains(human, "(as last fetched); already on trunk, nothing to rebase\n") {
		t.Fatalf("not said, or not said to be as last fetched:\n%s", human)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != remote {
		t.Fatalf("the branch is at %s", got)
	}
}

// In a stack: a parent that only needs the fast-forward gets it, and its
// child is rebased onto where that leaves the parent.
func TestUpFastForwardsAStackParentWithNothingToRebaseAndRebasesItsChild(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	var buf bytes.Buffer
	y, err := New(f.ctx, "feat/y", NewOptions{NoSetup: true, Base: "feat_wt/x"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	mine(t, y, "y.txt", "y\n")
	local := gitOut(t, x, "rev-parse", "HEAD")
	f.advanceOrigin(t, 1)
	remote := f.onTrunkElsewhere(t, "feat_wt/x")

	r, human, err := upOut(t, f.ctx, "y", opts)
	if err != nil || len(r.Worktrees) != 2 || r.Outcome != OutcomeDone {
		t.Fatalf("%v %+v\n%s", err, r.Worktrees, human)
	}
	if px := r.Worktrees[0]; px.Branch != "feat_wt/x" || px.Result != ResultFastForwarded || str(px.Before) != local || str(px.After) != remote {
		t.Fatalf("parent %+v", px)
	}
	if py := r.Worktrees[1]; py.Result != ResultRebased || py.OwnRemoteSync.FastForwarded {
		t.Fatalf("child %+v %+v", py, py.OwnRemoteSync)
	}
	if gitOut(t, x, "rev-parse", "HEAD") != remote || gitOut(t, y, "rev-parse", "HEAD~1") != remote {
		t.Fatalf("the child is not one commit on the parent's fast-forwarded tip:\n%s", human)
	}
	if !gitAncestor(t, f.main, "origin/main", "feat_wt/y") {
		t.Fatal("the child is not on trunk")
	}
	// Not a torn stack: the child is not rebased onto trunk with a copy of
	// the parent's commit under it, which is what skipping the parent did.
	if n := gitOut(t, f.main, "rev-list", "--count", "feat_wt/x..feat_wt/y"); n != "1" {
		t.Fatalf("the child carries %s commits the parent lacks, want its own one", n)
	}
	// Undoing the child's run takes back the whole run, the parent's
	// fast-forward with it.
	var out bytes.Buffer
	if err := SyncUndo(f.ctx, "y", UndoOptions{verbOptions: opts.verbOptions}, &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != local {
		t.Fatalf("undo left the parent at %s, want %s", got, local)
	}
}

// Where the fast-forward is not safe it is the behind block as before:
// refused untouched, with the code that says why.
func TestSyncRunRefusesAFastForwardOnlyBranchItCannotMove(t *testing.T) {
	t.Run("dirty", func(t *testing.T) {
		f, x, opts, local, _ := nothingToRebase(t)
		writeIn(t, x, "x.txt", "edited\n")
		if p := planOfWork(t, f.ctx, "x"); str(p.UpIneligibleCode) != IneligibleOwnBehind || p.Token != nil ||
			str(p.Worktree.OwnRemote.Blocks) != wtsync.OwnBlockDirty {
			t.Fatalf("plan: %s %+v", str(p.UpIneligibleCode), p.Worktree.OwnRemote)
		}
		if row := overviewRow(t, overviewJSON(t, f.ctx), "x"); row.Verdict != VerdictRefuse || row.Runnable || row.Group != GroupNeedsYou {
			t.Fatalf("overview row: %+v", row)
		}
		r, err := syncRunJSON(t, f.ctx, []string{"x"}, opts)
		if err == nil || r.Worktrees[0].Result != ResultRefused || str(r.Worktrees[0].OwnRemoteSync.SkippedReason) != wtsync.OwnBlockDirty {
			t.Fatalf("%v %+v %+v", err, r.Worktrees[0], r.Worktrees[0].OwnRemoteSync)
		}
		if got := gitOut(t, x, "rev-parse", "HEAD"); got != local || safetyRefs(t, f.main) != "" {
			t.Fatal("a refused run touched something")
		}
	})
	t.Run("a busy session", func(t *testing.T) {
		f, x, opts, local, remote := nothingToRebase(t)
		busy := []wtsync.Agent{{Name: "busy-1", Cwd: x, Status: "busy"}}
		opts.Agents, opts.Relist = busy, func() ([]wtsync.Agent, error) { return busy, nil }
		r, err := syncRunJSON(t, f.ctx, []string{"x"}, opts)
		if err == nil || r.Worktrees[0].Result != ResultRefused || str(r.Worktrees[0].OwnRemoteSync.SkippedReason) != wtsync.OwnBlockSession {
			t.Fatalf("%v %+v %+v", err, r.Worktrees[0], r.Worktrees[0].OwnRemoteSync)
		}
		if got := gitOut(t, x, "rev-parse", "HEAD"); got != local || safetyRefs(t, f.main) != "" {
			t.Fatal("a refused run touched something")
		}
		// --force takes the run past the session, and the fast-forward with it.
		opts.Force = true
		r, err = syncRunJSON(t, f.ctx, []string{"x"}, opts)
		if err != nil || r.Worktrees[0].Result != ResultFastForwarded || str(r.Worktrees[0].After) != remote {
			t.Fatalf("--force: %v %+v", err, r.Worktrees[0])
		}
	})
	t.Run("an idle session is told", func(t *testing.T) {
		f, x, opts, _, remote := nothingToRebase(t)
		opts.Agents = idleIn(t, x, "parked-1")
		opts.Relist = func() ([]wtsync.Agent, error) { return opts.Agents, nil }
		var out bytes.Buffer
		if err := SyncRun(f.ctx, []string{"x"}, opts, &out); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		if !strings.Contains(out.String(), "wt: x fast-forwarded to origin/feat_wt/x (+") || gitOut(t, x, "rev-parse", "HEAD") != remote {
			t.Fatalf("the idle session is not told, or nothing moved:\n%s", out.String())
		}
	})
}
