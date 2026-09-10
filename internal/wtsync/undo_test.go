package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
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
		Stop: res.Left.Index, Total: res.Left.Total,
		Resolved: res.Left.Staged, Strategy: map[string]string{"v.txt": "owned-line"},
		Deleted: res.Left.Deleted, Left: res.Left.Left,
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
	if err := gitCmd(wt, "rebase", "--no-update-refs", "--no-gpg-sign", "main").Run(); err == nil {
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
