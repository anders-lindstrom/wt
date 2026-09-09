package wtsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deferRepo(t *testing.T) (wt, oldTip, newTip string) {
	t.Helper()
	dir := linearRepo(t, []map[string]string{{"gen/in.txt": "trunk\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	w := featureWorktree(t, dir)
	oldTip = gitIn(t, w.Path, "rev-parse", "HEAD")
	gitIn(t, w.Path, "rebase", "-q", "--no-gpg-sign", "main")
	newTip = gitIn(t, w.Path, "rev-parse", "HEAD")
	return w.Path, oldTip, newTip
}

func TestChangedPathsIsTheDiffBetweenTheTips(t *testing.T) {
	wt, old, cur := deferRepo(t)
	got, err := ChangedPaths(wt, old, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "gen/in.txt" {
		t.Fatalf("changed %v", got)
	}
}

func TestRunDeferredRunsAMatchingStepAndCommitsTrackedOutput(t *testing.T) {
	wt, old, _ := deferRepo(t)
	steps := []Deferred{{Run: "cp gen/in.txt gen/out.txt && echo regenerated", Paths: []string{"gen/**"}, Commit: "chore: regenerate"}}
	// gen/out.txt must be tracked for `add -u` to pick it up.
	if err := os.WriteFile(filepath.Join(wt, "gen", "out.txt"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "track out")
	cur := gitIn(t, wt, "rev-parse", "HEAD")
	rs, err := RunDeferred(wt, steps, old, cur, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := rs[0]
	if !r.Ran || r.Err != nil || r.Commit == "" || r.Files != 1 || !strings.Contains(r.Output, "regenerated") {
		t.Fatalf("result %+v", r)
	}
	if gitIn(t, wt, "log", "-1", "--format=%s") != "chore: regenerate" {
		t.Fatal("no regeneration commit")
	}
	if out := gitIn(t, wt, "status", "--porcelain"); out != "" {
		t.Fatalf("dirty after: %q", out)
	}
}

func TestRunDeferredSkipsAStepWhoseListedPathsDidNotChange(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := RunDeferred(wt, []Deferred{{Run: "exit 1", Paths: []string{"other/**"}}}, old, cur, nil)
	if err != nil || rs[0].Ran || rs[0].Why == "" {
		t.Fatalf("rs %+v err %v", rs, err)
	}
}

func TestRunDeferredAStepWithoutPathsAlwaysRunsAndNeedsNoCommit(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := RunDeferred(wt, []Deferred{{Run: "echo hi > /dev/null"}}, old, cur, nil)
	if err != nil || !rs[0].Ran || rs[0].Err != nil || rs[0].Commit != "" {
		t.Fatalf("rs %+v err %v", rs, err)
	}
}

func TestRunDeferredAFailingStepIsOwedStopsLaterStepsAndTheRebaseStays(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := RunDeferred(wt, []Deferred{{Run: "echo boom >&2; exit 3"}, {Run: "true"}}, old, cur, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Err == nil || !strings.Contains(rs[0].Err.Error(), "exit 3") || !strings.Contains(rs[0].Output, "boom") {
		t.Fatalf("rs[0] %+v", rs[0])
	}
	if rs[1].Ran || !strings.Contains(rs[1].Why, "earlier step failed") {
		t.Fatalf("second step ran after a failure: %+v", rs[1])
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != cur {
		t.Fatal("HEAD moved")
	}
}

func TestRunDeferredTimesOut(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := runDeferredWithTimeout(wt, []Deferred{{Run: "sleep 5"}}, old, cur, nil, 300*time.Millisecond)
	if err != nil || rs[0].Err == nil || !strings.Contains(rs[0].Err.Error(), "timed out") {
		t.Fatalf("rs %+v err %v", rs, err)
	}
}

// TestRunDeferredTimesOutEvenWhenAnOrphanedChildMakesTheStepLookClean covers
// a step whose own process exits 0 quickly but leaves a child detached into
// its own process group (so our group kill can't reach it) still holding
// stdout/stderr open past the wait delay: exec reports that as
// exec.ErrWaitDelay, which looks like success, but ctx already timed out by
// the time Wait returns. The deadline must win over that appearance of
// success (script.go's proven order), or the step is silently reported as a
// clean success even though it ran past its deadline.
//
// setsid isn't on macOS, so a perl one-liner stands in for it: it calls
// setpgrp(0,0) to leave the shell's process group before exec'ing sleep.
func TestRunDeferredTimesOutEvenWhenAnOrphanedChildMakesTheStepLookClean(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl not on PATH")
	}
	wt, old, cur := deferRepo(t)
	timeout := 300 * time.Millisecond
	start := time.Now()
	rs, err := runDeferredWithTimeout(wt, []Deferred{{Run: `perl -e 'setpgrp(0,0); exec "sleep", "5"' & true`}}, old, cur, nil, timeout)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Err == nil || !strings.Contains(rs[0].Err.Error(), "timed out") {
		t.Fatalf("rs[0] %+v", rs[0])
	}
	if bound := timeout + scriptWaitDelay + time.Second; elapsed > bound {
		t.Fatalf("elapsed %s exceeds bound %s", elapsed, bound)
	}
}

func TestRunDeferredAStepThatDirtiesTrackedFilesWithoutACommitIsReported(t *testing.T) {
	wt, old, cur := deferRepo(t)
	rs, err := RunDeferred(wt, []Deferred{{Run: "echo x >> b.txt"}}, old, cur, nil)
	if err != nil || rs[0].Err == nil || !strings.Contains(rs[0].Err.Error(), "no commit") {
		t.Fatalf("rs %+v err %v", rs, err)
	}
}
