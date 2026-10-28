package synccmd_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A finished rebase says where the old tip is pinned, what each deferred
// step did, and how to take it all back.
func TestSyncRunJSONReportsTheSafetyRefDeferredStepsAndUndo(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, true)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	r, err := synccmdtest.SyncRunJSON(t, ctx, []string{"bump"}, synccmdtest.NoAgents())
	if err != nil || r.Outcome != kit.OutcomeDone || r.Command != "sync rebase" || r.Repo == nil || *r.Repo != ctx.Repo.MainRoot ||
		len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	p := r.Worktrees[0]
	if p.Result != syncrun.ResultRebased || p.Before == nil || *p.Before != old || p.After == nil || *p.After != testkit.GitOut(t, bump, "rev-parse", "HEAD") {
		t.Errorf("participant %+v", p)
	}
	if p.SafetyRef == nil || *p.SafetyRef != wtsync.SafetyRef("feat_wt/bump", 99) {
		t.Errorf("safety ref %v", p.SafetyRef)
	}
	if !slices.Equal(p.UndoCommand, []string{"wt", "sync", "undo", "bump"}) {
		t.Errorf("undo %q", p.UndoCommand)
	}
	if len(p.Deferred) != 1 || p.Deferred[0].Step != "cp v.txt gen.txt" || p.Deferred[0].Result != syncrun.DeferredCommitted ||
		p.Deferred[0].Commit == nil {
		t.Errorf("deferred %+v", p.Deferred)
	}
	if p.Pushed || len(p.PushCommand) == 0 {
		t.Errorf("push %v %q", p.Pushed, p.PushCommand)
	}
}

// --expect on a run holds it to the overview wt sync --json printed: a
// stack that grew since is refused with nothing touched; the overview as
// read runs.
func TestSyncRunJSONExpectHoldsTheRunToTheOverview(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	token := *synccmdtest.OverviewJSON(t, ctx).Token
	provisiontest.StackFixture(t, ctx)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	opts := synccmdtest.NoAgents()
	opts.Expect = token
	for _, works := range [][]string{nil, {"bump"}} {
		r, err := synccmdtest.SyncRunJSON(t, ctx, works, opts)
		if err == nil || r.Outcome != kit.OutcomeRefused || r.Error == nil || len(r.Worktrees) != 0 {
			t.Fatalf("%v: a grown stack is refused before any participant: %v %+v", works, err, r)
		}
		if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
			t.Fatal("the worktree moved")
		}
	}
	opts.Expect = *synccmdtest.OverviewJSON(t, ctx).Token
	if r, err := synccmdtest.SyncRunJSON(t, ctx, nil, opts); err != nil || r.Outcome != kit.OutcomeDone || len(r.Worktrees) != 2 {
		t.Fatalf("the overview as read runs: %v %+v", err, r)
	}
}

// A contested stop handed over: its plan, its safety ref and both ways on.
func TestSyncRunJSONReportsAHandover(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.ContestedFixture(t)
	r, err := synccmdtest.SyncRunJSON(t, ctx, []string{"bump"}, synccmdtest.NoAgents())
	if err == nil || r.Outcome != kit.OutcomePartial || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	p := r.Worktrees[0]
	gitDir, _ := wtsync.GitDir(bump)
	if p.Result != syncrun.ResultHandedOver || p.PlanFile == nil || *p.PlanFile != wtsync.PlanPath(gitDir) ||
		p.SafetyRef == nil || p.Recovery == nil || !slices.Equal(p.UndoCommand, []string{"wt", "sync", "undo", "bump"}) {
		t.Errorf("participant %+v", p)
	}
}

// Nobody said yes: nothing rebased, and the object says why.
func TestSyncRunJSONSaysWhenNobodyConfirmed(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	opts := synccmdtest.NoAgents()
	opts.Confirm = func([]string) (bool, error) { return false, nil }
	r, _ := synccmdtest.SyncRunJSON(t, ctx, nil, opts)
	if r.Outcome != kit.OutcomeRefused || r.Error == nil {
		t.Fatalf("%+v", r)
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("the worktree moved")
	}
}

// A signal with nothing tracked in flight — undo, between one branch and the
// next — asks the verb which participants the signal caught changed: those
// are interrupted, the rest stand as recorded.
func TestJournalAsksWhatASignalCaughtChanged(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	j := syncrun.NewSyncRunJournal(&out, "sync undo")
	j.Join("one", "feat_wt/one", "/w/one", strings.Repeat("1", 40), nil)
	j.Join("two", "feat_wt/two", "/w/two", strings.Repeat("2", 40), nil)
	j.OnSignal(func(p *syncrun.SyncParticipant) bool { return p.Branch == "feat_wt/one" })
	j.Interrupted("", "interrupted")
	r := synccmdtest.DecodeSyncRun(t, out.Bytes())
	if r.Outcome != kit.OutcomeInterrupted || r.Worktrees[0].Result != syncrun.ResultInterrupted || r.Worktrees[0].Recovery == nil ||
		r.Worktrees[1].Result != syncrun.ResultNotRun {
		t.Errorf("%+v %+v", r.Worktrees[0], r.Worktrees[1])
	}
}
