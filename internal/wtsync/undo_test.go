package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/repo"
)

func TestUndoResetsEveryBranchOfTheNewestEpoch(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	if _, err := Rebase(dir, cfg, trunkReq(wt, 5), nil); err != nil {
		t.Fatal(err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") == old {
		t.Fatal("rebase did nothing; the test is vacuous")
	}
	got, err := Undo(dir, []repo.Worktree{{Path: dir, Branch: "main", IsMain: true}, {Path: wt, Branch: "feature"}}, nil, "feature", time.Now())
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
	got, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "second", time.Now())
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
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "feature", time.Now())
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
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, []Agent{{Name: "f-1", Cwd: resolved}}, "feature", time.Now())
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
	got, err := Undo(dir, []repo.Worktree{{Path: dir, Branch: "main", IsMain: true}}, nil, "main", time.Now())
	if err != nil || len(got) != 1 || got[0].Path != dir || gitIn(t, dir, "rev-parse", "HEAD") != old {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestUndoWithoutARunIsAnError(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, nil)
	_, err := Undo(dir, []repo.Worktree{{Path: wt, Branch: "feature"}}, nil, "feature", time.Now())
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
	got, err := Undo(dir, nil, nil, "loose", time.Now())
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
	wts := []repo.Worktree{{Path: wt, Branch: "feature"}}
	if _, err := Undo(dir, wts, nil, "feature", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := Undo(dir, wts, nil, "feature", time.Now())
	if err != nil || len(got) != 1 || got[0].From != old || got[0].To != old {
		t.Fatalf("got %+v err %v", got, err)
	}
}
