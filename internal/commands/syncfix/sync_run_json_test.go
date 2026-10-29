package syncfix

import (
	"bytes"
	"slices"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

func syncResumeJSON(t *testing.T, ctx *kit.Context, work string, opts ResumeOptions) (syncrun.RunResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = syncrun.NewSyncRunJournal(&out, "sync resume")
	err := SyncResume(ctx, work, opts, &human)
	return synccmdtest.DecodeSyncRun(t, out.Bytes()), err
}

func syncUndoJSON(t *testing.T, ctx *kit.Context, work string, opts UndoOptions) (syncrun.RunResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = syncrun.NewSyncRunJournal(&out, "sync undo")
	err := SyncUndo(ctx, work, opts, &human)
	return synccmdtest.DecodeSyncRun(t, out.Bytes()), err
}

// Resume finishes the handover: rebased, with the run's safety ref.
func TestSyncResumeJSONReportsTheFinishedRebase(t *testing.T) {
	t.Parallel()
	ctx, bump, _, st := synccmdtest.HandedOver(t)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	r, err := syncResumeJSON(t, ctx, "bump", noResumeAgents())
	if err != nil || r.Outcome != kit.OutcomeDone || r.Command != "sync resume" || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	p := r.Worktrees[0]
	if p.Result != syncrun.ResultRebased || p.Work != "bump" || p.SafetyRef == nil || *p.SafetyRef != st.Safety ||
		p.Before == nil || *p.Before != st.OldTip || p.After == nil || *p.After == st.OldTip ||
		!slices.Equal(p.UndoCommand, []string{"wt", "sync", "undo", "bump"}) {
		t.Errorf("participant %+v", p)
	}
}

// A resume refused leaves the handover as it was, and says so.
func TestSyncResumeJSONReportsARefusal(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump, gitDir, st := synccmdtest.HandedOver(t)
	r, err := syncResumeJSON(t, ctx, "bump", noResumeAgents())
	if err == nil || r.Outcome != kit.OutcomeRefused || len(r.Worktrees) != 1 ||
		r.Worktrees[0].Result != syncrun.ResultRefused || r.Worktrees[0].Reason == nil {
		t.Fatalf("%v %+v", err, r)
	}
	assertUntouched(t, bump, gitDir, st)

	r, err = syncResumeJSON(t, ctx, "other", noResumeAgents())
	if err == nil || r.Outcome != kit.OutcomeRefused || r.Error == nil || len(r.Worktrees) != 0 {
		t.Fatalf("nothing to resume: %v %+v", err, r)
	}
}

// Undo reports each branch it put back, and a second undo finds it there.
func TestSyncUndoJSONReportsWhatItPutBack(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, true)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	var human bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &human); err != nil {
		t.Fatalf("%v\n%s", err, human.String())
	}
	rebased := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	r, err := syncUndoJSON(t, ctx, "bump", noAgentsUndo())
	if err != nil || r.Outcome != kit.OutcomeDone || r.Command != "sync undo" || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	p := r.Worktrees[0]
	if p.Result != syncrun.ResultUndone || *p.Before != rebased || *p.After != old || p.SafetyRef == nil || p.UndoCommand != nil {
		t.Errorf("participant %+v", p)
	}

	r, err = syncUndoJSON(t, ctx, "bump", noAgentsUndo())
	if err != nil || r.Outcome != kit.OutcomeDone || len(r.Worktrees) != 1 || r.Worktrees[0].Result != syncrun.ResultSkipped {
		t.Fatalf("second undo: %v %+v", err, r)
	}
}

// A rebase the person finished by hand is where resume starts: before is the
// branch as resume found it, not the tip the run started from.
func TestSyncResumeJSONStartsFromTheBranchAsFound(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump, _, st := synccmdtest.HandedOver(t)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	testkit.GitTry(t, bump, "-c", "core.editor=true", "rebase", "--continue")
	found := testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", st.Branch)
	if found == st.OldTip {
		t.Fatal("the fixture did not finish the rebase")
	}
	r, _ := syncResumeJSON(t, ctx, "bump", noResumeAgents())
	if len(r.Worktrees) != 1 || r.Worktrees[0].Before == nil || *r.Worktrees[0].Before != found {
		t.Fatalf("%+v", r)
	}
}

// A question answered no: nothing resumed, and the object says why.
func TestSyncResumeJSONSaysWhenNobodyConfirmed(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump, _, _ := synccmdtest.HandedOver(t)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	opts := noResumeAgents()
	opts.Agents = testkit.IdleIn(t, bump, "parked")
	opts.Confirm = func([]string) bool { return false }
	r, _ := syncResumeJSON(t, ctx, "bump", opts)
	if r.Outcome != kit.OutcomeRefused || r.Error == nil {
		t.Fatalf("%+v", r)
	}
}

// A forced undo keeps what it discarded as a run of its own: undo takes that
// back too, and the object says how.
func TestSyncUndoJSONForcedNamesTheWayBackToWhatItKept(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump, _, _ := synccmdtest.HandedOver(t)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	testkit.GitOut(t, bump, "commit", "-q", "-m", "resolved by hand")
	forced := noAgentsUndo()
	forced.Force = true
	r, err := syncUndoJSON(t, ctx, "bump", forced)
	if err != nil || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	if p := r.Worktrees[0]; p.Result != syncrun.ResultUndone || !slices.Equal(p.UndoCommand, []string{"wt", "sync", "undo", "bump"}) || p.Reason == nil {
		t.Errorf("participant %+v", p)
	}
}
