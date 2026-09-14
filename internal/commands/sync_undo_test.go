package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

func noAgentsUndo() UndoOptions {
	return UndoOptions{verbOptions: verbOptions{
		Agents: []wtsync.Agent{},
		Now:    func() time.Time { return time.Unix(0, 100) },
	}}
}

func TestSyncUndoPutsBackWhatSyncRunMoved(t *testing.T) {
	ctx, bump := runFixture(t, true)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "log", "-1", "--format=%s") != "chore: regen" {
		t.Fatal("fixture did not produce the regeneration commit; the test is vacuous")
	}

	var undoOut bytes.Buffer
	if err := SyncUndo(ctx, "bump", noAgentsUndo(), &undoOut); err != nil {
		t.Fatalf("err %v\n%s", err, undoOut.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD not restored to the pre-run tip")
	}
	if gitOut(t, bump, "log", "-1", "--format=%s") == "chore: regen" {
		t.Fatal("regeneration commit still present after undo")
	}
	if !strings.Contains(undoOut.String(), "bump") || !strings.Contains(undoOut.String(), "→") {
		t.Fatalf("out %s", undoOut.String())
	}

	undoOut.Reset()
	if err := SyncUndo(ctx, "bump", noAgentsUndo(), &undoOut); err != nil {
		t.Fatalf("second undo err %v\n%s", err, undoOut.String())
	}
	if !strings.Contains(undoOut.String(), "already at") {
		t.Fatalf("second undo out %s", undoOut.String())
	}
}

func TestSyncUndoRefusesAWorktreeCommittedToSinceTheRun(t *testing.T) {
	ctx, bump := runFixture(t, true)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	gitIn(t, bump, "commit", "-q", "--allow-empty", "-m", "after the run")
	after := gitOut(t, bump, "rev-parse", "HEAD")

	var undoOut bytes.Buffer
	err := SyncUndo(ctx, "bump", noAgentsUndo(), &undoOut)
	if err == nil || !strings.Contains(err.Error(), "moved since that run") {
		t.Fatalf("err %v\n%s", err, undoOut.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != after {
		t.Fatal("HEAD moved despite the refusal")
	}

	forced := noAgentsUndo()
	forced.Force = true
	undoOut.Reset()
	if err := SyncUndo(ctx, "bump", forced, &undoOut); err != nil {
		t.Fatalf("forced undo err %v\n%s", err, undoOut.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") == after {
		t.Fatal("forced undo did not rewind")
	}
	if gitOut(t, ctx.Repo.MainRoot, "rev-parse", wtsync.SafetyPrefix+"feat_wt/bump/100") != after {
		t.Fatal("forced undo did not pin the tip it discarded")
	}
}

// A handed-over rebase finished with git rebase --continue is the run's own
// rebase, finished by hand: a plain undo names it as that and points at
// resume, rather than calling the rebased branch one that "moved since"
// the run. --force rewinds and pins it, as for any moved branch.
func TestSyncUndoNamesAFinishedByHandRebaseInsteadOfCallingItMoved(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	gitTry(t, bump, "rebase", "--continue")
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; the hand continue did not finish", busy, err)
	}
	finished := gitOut(t, bump, "rev-parse", "HEAD")

	var out bytes.Buffer
	err := SyncUndo(ctx, "bump", noAgentsUndo(), &out)
	if err == nil || !strings.Contains(err.Error(), "finished by hand") || !strings.Contains(err.Error(), "wt sync resume bump") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if strings.Contains(err.Error(), "moved since") {
		t.Fatalf("the run's own rebase is called a branch that moved: %v", err)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != finished {
		t.Fatal("HEAD moved despite the refusal")
	}
	if has, herr := wtsync.HasPlan(gitDir); herr != nil || !has {
		t.Fatalf("HasPlan = %v, %v; the refusal removed the handover", has, herr)
	}

	forced := noAgentsUndo()
	forced.Force = true
	out.Reset()
	if err := SyncUndo(ctx, "bump", forced, &out); err != nil {
		t.Fatalf("forced undo err %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != st.OldTip {
		t.Fatal("the forced undo did not rewind to the run's old tip")
	}
	if gitOut(t, ctx.Repo.MainRoot, "rev-parse", wtsync.SafetyPrefix+"feat_wt/bump/100") != finished {
		t.Fatal("the forced undo did not pin the finished rebase")
	}
}

// The plan file tells a person wt sync undo puts everything back. It has to,
// on the worktree exactly as the run left it: the rebase in progress, the
// strategies' answers staged, and the run's lock still in the git dir.
func TestSyncUndoAbortsWhatSyncRunHandedOver(t *testing.T) {
	ctx, bump := contestedFixture(t)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	gitDir, _ := handOverNow(t, ctx, bump)
	if _, ok, err := wtsync.ReadLock(gitDir); err != nil || !ok {
		t.Fatalf("ReadLock = %v, %v; the run kept no lock and the test is vacuous", ok, err)
	}

	var out bytes.Buffer
	if err := SyncUndo(ctx, "bump", noAgentsUndo(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "bump  aborted the rebase; back at "+git.ShortID(old, 7)) {
		t.Fatalf("out %s", out.String())
	}
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; undo left the rebase in place", busy, err)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD is not back at the pre-run tip")
	}
	if has, err := wtsync.HasPlan(gitDir); err != nil || has {
		t.Fatalf("HasPlan = %v, %v; undo ends the handover", has, err)
	}
	if _, ok, err := wtsync.ReadLock(gitDir); err != nil || ok {
		t.Fatalf("ReadLock = %v, %v; the run's lock outlived the undo", ok, err)
	}
}

// A rebase a person restarted from their own commit carries the run's
// sidecar but is not the run's rebase. resume refuses it; undo must too,
// rather than abort it, because the abort would discard that commit and
// nothing would pin it.
func TestSyncUndoRefusesARebaseItDidNotLeave(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	restartedByHand(t, bump, st)
	main := ctx.Repo.MainRoot
	moved := gitOut(t, main, "rev-parse", st.Branch)
	if moved == st.OldTip {
		t.Fatal("the branch did not move; the test is vacuous")
	}
	for _, force := range []bool{true, false} {
		opts := noAgentsUndo()
		opts.Force = force
		var out bytes.Buffer
		err := SyncUndo(ctx, "bump", opts, &out)
		if err == nil || !strings.Contains(err.Error(), "not the one wt sync run left") || !strings.Contains(err.Error(), "rebase --abort") {
			t.Fatalf("force=%v: err %v\n%s", force, err, out.String())
		}
		// Not assertUntouched: the refusal comes after undo took the
		// handover's lock over, so the lock is displaced (the sidecar, not
		// the lock, is the durable marker). The rebase and the sidecar are
		// what the refusal must leave alone.
		if busy, err := wtsync.RebaseInProgress(bump); err != nil || !busy {
			t.Fatalf("force=%v: RebaseInProgress = %v, %v; the refusal aborted the rebase", force, busy, err)
		}
		if _, ok, err := wtsync.ReadState(gitDir); err != nil || !ok {
			t.Fatalf("force=%v: ReadState = %v, %v; the sidecar is gone", force, ok, err)
		}
		if got := gitOut(t, main, "rev-parse", st.Branch); got != moved {
			t.Fatalf("force=%v: %s is at %s, want %s", force, st.Branch, got, moved)
		}
	}
}

// A person who resolves the handed-over stop and commits it themselves has
// a commit on detached HEAD that only the rebase knows about. undo must
// name it and both ways of keeping it, and touch nothing.
func TestSyncUndoNamesTheCommitAPersonMadeInsideTheHandover(t *testing.T) {
	ctx, bump, gitDir, _ := handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	gitOut(t, bump, "commit", "-q", "-m", "resolved by hand")
	sha := gitOut(t, bump, "rev-parse", "HEAD")

	var out bytes.Buffer
	err := SyncUndo(ctx, "bump", noAgentsUndo(), &out)
	if err == nil || !strings.Contains(err.Error(), git.ShortID(sha, 7)) || !strings.Contains(err.Error(), "wt sync resume bump") || !strings.Contains(err.Error(), "wt sync undo --force bump") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	// Not assertUntouched: the lock is displaced by the refusal (see
	// TestSyncUndoRefusesARebaseItDidNotLeave); the rebase, the sidecar and
	// the commit are what must survive.
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the refusal aborted the rebase", busy, err)
	}
	if _, ok, err := wtsync.ReadState(gitDir); err != nil || !ok {
		t.Fatalf("ReadState = %v, %v; the sidecar is gone", ok, err)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != sha {
		t.Fatal("HEAD moved despite the refusal")
	}
}

// The sidecar records where the rebase's HEAD was when the run handed
// over, so undo can tell a commit made inside the rebase from the run's
// own picks exactly, not by counting.
func TestSyncHandoverRecordsHead(t *testing.T) {
	ctx, bump := contestedFixture(t)
	_, st := handOverNow(t, ctx, bump)
	if head := gitOut(t, bump, "rev-parse", "HEAD"); st.Head == "" || st.Head != head {
		t.Fatalf("sidecar head %q, HEAD %s", st.Head, head)
	}
}

// An undo that stopped after aborting a handover it still had to rewind
// must not print that rewind as done.
func TestRestoredLineNeverClaimsARewindThatDidNotHappen(t *testing.T) {
	r := wtsync.Restored{
		Branch: "feat_wt/bump", From: strings.Repeat("a", 40), To: strings.Repeat("b", 40),
		Ref: "refs/wt-sync/feat_wt/bump/99", Aborted: true, NotRewound: true,
	}
	line := restoredLine("bump", r)
	if strings.Contains(line, "→") || !strings.Contains(line, "not rewound") || !strings.Contains(line, "still at "+git.ShortID(r.From, 7)) {
		t.Fatalf("line %q claims a rewind or hides where the branch is", line)
	}
}

// A forced undo of a handed-over branch that moved since the run does more
// than abort: it rewinds past the moved commits, and the output has to say
// from where.
func TestSyncUndoForcedPastAMovedHandoverSaysWhatItRewound(t *testing.T) {
	ctx, bump := contestedFixture(t)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	handOverNow(t, ctx, bump)
	main := ctx.Repo.MainRoot
	gitOut(t, main, "update-ref", "refs/heads/feat_wt/bump", "main")
	moved := gitOut(t, main, "rev-parse", "feat_wt/bump")
	if moved == old {
		t.Fatal("the branch did not move; the test is vacuous")
	}

	forced := noAgentsUndo()
	forced.Force = true
	var out bytes.Buffer
	if err := SyncUndo(ctx, "bump", forced, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if want := "bump  aborted the rebase; " + git.ShortID(moved, 7) + " → " + git.ShortID(old, 7); !strings.Contains(out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, out.String())
	}
	if want := git.ShortID(moved, 7) + " kept under refs/wt-sync/feat_wt/bump/100, wt sync undo bump puts it back"; !strings.Contains(out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD is not back at the pre-run tip")
	}
	if gitOut(t, main, "rev-parse", wtsync.SafetyPrefix+"feat_wt/bump/100") != moved {
		t.Fatal("the forced undo did not pin the tip it discarded")
	}
}

// A forced undo past a commit made inside the handover leaves the branch
// where it was, so the row's "back at" names nothing of what was kept. The
// row has to say where the commit went and what brings it back.
func TestSyncUndoForcedPastACommitInsideSaysWhereItKeptIt(t *testing.T) {
	ctx, bump, _, _ := handedOver(t)
	old := gitOut(t, ctx.Repo.MainRoot, "rev-parse", "feat_wt/bump")
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	gitOut(t, bump, "commit", "-q", "-m", "resolved by hand")
	sha := gitOut(t, bump, "rev-parse", "HEAD")

	forced := noAgentsUndo()
	forced.Force = true
	var out bytes.Buffer
	if err := SyncUndo(ctx, "bump", forced, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	want := "bump  aborted the rebase; back at " + git.ShortID(old, 7) + "; " + git.ShortID(sha, 7) + " kept under refs/wt-sync/feat_wt/bump/100, wt sync undo bump puts it back"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, out.String())
	}
	if gitOut(t, ctx.Repo.MainRoot, "rev-parse", "refs/wt-sync/feat_wt/bump/100") != sha {
		t.Fatal("the ref the row names does not pin the commit")
	}
}

func TestSyncUndoUnderAnIdleSessionAsksThenTellsIt(t *testing.T) {
	ctx, bump := runFixture(t, false)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	rebased := gitOut(t, bump, "rev-parse", "HEAD")

	opts := noAgentsUndo()
	opts.Agents = idleIn(t, bump, "bump-1")
	var asked []string
	opts.Confirm = func(works []string) (bool, error) { asked = works; return false, nil }
	var no bytes.Buffer
	if err := SyncUndo(ctx, "bump", opts, &no); err != nil {
		t.Fatalf("err %v\n%s", err, no.String())
	}
	if len(asked) != 1 || asked[0] != "bump" || !strings.Contains(no.String(), "nothing undone") ||
		!strings.Contains(no.String(), "⚠ bump: session bump-1 (idle) is in it") {
		t.Fatalf("asked %v\n%s", asked, no.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != rebased {
		t.Fatal("HEAD moved after no")
	}

	opts.Confirm = func([]string) (bool, error) { return true, nil }
	var yes bytes.Buffer
	if err := SyncUndo(ctx, "bump", opts, &yes); err != nil {
		t.Fatalf("err %v\n%s", err, yes.String())
	}
	want := "⚠ tell bump-1, idle in it:\n    wt: bump undone, back at " + old[:7] + "\n"
	if !strings.Contains(yes.String(), want) {
		t.Fatalf("no relay line %q:\n%s", want, yes.String())
	}
}

// A no to undo on a handed-over worktree must leave the handover's own lock:
// the question comes before any lock is taken over.
func TestSyncUndoNoLeavesAHandoverAndItsLock(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	opts := noAgentsUndo()
	opts.Agents = idleIn(t, bump, "bump-1")
	opts.Confirm = func([]string) (bool, error) { return false, nil }
	var out bytes.Buffer
	if err := SyncUndo(ctx, "bump", opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if has, _ := wtsync.HasPlan(gitDir); !has {
		t.Fatal("the handover is gone after no")
	}
	if l, ok, err := wtsync.ReadLock(gitDir); err != nil || !ok || l.PID != st.Lock.PID || l.Started.Unix() != st.Lock.Started {
		t.Fatalf("lock %+v %v %v; a no must leave the run's lock alone", l, ok, err)
	}
}

func TestSyncUndoRefusesASessionThatWokeWhileAsked(t *testing.T) {
	ctx, bump := runFixture(t, false)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	rebased := gitOut(t, bump, "rev-parse", "HEAD")
	opts := noAgentsUndo()
	opts.Agents = idleIn(t, bump, "bump-1")
	opts.Confirm = func([]string) (bool, error) { return true, nil }
	woke := idleIn(t, bump, "bump-1")
	woke[0].Status = "busy"
	opts.Relist = func() ([]wtsync.Agent, error) { return woke, nil }
	var undoOut bytes.Buffer
	if err := SyncUndo(ctx, "bump", opts, &undoOut); err == nil || !strings.Contains(err.Error(), "busy in it now: bump-1") {
		t.Fatalf("err %v\n%s", err, undoOut.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != rebased {
		t.Fatal("HEAD moved")
	}
}
