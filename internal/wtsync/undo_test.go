package wtsync

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// completed pins the result ref a finished run would write: the tip the
// branch was left at once its rebase and deferred steps were done. Rebase
// alone does not write one; SyncRun does, after the deferred steps.
func completed(t *testing.T, mainRoot, wtPath, branch string, epoch int64) {
	t.Helper()
	if err := WriteResult(mainRoot, branch, gitIn(t, wtPath, "rev-parse", "HEAD"), epoch); err != nil {
		t.Fatal(err)
	}
}

func TestUndoResetsEveryBranchOfTheNewestEpoch(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") == old {
		t.Fatal("rebase did nothing; the test is vacuous")
	}
	completed(t, dir, wt, "feature", 5)
	got, err := Undo(dir, []repo.Worktree{{Path: dir, Branch: "main", IsMain: true}, {Path: wt, Branch: "feature"}}, nil, "feature", time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].To != old || got[0].Path != wt {
		t.Fatalf("restored %+v", got)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old || gitIn(t, wt, "status", "--porcelain") != "" {
		t.Fatal("not restored cleanly")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".git", "worktrees", "*", LockName+"*")); len(left) != 0 {
		t.Fatalf("lock left behind: %v", left)
	}
}

func TestUndoRestoresTwoBranchesThatShareAnEpoch(t *testing.T) {
	// Two safety refs written by one run (same epoch) on two branches; both
	// branches then moved; undoing either restores both.
	dir, wt, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	gitIn(t, dir, "branch", "second", "feature")
	oldF, oldS := gitIn(t, dir, "rev-parse", "feature"), gitIn(t, dir, "rev-parse", "second")
	for _, b := range []string{"feature", "second"} {
		if _, err := WriteSafety(dir, b, gitIn(t, dir, "rev-parse", b), 77); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "moved")
	gitIn(t, dir, "update-ref", "refs/heads/second", "main")
	for _, b := range []string{"feature", "second"} {
		if err := WriteResult(dir, b, gitIn(t, dir, "rev-parse", b), 77); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "second", time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || gitIn(t, dir, "rev-parse", "feature") != oldF || gitIn(t, dir, "rev-parse", "second") != oldS {
		t.Fatalf("restored %+v; feature %s second %s", got, gitIn(t, dir, "rev-parse", "feature"), gitIn(t, dir, "rev-parse", "second"))
	}
}

func TestUndoRefusesADirtyCheckoutBeforeTouchingAnything(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	after := gitIn(t, wt, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "tracked changes") {
		t.Fatalf("err %v", err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != after {
		t.Fatal("HEAD moved despite the refusal")
	}
}

func TestUndoRefusesACheckoutWithAnAgent(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(wt)
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, []Agent{{Name: "f-1", Cwd: resolved}}, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "f-1") {
		t.Fatalf("err %v", err)
	}
}

func TestUndoRestoresTheMainCheckoutLikeAnyOther(t *testing.T) {
	// The main checkout sits on a branch a run pinned; undo resets it there
	// (with the dirt check), never through update-ref.
	dir, _, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, dir, "rev-parse", "main")
	if _, err := WriteSafety(dir, "main", old, 3); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "moved")
	if err := WriteResult(dir, "main", gitIn(t, dir, "rev-parse", "main"), 3); err != nil {
		t.Fatal(err)
	}
	got, err := Undo(dir, []repo.Worktree{{Path: dir, Branch: "main", IsMain: true}}, nil, "main", time.Now(), false)
	if err != nil || len(got) != 1 || got[0].Path != dir || gitIn(t, dir, "rev-parse", "HEAD") != old {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestUndoWithoutARunIsAnError(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, nil)
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "no run to undo") {
		t.Fatalf("err %v", err)
	}
}

func TestUndoRestoresABranchWithNoCheckoutThroughUpdateRef(t *testing.T) {
	dir, _, _ := runRepo(t, nil, nil)
	gitIn(t, dir, "branch", "loose", "main")
	old := gitIn(t, dir, "rev-parse", "loose")
	if _, err := WriteSafety(dir, "loose", old, 4); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "update-ref", "refs/heads/loose", "feature")
	if err := WriteResult(dir, "loose", gitIn(t, dir, "rev-parse", "loose"), 4); err != nil {
		t.Fatal(err)
	}
	got, err := Undo(dir, nil, nil, "loose", time.Now(), false)
	if err != nil || len(got) != 1 || got[0].Path != "" || gitIn(t, dir, "rev-parse", "loose") != old {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestUndoASecondTimeIsANoOp(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, wt, "feature", 5)
	wts := []repo.Worktree{{Path: wt, Branch: "feature"}}
	if _, err := Undo(dir, wts, nil, "feature", time.Now(), false); err != nil {
		t.Fatal(err)
	}
	got, err := Undo(dir, wts, nil, "feature", time.Now(), false)
	if err != nil || len(got) != 1 || got[0].From != old || got[0].To != old {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestUndoRefusesABranchThatMovedAfterTheRun(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, wt, "feature", 5)
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "after the run")
	after := gitIn(t, wt, "rev-parse", "HEAD")
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "moved since that run") {
		t.Fatalf("err %v", err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != after {
		t.Fatal("HEAD moved despite the refusal")
	}
}

func TestUndoForcedPastAMovedBranchPinsWhatItDiscards(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, wt, "feature", 5)
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "after the run")
	discarded := gitIn(t, wt, "rev-parse", "HEAD")
	now := time.Unix(0, 1234)
	got, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "feature", now, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatalf("got %+v; HEAD %s want %s", got, gitIn(t, wt, "rev-parse", "HEAD"), old)
	}
	if pinned := gitIn(t, dir, "rev-parse", SafetyPrefix+"feature/1234"); pinned != discarded {
		t.Fatalf("fresh safety ref pins %s, want %s", pinned, discarded)
	}
	if got[0].Kept != SafetyPrefix+"feature/1234" || got[0].KeptTip != discarded {
		t.Fatalf("row %+v; want the fresh safety ref and what it pins named", got[0])
	}
}

func TestUndoRefusesABranchWithALaterRun(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, wt, "feature", 5)
	// A second run of the same branch, pinned at where the first left it.
	if _, err := WriteSafety(dir, "feature", gitIn(t, wt, "rev-parse", "HEAD"), 9); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, wt, "feature", 9)
	// Ask for the older run by name: the newest is what Undo picks, so drive
	// it through the older epoch's ref by pinning a sibling branch to it.
	gitIn(t, dir, "branch", "sibling", "main")
	if _, err := WriteSafety(dir, "sibling", gitIn(t, dir, "rev-parse", "sibling"), 5); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, dir, "sibling", 5)
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "sibling", time.Now(), true)
	if err == nil || !strings.Contains(err.Error(), "feature has a later run") {
		t.Fatalf("err %v", err)
	}
}

func TestUndoForcedLeavesNoSafetyRefWhenALaterBranchRefuses(t *testing.T) {
	// One run pinned two branches; the second has a later run of its own, so
	// the whole undo is refused. The first branch had moved and would have
	// been force-pinned: that pin must not survive the refusal, or every
	// later plain undo reports "already at" and the run is stranded.
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, wt, "feature", 5)
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "after the run")

	gitIn(t, dir, "branch", "second", "main")
	if _, err := WriteSafety(dir, "second", gitIn(t, dir, "rev-parse", "second"), 5); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, dir, "second", 5)
	// second's later run, which is what refuses the undo.
	if _, err := WriteSafety(dir, "second", gitIn(t, dir, "rev-parse", "second"), 8); err != nil {
		t.Fatal(err)
	}

	before, err := ListSafety(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "feature", time.Unix(0, 4321), true)
	if err == nil || !strings.Contains(err.Error(), "later run") {
		t.Fatalf("err %v", err)
	}
	after, err := ListSafety(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("the refusal left %d safety refs, was %d", len(after), len(before))
	}
	if out := gitIn(t, dir, "for-each-ref", "--format=%(refname)", SafetyRef("feature", 4321)); out != "" {
		t.Fatalf("a forced pin survived the refusal: %s", out)
	}
}

func TestAForcedUndoIsItselfUndoneByAPlainUndo(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, wt, "feature", 5)
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "after the run")
	discarded := gitIn(t, wt, "rev-parse", "HEAD")

	wts := []repo.Worktree{{Path: wt, Branch: "feature"}}
	if _, err := Undo(dir, wts, nil, "feature", time.Unix(0, 4321), true); err != nil {
		t.Fatal(err)
	}
	if got := gitIn(t, wt, "rev-parse", "HEAD"); got != old {
		t.Fatalf("forced undo left HEAD at %s, want %s", got, old)
	}
	// The plain undo puts back what the forced one discarded.
	got, err := Undo(dir, wts, nil, "feature", time.Now(), false)
	if err != nil {
		t.Fatalf("a forced undo must be undoable without --force: %v", err)
	}
	if len(got) != 1 || gitIn(t, wt, "rev-parse", "HEAD") != discarded {
		t.Fatalf("got %+v; HEAD %s want %s", got, gitIn(t, wt, "rev-parse", "HEAD"), discarded)
	}
}

// handedOverRepo is runRepo left the way a run leaves a stop nobody's
// strategy claims: the rebase in progress in the worktree, v.txt staged by
// owned-line and a.txt a person's, and the plan file and sidecar written the
// way the run's handover writes them. keepLock also takes a lock, records it
// in the sidecar and keeps it, as a handover keeps the run's. old is the
// branch tip before the run.
func handedOverRepo(t *testing.T, epoch int64, keepLock bool) (dir, wt, gitDir, old string) {
	t.Helper()
	return handedOverRepoWith(t, epoch, keepLock,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
}

// handedOverRepoWith is handedOverRepo with trunk's and the branch's
// commits chosen by the caller; the rebase must stop at a file nobody's
// strategy claims, or there is nothing to hand over.
func handedOverRepoWith(t *testing.T, epoch int64, keepLock bool, trunkEdits, branchEdits []map[string]string) (dir, wt, gitDir, old string) {
	t.Helper()
	dir, wt, cfg := runRepo(t, trunkEdits, branchEdits)
	old = gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, epoch), nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v; want a handover", res, err)
	}
	gitDir, err = GitDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	st := State{
		Branch: "feature", Work: "feature", Trunk: gitIn(t, dir, "rev-parse", "origin/main"), TrunkRef: "origin/main",
		Onto: "origin/main", Epoch: epoch, Safety: res.Safety.Ref, OldTip: res.OldTip,
		Head: gitIn(t, wt, "rev-parse", "HEAD"),
		Stop: res.Left.Index, Total: res.Left.Total,
		Resolved: res.Left.Staged, Strategy: map[string]string{},
		Deleted: res.Left.Deleted, Left: res.Left.Left,
	}
	for _, f := range res.Left.Files {
		if f.Resolved {
			st.Strategy[f.Path] = f.Strategy
		}
	}
	var lock *Lock
	if keepLock {
		if lock, err = Acquire(gitDir, time.Now()); err != nil {
			t.Fatal(err)
		}
		st.Lock = LeftLock{PID: lock.PID, Started: lock.Started.Unix()}
	}
	if err := WritePlanFile(gitDir, "# feature needs you\n"); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(gitDir, st); err != nil {
		t.Fatal(err)
	}
	if lock != nil {
		lock.Keep()
	}
	return dir, wt, gitDir, old
}

func TestUndoAbortsAHandedOverRebase(t *testing.T) {
	dir, wt, gitDir, old := handedOverRepo(t, 5, false)
	got, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}, nil, "feature", time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if busy, err := RebaseInProgress(wt); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; undo left the rebase in place", busy, err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old || gitIn(t, wt, "symbolic-ref", "HEAD") != "refs/heads/feature" {
		t.Fatal("HEAD is not back on feature at the safety tip")
	}
	if gitIn(t, wt, "status", "--porcelain", "--untracked-files=no") != "" {
		t.Fatal("the abort left tracked changes")
	}
	if has, err := HasPlan(gitDir); err != nil || has {
		t.Fatalf("HasPlan = %v, %v; undo ends the handover", has, err)
	}
	if _, err := os.Stat(PlanPath(gitDir)); !os.IsNotExist(err) {
		t.Fatalf("the plan file survived the undo: %v", err)
	}
	if len(got) != 1 || !got[0].Aborted || got[0].To != old || got[0].Path != wt {
		t.Fatalf("restored %+v", got)
	}
}

func TestUndoStillRefusesAForeignRebase(t *testing.T) {
	// A rebase somebody started by hand carries no handover: undo has no
	// business discarding it.
	dir, wt, _ := runRepo(t, []map[string]string{{"a.txt": "trunk\n"}}, []map[string]string{{"a.txt": "branch\n"}})
	if _, err := WriteSafety(dir, "feature", gitIn(t, wt, "rev-parse", "HEAD"), 5); err != nil {
		t.Fatal(err)
	}
	if _, err := gittest.Try(t, wt, "rebase", "--no-update-refs", "--no-gpg-sign", "main"); err == nil {
		t.Fatal("rebase did not stop; the test is vacuous")
	}
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "mid-rebase") {
		t.Fatalf("err %v", err)
	}
	if busy, err := RebaseInProgress(wt); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the refusal aborted somebody's rebase", busy, err)
	}
}

func TestVerifyLeftAcceptsTheRunsOwnRebase(t *testing.T) {
	_, wt, gitDir, _ := handedOverRepo(t, 5, false)
	st, ok, err := ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	if err := VerifyLeft(wt, st); err != nil {
		t.Fatalf("the run's own rebase was refused: %v", err)
	}
}

func TestVerifyLeftRefusesARebaseFromAnotherTipOntoAnotherCommit(t *testing.T) {
	// The sequencer is the run's; the handover is made to disagree with it
	// on one thing at a time, and the refusal has to name that thing.
	dir, wt, gitDir, old := handedOverRepo(t, 5, false)
	st, ok, err := ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	base := gitIn(t, dir, "rev-parse", "origin/main~1")
	cases := []struct {
		name string
		edit func(*State)
		want string
	}{
		{"another tip", func(s *State) { s.OldTip = base }, "it started from " + old[:7]},
		{"another onto", func(s *State) { s.Onto = base }, "it replays onto"},
		{"another branch", func(s *State) { s.Branch = "other" }, "it moves refs/heads/feature, not refs/heads/other"},
	}
	for _, c := range cases {
		s := st
		c.edit(&s)
		err := VerifyLeft(wt, s)
		if err == nil || !strings.Contains(err.Error(), "not the one wt sync run left") || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err %v; want %q named", c.name, err, c.want)
		}
	}
}

func TestUndoRefusesARebaseThatIsNotTheRuns(t *testing.T) {
	// feature's handover was aborted by hand, given a commit and rebased
	// again by hand onto the run's onto, leaving the run's sidecar behind.
	// The rebase in progress started from that commit, not the run's tip:
	// aborting it would discard the commit and no pin covers it (the branch
	// ref never moved), so undo refuses with and without force.
	dir, wt, gitDir, _ := handedOverRepo(t, 5, false)
	gitIn(t, wt, "rebase", "--abort")
	if err := os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "c.txt")
	gitIn(t, wt, "commit", "-q", "-m", "by hand")
	moved := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := gittest.Try(t, wt, "rebase", "--no-update-refs", "--no-gpg-sign", "origin/main"); err == nil {
		t.Fatal("feature's rebase did not stop; the test is vacuous")
	}
	wts := []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}
	for _, force := range []bool{true, false} {
		_, err := Undo(dir, wts, nil, "feature", time.Now(), force)
		if err == nil || !strings.Contains(err.Error(), "not the one wt sync run left") || !strings.Contains(err.Error(), "rebase --abort") {
			t.Fatalf("force=%v: err %v; want the foreign rebase refused and the abort named", force, err)
		}
		if busy, err := RebaseInProgress(wt); err != nil || !busy {
			t.Fatalf("force=%v: RebaseInProgress = %v, %v; the refusal aborted somebody's rebase", force, busy, err)
		}
		if has, err := HasPlan(gitDir); err != nil || !has {
			t.Fatalf("force=%v: HasPlan = %v, %v; the refusal removed the handover", force, has, err)
		}
		if got := gitIn(t, dir, "rev-parse", "refs/heads/feature"); got != moved {
			t.Fatalf("force=%v: feature is at %s, want %s: the refusal moved it", force, got, moved)
		}
	}
}

// resolveAndCommit does what a person may do at a handed-over stop instead
// of leaving the resolution staged: resolves a.txt and commits it, inside
// the rebase, on detached HEAD. The commit is theirs and nothing but HEAD's
// reflog names it once the rebase is aborted.
func resolveAndCommit(t *testing.T, wt string) (sha string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("merged by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "--", "a.txt")
	gitIn(t, wt, "commit", "-q", "-m", "resolved by hand")
	return gitIn(t, wt, "rev-parse", "HEAD")
}

func TestCommittedInsideSeesACommitAtTheStop(t *testing.T) {
	_, wt, gitDir, _ := handedOverRepo(t, 5, false)
	st, ok, err := ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	left := gitIn(t, wt, "rev-parse", "HEAD")
	if st.Head != left {
		t.Fatalf("the fixture's sidecar records %q, HEAD is %s", st.Head, left)
	}
	if got, err := CommittedInside(wt, st); err != nil || len(got.Commits) != 0 || got.Unproven || got.Head != left {
		t.Fatalf("before any commit: %+v, %v", got, err)
	}
	sha := resolveAndCommit(t, wt)
	want := Inside{Head: sha, Commits: []Commit{{SHA: sha, Subject: "resolved by hand"}}}
	if got, err := CommittedInside(wt, st); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("by head: %+v, %v; want %+v", got, err, want)
	}
	// An old sidecar that recorded no head: the same commit, but unproven,
	// since nothing separates it from the run's own picks.
	older := st
	older.Head = ""
	want.Unproven = true
	if got, err := CommittedInside(wt, older); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("by count: %+v, %v; want %+v", got, err, want)
	}
	// Recorded at the commit itself, as a resume that stopped there would:
	// nothing is inside.
	st.Head = sha
	if got, err := CommittedInside(wt, st); err != nil || len(got.Commits) != 0 || got.Unproven {
		t.Fatalf("at the recorded head: %+v, %v", got, err)
	}
	// An amend of the recorded head is a sibling of it: reported as inside,
	// so a forced undo pins it rather than refusing.
	gitIn(t, wt, "commit", "-q", "--amend", "-m", "resolved by hand, amended")
	amended := gitIn(t, wt, "rev-parse", "HEAD")
	if got, err := CommittedInside(wt, st); err != nil || !reflect.DeepEqual(got, Inside{Head: amended, Commits: []Commit{{SHA: amended, Subject: "resolved by hand, amended"}}}) {
		t.Fatalf("after an amend: %+v, %v", got, err)
	}
	// HEAD reset to before the recorded head is behind the run's line.
	gitIn(t, wt, "reset", "-q", "--hard", left)
	if _, err := CommittedInside(wt, st); err == nil || !strings.Contains(err.Error(), "behind where the run left") {
		t.Fatalf("err %v; want a HEAD behind the run's line refused", err)
	}
}

// olderSidecar rewrites the handover in gitDir as a wt from before the head
// was recorded would have written it.
func olderSidecar(t *testing.T, gitDir string) {
	t.Helper()
	st, ok, err := ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	st.Head = ""
	if err := WriteState(gitDir, st); err != nil {
		t.Fatal(err)
	}
}

func TestUndoRefusesAnOldHandoverWithACommitBehindADroppedPick(t *testing.T) {
	// The branch's first commit makes a change trunk already made (with a
	// different patch id, so the rebase still tries it): the merge backend
	// drops it as empty and the stop is at 2/2 with HEAD still at onto. A
	// count of picks would then say the stop has one pick before it, and a
	// commit a person makes there would net to nothing. A sidecar with no
	// recorded head cannot prove otherwise, so a plain undo refuses and a
	// forced one pins HEAD.
	dir, wt, gitDir, old := handedOverRepoWith(t, 5, false,
		[]map[string]string{{"a.txt": "x\n", "c.txt": "c\n"}, {"b.txt": "trunk\n"}},
		[]map[string]string{{"a.txt": "x\n"}, {"b.txt": "branch\n"}})
	if p, err := RebaseProgress(wt); err != nil || p.Index != 2 {
		t.Fatalf("progress %+v, %v; want the stop at 2/2 with the first pick dropped", p, err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != gitIn(t, dir, "rev-parse", "origin/main") {
		t.Fatal("HEAD is not at onto; the first pick was not dropped and the test is vacuous")
	}
	olderSidecar(t, gitDir)
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("merged by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "--", "b.txt")
	gitIn(t, wt, "commit", "-q", "-m", "resolved by hand")
	sha := gitIn(t, wt, "rev-parse", "HEAD")

	wts := []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}
	_, err := Undo(dir, wts, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "did not record where it left HEAD") || !strings.Contains(err.Error(), "wt sync undo --force feature") || !strings.Contains(err.Error(), "1 commit on top") {
		t.Fatalf("err %v; want the old handover refused", err)
	}
	if busy, err := RebaseInProgress(wt); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the refusal aborted the rebase", busy, err)
	}
	got, err := Undo(dir, wts, nil, "feature", time.Unix(0, 4321), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Aborted || gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatalf("restored %+v; HEAD %s want %s", got, gitIn(t, wt, "rev-parse", "HEAD"), old)
	}
	if pinned := gitIn(t, dir, "rev-parse", SafetyRef("feature", 4321)); pinned != sha {
		t.Fatalf("the forced undo pinned %s, want the commit behind the dropped pick %s", pinned, sha)
	}
	if _, err := Undo(dir, wts, nil, "feature", time.Now(), false); err != nil || gitIn(t, wt, "rev-parse", "HEAD") != sha {
		t.Fatalf("plain undo of the forced undo: err %v, HEAD %s want %s", err, gitIn(t, wt, "rev-parse", "HEAD"), sha)
	}
}

func TestUndoRefusesAnOldHandoverEvenWithNothingOnTopOfOnto(t *testing.T) {
	// No commit on top of onto proves nothing for an old sidecar either: a
	// pick dropped as empty and a commit made by hand cancel out. Refused
	// plain; forced pins the detached HEAD, here onto itself.
	dir, wt, gitDir, old := handedOverRepo(t, 5, false)
	olderSidecar(t, gitDir)
	head := gitIn(t, wt, "rev-parse", "HEAD")
	wts := []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}
	_, err := Undo(dir, wts, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "did not record where it left HEAD") || strings.Contains(err.Error(), "on top of") {
		t.Fatalf("err %v; want the old handover refused without a count", err)
	}
	if busy, err := RebaseInProgress(wt); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the refusal aborted the rebase", busy, err)
	}
	if _, err := Undo(dir, wts, nil, "feature", time.Unix(0, 4321), true); err != nil || gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatalf("forced undo: err %v, HEAD %s want %s", err, gitIn(t, wt, "rev-parse", "HEAD"), old)
	}
	if pinned := gitIn(t, dir, "rev-parse", SafetyRef("feature", 4321)); pinned != head {
		t.Fatalf("the forced undo pinned %s, want the detached HEAD %s", pinned, head)
	}
}

func TestUndoRefusesACommitMadeInsideTheHandover(t *testing.T) {
	dir, wt, gitDir, _ := handedOverRepo(t, 5, false)
	sha := resolveAndCommit(t, wt)
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), sha[:7]) || !strings.Contains(err.Error(), "wt sync undo --force feature") || !strings.Contains(err.Error(), "wt sync resume feature") {
		t.Fatalf("err %v; want the commit named and both ways forward", err)
	}
	if busy, err := RebaseInProgress(wt); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the refusal aborted the rebase", busy, err)
	}
	if has, err := HasPlan(gitDir); err != nil || !has {
		t.Fatalf("HasPlan = %v, %v; the refusal removed the handover", has, err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != sha {
		t.Fatal("HEAD moved despite the refusal")
	}
}

func TestUndoRefusesAContinueThatStoppedAgain(t *testing.T) {
	// Two branch commits; the person resolves stop 1 the way the plan file
	// asks and runs git rebase --continue themselves, which commits pick 1
	// and stops at 2/2 with the old sidecar still describing 1/2. Pick 1's
	// commit carries their resolution; a plain undo names it and refuses.
	dir, wt, gitDir, _ := handedOverRepoWith(t, 5, false,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}, {"v.txt": "1.0.2\n"}})
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("merged by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "--", "a.txt")
	if _, err := gittest.Try(t, wt, "rebase", "--continue"); err == nil {
		t.Fatal("the rebase did not stop again; the test is vacuous")
	}
	if p, err := RebaseProgress(wt); err != nil || p.Index != 2 {
		t.Fatalf("progress %+v, %v; want a stop at 2/2", p, err)
	}
	picked := gitIn(t, wt, "rev-parse", "HEAD")
	if gitIn(t, wt, "log", "-1", "--format=%s") != "branch 1" {
		t.Fatalf("HEAD is %q, not the first pick", gitIn(t, wt, "log", "-1", "--format=%s"))
	}
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), picked[:7]) || !strings.Contains(err.Error(), "wt sync undo --force feature") {
		t.Fatalf("err %v; want pick 1's commit named", err)
	}
	if busy, err := RebaseInProgress(wt); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the refusal aborted the rebase", busy, err)
	}
	if has, err := HasPlan(gitDir); err != nil || !has {
		t.Fatalf("HasPlan = %v, %v; the refusal removed the handover", has, err)
	}
}

func TestUndoForcedPastACommitInsideKeepsItUnderASafetyRef(t *testing.T) {
	dir, wt, gitDir, old := handedOverRepo(t, 5, false)
	sha := resolveAndCommit(t, wt)
	wts := []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}
	got, err := Undo(dir, wts, nil, "feature", time.Unix(0, 4321), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Aborted || got[0].NotRewound || got[0].From != old || got[0].To != old {
		t.Fatalf("restored %+v; want feature aborted and back at %s", got, old)
	}
	if busy, err := RebaseInProgress(wt); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; the forced undo did not abort", busy, err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old || gitIn(t, wt, "symbolic-ref", "HEAD") != "refs/heads/feature" {
		t.Fatal("HEAD is not back on feature at the old tip")
	}
	if has, err := HasPlan(gitDir); err != nil || has {
		t.Fatalf("HasPlan = %v, %v; the forced undo left the handover", has, err)
	}
	if pinned := gitIn(t, dir, "rev-parse", SafetyRef("feature", 4321)); pinned != sha {
		t.Fatalf("the forced undo pinned %s, want the discarded commit %s", pinned, sha)
	}
	if got[0].Kept != SafetyRef("feature", 4321) || got[0].KeptTip != sha {
		t.Fatalf("row %+v; want the safety ref keeping the commit named: the branch never moved, so nothing else does", got[0])
	}
	if res, ok, err := ResultTip(dir, "feature", 4321); err != nil || !ok || res != old {
		t.Fatalf("ResultTip = %s, %v, %v; want the tip the abort left, %s", res, ok, err, old)
	}
	// The forced undo is itself undone by a plain undo, which lands the
	// branch on the person's commit: onto, the picks so far, their resolution.
	got, err = Undo(dir, wts, nil, "feature", time.Now(), false)
	if err != nil {
		t.Fatalf("a forced undo must be undoable without --force: %v", err)
	}
	if len(got) != 1 || got[0].To != sha || gitIn(t, wt, "rev-parse", "HEAD") != sha {
		t.Fatalf("got %+v; HEAD %s want %s", got, gitIn(t, wt, "rev-parse", "HEAD"), sha)
	}
	if gitIn(t, wt, "symbolic-ref", "HEAD") != "refs/heads/feature" || gitIn(t, wt, "status", "--porcelain", "--untracked-files=no") != "" {
		t.Fatal("feature is not checked out clean at the kept commit")
	}
}

func TestUndoRefusesAMovedBranchWithCommitsInsideEvenWhenForced(t *testing.T) {
	// The branch ref was moved by hand while its handed-over rebase, which
	// carries a person's commit, is still in progress: two things to keep,
	// and a forced undo pins one. Refused, and nothing pinned.
	dir, wt, gitDir, _ := handedOverRepo(t, 5, false)
	sha := resolveAndCommit(t, wt)
	gitIn(t, dir, "update-ref", "refs/heads/feature", "main")
	moved := gitIn(t, dir, "rev-parse", "feature")
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}, nil, "feature", time.Unix(0, 4321), true)
	// The abort it names discards the very commits it refuses to discard,
	// so it has to say so.
	if err == nil || !strings.Contains(err.Error(), "moved since that run and carries commits") || !strings.Contains(err.Error(), "rebase --abort) and lose those commits") {
		t.Fatalf("err %v", err)
	}
	if busy, err := RebaseInProgress(wt); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the refusal aborted the rebase", busy, err)
	}
	if has, err := HasPlan(gitDir); err != nil || !has {
		t.Fatalf("HasPlan = %v, %v; the refusal removed the handover", has, err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != sha || gitIn(t, dir, "rev-parse", "feature") != moved {
		t.Fatal("the refusal moved something")
	}
	if out := gitIn(t, dir, "for-each-ref", "--format=%(refname)", SafetyRef("feature", 4321)); out != "" {
		t.Fatalf("a forced pin survived the refusal: %s", out)
	}
}

func TestUndoChecksEverythingBeforeAborting(t *testing.T) {
	// One run pinned two branches: feature was handed over, second has moved
	// on since. The moved-since refusal must come before feature's rebase is
	// aborted, or "nothing undone" throws away a staged resolution.
	dir, wt, gitDir, _ := handedOverRepo(t, 5, false)
	gitIn(t, dir, "branch", "second", "main")
	if _, err := WriteSafety(dir, "second", gitIn(t, dir, "rev-parse", "second"), 5); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "update-ref", "refs/heads/second", "feature")
	moved := gitIn(t, dir, "rev-parse", "second")
	staged := gitIn(t, wt, "rev-parse", ":v.txt")

	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "second has moved since that run") {
		t.Fatalf("err %v", err)
	}
	if busy, err := RebaseInProgress(wt); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the refused undo aborted the handover", busy, err)
	}
	if got := gitIn(t, wt, "rev-parse", ":v.txt"); got != staged {
		t.Fatalf("v.txt is staged as %s, was %s", got, staged)
	}
	if has, err := HasPlan(gitDir); err != nil || !has {
		t.Fatalf("HasPlan = %v, %v; the refused undo removed the handover", has, err)
	}
	if gitIn(t, dir, "rev-parse", "second") != moved {
		t.Fatal("second was reset despite the refusal")
	}
}

func TestUndoTakesOverTheLockAHandoverLeft(t *testing.T) {
	dir, wt, gitDir, old := handedOverRepo(t, 5, true)
	if _, ok, err := ReadLock(gitDir); err != nil || !ok {
		t.Fatalf("ReadLock = %v, %v; the fixture kept no lock", ok, err)
	}
	got, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}, nil, "feature", time.Now(), false)
	if err != nil {
		t.Fatalf("undo refused the lock its own handover left: %v", err)
	}
	if len(got) != 1 || !got[0].Aborted || gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatalf("restored %+v", got)
	}
	if _, ok, err := ReadLock(gitDir); err != nil || ok {
		t.Fatalf("ReadLock = %v, %v; the lock outlived the run it belonged to", ok, err)
	}
}

func TestUndoRefusesAHandoverItCannotRead(t *testing.T) {
	// Without the sidecar the kept lock cannot be recognised as the
	// handover's: the refusal has to name the sidecar, not the lock, and
	// displace nothing.
	dir, wt, gitDir, _ := handedOverRepo(t, 5, true)
	before, ok, err := ReadLock(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadLock = %v, %v; the fixture kept no lock", ok, err)
	}
	if err := os.WriteFile(StatePath(gitDir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged := gitIn(t, wt, "rev-parse", ":v.txt")

	_, err = Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}}, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), StatePath(gitDir)) || !strings.Contains(err.Error(), "nothing undone") || strings.Contains(err.Error(), "locked by") {
		t.Fatalf("err %v; want the unreadable sidecar named", err)
	}
	if busy, err := RebaseInProgress(wt); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the refusal aborted the rebase", busy, err)
	}
	if got := gitIn(t, wt, "rev-parse", ":v.txt"); got != staged {
		t.Fatalf("v.txt is staged as %s, was %s", got, staged)
	}
	after, ok, err := ReadLock(gitDir)
	if err != nil || !ok || after.PID != before.PID || !after.Started.Equal(before.Started) {
		t.Fatalf("lock after = %+v, %v, %v; was %+v: the refusal displaced it", after, ok, err, before)
	}
}

// laterHandover adds a worktree on a new branch "later" at tip, pinned by
// the run at epoch, stopped mid-rebase onto origin/main with a handover
// that passes every check undo makes before it aborts anything.
func laterHandover(t *testing.T, dir, tip string, epoch int64) (path, gitDir string) {
	t.Helper()
	gitIn(t, dir, "branch", "later", tip)
	path = dir + "-later"
	gitIn(t, dir, "worktree", "add", "-q", path, "later")
	if _, err := WriteSafety(dir, "later", tip, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := gittest.Try(t, path, append(append([]string{}, rebaseConfig...), "rebase", "--no-update-refs", "--no-gpg-sign", "origin/main")...); err == nil {
		t.Fatal("later's rebase did not stop; the test is vacuous")
	}
	gitDir, err := GitDir(path)
	if err != nil {
		t.Fatal(err)
	}
	st := State{Branch: "later", Work: "later", Epoch: epoch, Onto: "origin/main", OldTip: tip, Safety: SafetyRef("later", epoch),
		Head: gitIn(t, path, "rev-parse", "HEAD"), Stop: 1, Total: 1}
	if err := WriteState(gitDir, st); err != nil {
		t.Fatal(err)
	}
	return path, gitDir
}

// unabortableHandover is laterHandover whose git rebase --abort cannot
// work: a stale index.lock stops the reset the abort has to make, and the
// rebase stays in progress. The sequencer itself is intact, so the handover
// passes VerifyLeft and the failure comes from the abort, not a check.
func unabortableHandover(t *testing.T, dir, tip string, epoch int64) (path string) {
	t.Helper()
	path, gitDir := laterHandover(t, dir, tip, epoch)
	if err := os.WriteFile(filepath.Join(gitDir, "index.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUndoReportsWhatItAbortedBeforeALaterAbortFailed(t *testing.T) {
	// One run moved base (a parent, say) and handed over feature and later;
	// later's abort cannot work. feature is already put back by its abort
	// and must be reported. base sorts first and needs a reset, but no
	// branch may be reset while a handover is still mid-rebase: that would
	// leave a leaf rebased onto a parent that has already been put back.
	dir, wt, gitDir, old := handedOverRepo(t, 5, false)
	gitIn(t, dir, "branch", "base", "main")
	baseOld := gitIn(t, dir, "rev-parse", "base")
	if _, err := WriteSafety(dir, "base", baseOld, 5); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "update-ref", "refs/heads/base", old)
	if err := WriteResult(dir, "base", old, 5); err != nil {
		t.Fatal(err)
	}
	later := unabortableHandover(t, dir, old, 5)

	wts := []repo.Worktree{{Path: wt, Branch: "feature", Rebasing: true}, {Path: later, Branch: "later", Rebasing: true}}
	got, err := Undo(dir, wts, nil, "feature", time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "later: rebase --abort") {
		t.Fatalf("err %v; want later's abort to fail", err)
	}
	if len(got) != 1 || got[0].Branch != "feature" || !got[0].Aborted || got[0].NotRewound || got[0].From != old || got[0].To != old {
		t.Fatalf("restored %+v; want feature's abort reported, and nothing else", got)
	}
	if busy, err := RebaseInProgress(wt); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; feature was not aborted", busy, err)
	}
	if has, err := HasPlan(gitDir); err != nil || has {
		t.Fatalf("HasPlan = %v, %v; feature's handover survived its abort", has, err)
	}
	if got := gitIn(t, dir, "rev-parse", "base"); got != old {
		t.Fatalf("base was reset to %s while later is still mid-rebase; nothing may be reset", got)
	}
}

func TestAbortedRowsSaysNotRewoundWhenTheAbortLandedElsewhere(t *testing.T) {
	// The rows for an undo that stopped after its aborts: a branch whose
	// abort put it at the safety tip is simply aborted; one whose abort
	// landed elsewhere (its rebase had been restarted from another commit)
	// is reported from where it is now, as not rewound, rather than as a
	// rewind that never happened. Undo itself no longer aborts such a
	// rebase, but the report stays honest for any abort that lands off the
	// tip, and restoredLine and UndoneLine read these fields.
	dir, wt, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "by hand")
	moved := gitIn(t, wt, "rev-parse", "HEAD")
	gitIn(t, dir, "branch", "second", old)
	run := []Safety{
		{Branch: "feature", Epoch: 5, Ref: SafetyRef("feature", 5), Tip: old},
		{Branch: "second", Epoch: 5, Ref: SafetyRef("second", 5), Tip: old},
		{Branch: "skipped", Epoch: 5, Ref: SafetyRef("skipped", 5), Tip: old},
	}
	byBranch := map[string]repo.Worktree{"feature": {Path: wt, Branch: "feature"}, "second": {Path: dir + "-second", Branch: "second"}}
	// tips is where each branch was before the aborts; second's abort put
	// it at the safety tip, feature's did not.
	tips := map[string]string{"feature": moved, "second": moved, "skipped": moved}
	aborted := map[string]bool{"feature": true, "second": true}

	rows := abortedRows(dir, run, byBranch, tips, aborted)
	if len(rows) != 2 {
		t.Fatalf("rows %+v; want feature and second, not the branch that was never aborted", rows)
	}
	f, s := rows[0], rows[1]
	if f.Branch != "feature" || !f.Aborted || !f.NotRewound || f.From != moved || f.To != old || f.Path != wt {
		t.Fatalf("feature row %+v; want aborted, not rewound, still at %s", f, moved)
	}
	if s.Branch != "second" || !s.Aborted || s.NotRewound || s.From != moved || s.To != old {
		t.Fatalf("second row %+v; want aborted and back at %s", s, old)
	}
}

func TestUndoPassesAnIdleSessionAndRefusesABusyOne(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	completed(t, dir, wt, "feature", 5)
	resolved, _ := filepath.EvalSymlinks(wt)
	wts := []repo.Worktree{{Path: wt, Branch: "feature"}}
	busy := []Agent{{Name: "f-1", Cwd: resolved, Kind: "interactive", Status: "idle"}, {Name: "f-2", Cwd: resolved, Kind: "interactive", Status: "busy"}}
	if _, err := Undo(dir, wts, busy, "feature", time.Now(), false); err == nil || !strings.Contains(err.Error(), "busy in it: f-2 +1") {
		t.Fatalf("err %v", err)
	}
	idle := busy[:1]
	if _, err := Undo(dir, wts, idle, "feature", time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatal("not restored under an idle session")
	}
}

func TestUndoBranchesNamesEveryBranchOfTheNewestRun(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	if got, err := UndoBranches(dir, "feature"); err != nil || !reflect.DeepEqual(got, []string{"feature"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := UndoBranches(dir, "nothing-here"); err == nil || !strings.Contains(err.Error(), "no run to undo") {
		t.Fatalf("err %v", err)
	}
}
