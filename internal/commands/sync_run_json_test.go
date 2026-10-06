package commands

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

func decodeSyncRun(t *testing.T, out []byte) SyncRunResult {
	t.Helper()
	validateJSON(t, "sync-run", out)
	var r SyncRunResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	return r
}

func syncRunJSON(t *testing.T, ctx *Context, works []string, opts RunOptions) (SyncRunResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = NewSyncRunJournal(&out, "sync run")
	err := SyncRun(ctx, works, opts, &human)
	return decodeSyncRun(t, out.Bytes()), err
}

func syncResumeJSON(t *testing.T, ctx *Context, work string, opts ResumeOptions) (SyncRunResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = NewSyncRunJournal(&out, "sync resume")
	err := SyncResume(ctx, work, opts, &human)
	return decodeSyncRun(t, out.Bytes()), err
}

func syncUndoJSON(t *testing.T, ctx *Context, work string, opts UndoOptions) (SyncRunResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = NewSyncRunJournal(&out, "sync undo")
	err := SyncUndo(ctx, work, opts, &human)
	return decodeSyncRun(t, out.Bytes()), err
}

// A finished rebase says where the old tip is pinned, what each deferred
// step did, and how to take it all back.
func TestSyncRunJSONReportsTheSafetyRefDeferredStepsAndUndo(t *testing.T) {
	ctx, bump := runFixture(t, true)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	r, err := syncRunJSON(t, ctx, []string{"bump"}, noAgents())
	if err != nil || r.Outcome != OutcomeDone || r.Command != "sync run" || r.Repo == nil || *r.Repo != ctx.Repo.MainRoot ||
		len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	p := r.Worktrees[0]
	if p.Result != ResultRebased || p.Before == nil || *p.Before != old || p.After == nil || *p.After != gitOut(t, bump, "rev-parse", "HEAD") {
		t.Errorf("participant %+v", p)
	}
	if p.SafetyRef == nil || *p.SafetyRef != wtsync.SafetyRef("feat_wt/bump", 99) {
		t.Errorf("safety ref %v", p.SafetyRef)
	}
	if !slices.Equal(p.UndoCommand, []string{"wt", "sync", "undo", "bump"}) {
		t.Errorf("undo %q", p.UndoCommand)
	}
	if len(p.Deferred) != 1 || p.Deferred[0].Step != "cp v.txt gen.txt" || p.Deferred[0].Result != DeferredCommitted ||
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
	ctx, bump := runFixture(t, false)
	token := *overviewJSON(t, ctx).Token
	stackFixture(t, ctx)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	opts := noAgents()
	opts.Expect = token
	for _, works := range [][]string{nil, {"bump"}} {
		r, err := syncRunJSON(t, ctx, works, opts)
		if err == nil || r.Outcome != OutcomeRefused || r.Error == nil || len(r.Worktrees) != 0 {
			t.Fatalf("%v: a grown stack is refused before any participant: %v %+v", works, err, r)
		}
		if gitOut(t, bump, "rev-parse", "HEAD") != old {
			t.Fatal("the worktree moved")
		}
	}
	opts.Expect = *overviewJSON(t, ctx).Token
	if r, err := syncRunJSON(t, ctx, nil, opts); err != nil || r.Outcome != OutcomeDone || len(r.Worktrees) != 2 {
		t.Fatalf("the overview as read runs: %v %+v", err, r)
	}
}

// A contested stop handed over: its plan, its safety ref and both ways on.
func TestSyncRunJSONReportsAHandover(t *testing.T) {
	ctx, bump := contestedFixture(t)
	r, err := syncRunJSON(t, ctx, []string{"bump"}, noAgents())
	if err == nil || r.Outcome != OutcomePartial || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	p := r.Worktrees[0]
	gitDir, _ := wtsync.GitDir(bump)
	if p.Result != ResultHandedOver || p.PlanFile == nil || *p.PlanFile != wtsync.PlanPath(gitDir) ||
		p.SafetyRef == nil || p.Recovery == nil || !slices.Equal(p.UndoCommand, []string{"wt", "sync", "undo", "bump"}) {
		t.Errorf("participant %+v", p)
	}
}

// Nobody said yes: nothing rebased, and the object says why.
func TestSyncRunJSONSaysWhenNobodyConfirmed(t *testing.T) {
	ctx, bump := runFixture(t, false)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	opts := noAgents()
	opts.Confirm = func([]string) (bool, error) { return false, nil }
	r, _ := syncRunJSON(t, ctx, nil, opts)
	if r.Outcome != OutcomeRefused || r.Error == nil {
		t.Fatalf("%+v", r)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("the worktree moved")
	}
}

// Resume finishes the handover: rebased, with the run's safety ref.
func TestSyncResumeJSONReportsTheFinishedRebase(t *testing.T) {
	ctx, bump, _, st := handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	r, err := syncResumeJSON(t, ctx, "bump", noResumeAgents())
	if err != nil || r.Outcome != OutcomeDone || r.Command != "sync resume" || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	p := r.Worktrees[0]
	if p.Result != ResultRebased || p.Work != "bump" || p.SafetyRef == nil || *p.SafetyRef != st.Safety ||
		p.Before == nil || *p.Before != st.OldTip || p.After == nil || *p.After == st.OldTip ||
		!slices.Equal(p.UndoCommand, []string{"wt", "sync", "undo", "bump"}) {
		t.Errorf("participant %+v", p)
	}
}

// A resume refused leaves the handover as it was, and says so.
func TestSyncResumeJSONReportsARefusal(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	r, err := syncResumeJSON(t, ctx, "bump", noResumeAgents())
	if err == nil || r.Outcome != OutcomeRefused || len(r.Worktrees) != 1 ||
		r.Worktrees[0].Result != ResultRefused || r.Worktrees[0].Reason == nil {
		t.Fatalf("%v %+v", err, r)
	}
	assertUntouched(t, bump, gitDir, st)

	r, err = syncResumeJSON(t, ctx, "other", noResumeAgents())
	if err == nil || r.Outcome != OutcomeRefused || r.Error == nil || len(r.Worktrees) != 0 {
		t.Fatalf("nothing to resume: %v %+v", err, r)
	}
}

// Undo reports each branch it put back, and a second undo finds it there.
func TestSyncUndoJSONReportsWhatItPutBack(t *testing.T) {
	ctx, bump := runFixture(t, true)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var human bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &human); err != nil {
		t.Fatalf("%v\n%s", err, human.String())
	}
	rebased := gitOut(t, bump, "rev-parse", "HEAD")
	r, err := syncUndoJSON(t, ctx, "bump", noAgentsUndo())
	if err != nil || r.Outcome != OutcomeDone || r.Command != "sync undo" || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	p := r.Worktrees[0]
	if p.Result != ResultUndone || *p.Before != rebased || *p.After != old || p.SafetyRef == nil || p.UndoCommand != nil {
		t.Errorf("participant %+v", p)
	}

	r, err = syncUndoJSON(t, ctx, "bump", noAgentsUndo())
	if err != nil || r.Outcome != OutcomeDone || len(r.Worktrees) != 1 || r.Worktrees[0].Result != ResultSkipped {
		t.Fatalf("second undo: %v %+v", err, r)
	}
}

// A rebase the person finished by hand is where resume starts: before is the
// branch as resume found it, not the tip the run started from.
func TestSyncResumeJSONStartsFromTheBranchAsFound(t *testing.T) {
	ctx, bump, _, st := handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	gitTry(t, bump, "-c", "core.editor=true", "rebase", "--continue")
	found := gitOut(t, ctx.Repo.MainRoot, "rev-parse", st.Branch)
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
	ctx, bump, _, _ := handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	opts := noResumeAgents()
	opts.Agents = idleIn(t, bump, "parked")
	opts.Confirm = func([]string) (bool, error) { return false, nil }
	r, _ := syncResumeJSON(t, ctx, "bump", opts)
	if r.Outcome != OutcomeRefused || r.Error == nil {
		t.Fatalf("%+v", r)
	}
}

// A forced undo keeps what it discarded as a run of its own: undo takes that
// back too, and the object says how.
func TestSyncUndoJSONForcedNamesTheWayBackToWhatItKept(t *testing.T) {
	ctx, bump, _, _ := handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	gitOut(t, bump, "commit", "-q", "-m", "resolved by hand")
	forced := noAgentsUndo()
	forced.Force = true
	r, err := syncUndoJSON(t, ctx, "bump", forced)
	if err != nil || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	if p := r.Worktrees[0]; p.Result != ResultUndone || !slices.Equal(p.UndoCommand, []string{"wt", "sync", "undo", "bump"}) || p.Reason == nil {
		t.Errorf("participant %+v", p)
	}
}

// A signal with nothing tracked in flight — undo, between one branch and the
// next — asks the verb which participants the signal caught changed: those
// are interrupted, the rest stand as recorded.
func TestJournalAsksWhatASignalCaughtChanged(t *testing.T) {
	var out bytes.Buffer
	j := NewSyncRunJournal(&out, "sync undo")
	j.join("one", "feat_wt/one", "/w/one", strings.Repeat("1", 40), nil)
	j.join("two", "feat_wt/two", "/w/two", strings.Repeat("2", 40), nil)
	j.onSignal(func(p *SyncParticipant) bool { return p.Branch == "feat_wt/one" })
	j.interrupted("", "interrupted")
	r := decodeSyncRun(t, out.Bytes())
	if r.Outcome != OutcomeInterrupted || r.Worktrees[0].Result != ResultInterrupted || r.Worktrees[0].Recovery == nil ||
		r.Worktrees[1].Result != ResultNotRun {
		t.Errorf("%+v %+v", r.Worktrees[0], r.Worktrees[1])
	}
}
