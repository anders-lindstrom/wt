package synccmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A run with nothing named takes it with the other ready ones, says what it
// did in one line and how to take it back, and offers nothing to push.
func TestSyncRunWithNothingNamedTakesAFastForwardOnlyWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f, x, opts, local, remote := synccmdtest.NothingToRebase(t)
	n := testkit.GitOut(t, f.Main, "rev-list", "--count", local+".."+remote)
	var out bytes.Buffer
	if err := synccmd.SyncRebase(f.Ctx, nil, opts, &out); err != nil {
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
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != remote {
		t.Fatalf("the branch is at %s", got)
	}
}

// wt up does the same, and with --no-fetch it goes by the remote as last
// fetched and says so.
func TestUpFastForwardsABranchWithNothingToRebase(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f, x, opts, local, remote := synccmdtest.NothingToRebase(t)
	opts.NoFetch = true
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err != nil || r.Outcome != kit.OutcomeDone {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	w := r.Worktrees[0]
	if w.Result != synccmd.ResultFastForwarded || testkit.Str(w.After) != remote || !w.OwnRemoteSync.FastForwarded || len(w.PushCommand) != 0 {
		t.Fatalf("%+v %+v", w, w.OwnRemoteSync)
	}
	if !strings.Contains(human, "✓ fast-forwarded to origin/feat_wt/x  "+local[:7]+" → "+remote[:7]) ||
		!strings.Contains(human, "(as last fetched); already on trunk, nothing to rebase\n") {
		t.Fatalf("not said, or not said to be as last fetched:\n%s", human)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != remote {
		t.Fatalf("the branch is at %s", got)
	}
}

// Where the fast-forward is not safe it is the behind block as before:
// refused untouched, with the code that says why.
func TestSyncRunRefusesAFastForwardOnlyBranchItCannotMove(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	t.Run("dirty", func(t *testing.T) {
		f, x, opts, local, _ := synccmdtest.NothingToRebase(t)
		testkit.WriteIn(t, x, "x.txt", "edited\n")
		if p := synccmdtest.PlanOfWork(t, f.Ctx, "x"); testkit.Str(p.UpIneligibleCode) != synccmd.IneligibleOwnBehind || p.Token != nil ||
			testkit.Str(p.Worktree.OwnRemote.Blocks) != wtsync.OwnBlockDirty {
			t.Fatalf("plan: %s %+v", testkit.Str(p.UpIneligibleCode), p.Worktree.OwnRemote)
		}
		if row := synccmdtest.OverviewRow(t, synccmdtest.OverviewJSON(t, f.Ctx), "x"); row.Verdict != synccmd.VerdictRefuse || row.Runnable || row.Group != synccmd.GroupNeedsYou {
			t.Fatalf("overview row: %+v", row)
		}
		r, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
		if err == nil || r.Worktrees[0].Result != synccmd.ResultRefused || testkit.Str(r.Worktrees[0].OwnRemoteSync.SkippedReason) != wtsync.OwnBlockDirty {
			t.Fatalf("%v %+v %+v", err, r.Worktrees[0], r.Worktrees[0].OwnRemoteSync)
		}
		if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != local || testkit.SafetyRefs(t, f.Main) != "" {
			t.Fatal("a refused run touched something")
		}
	})
	t.Run("a busy session", func(t *testing.T) {
		f, x, opts, local, remote := synccmdtest.NothingToRebase(t)
		busy := []wtsync.Agent{{Name: "busy-1", Cwd: x, Status: "busy"}}
		opts.Agents, opts.Relist = busy, func() ([]wtsync.Agent, error) { return busy, nil }
		r, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
		if err == nil || r.Worktrees[0].Result != synccmd.ResultRefused || testkit.Str(r.Worktrees[0].OwnRemoteSync.SkippedReason) != wtsync.OwnBlockSession {
			t.Fatalf("%v %+v %+v", err, r.Worktrees[0], r.Worktrees[0].OwnRemoteSync)
		}
		if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != local || testkit.SafetyRefs(t, f.Main) != "" {
			t.Fatal("a refused run touched something")
		}
		// --force takes the run past the session, and the fast-forward with it.
		opts.Force = true
		r, err = synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
		if err != nil || r.Worktrees[0].Result != synccmd.ResultFastForwarded || testkit.Str(r.Worktrees[0].After) != remote {
			t.Fatalf("--force: %v %+v", err, r.Worktrees[0])
		}
	})
	t.Run("an idle session is told", func(t *testing.T) {
		f, x, opts, _, remote := synccmdtest.NothingToRebase(t)
		opts.Agents = testkit.IdleIn(t, x, "parked-1")
		opts.Relist = func() ([]wtsync.Agent, error) { return opts.Agents, nil }
		var out bytes.Buffer
		if err := synccmd.SyncRebase(f.Ctx, []string{"x"}, opts, &out); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		if !strings.Contains(out.String(), "wt: x fast-forwarded to origin/feat_wt/x (+") || testkit.GitOut(t, x, "rev-parse", "HEAD") != remote {
			t.Fatalf("the idle session is not told, or nothing moved:\n%s", out.String())
		}
	})
}
