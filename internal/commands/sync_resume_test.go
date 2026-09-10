package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// handedOver drives the contested fixture to the point a run hands it over:
// v.txt resolved by the declared owned-line strategy and staged, a.txt left
// unmerged for a person, the rebase still in progress, the sidecar written.
func handedOver(t *testing.T) (ctx *Context, bump, gitDir string, st wtsync.State) {
	t.Helper()
	ctx, bump = contestedFixture(t)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err == nil {
		t.Fatalf("the run should have handed over:\n%s", out.String())
	}
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	st, ok, err := wtsync.ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the fixture is not mid-rebase", busy, berr)
	}
	return ctx, bump, gitDir, st
}

// noResumeAgents is the resume half of noAgents: no sessions anywhere, and a
// clock late enough to take the lock the run left over rather than expire it.
func noResumeAgents() ResumeOptions {
	return ResumeOptions{Agents: []wtsync.Agent{}, Now: func() time.Time { return time.Unix(0, 199) }}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncResumeFinishesAHandedOverRebase(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	// The person does what the plan file asks: resolve what is theirs and
	// stage it. Nothing else in the worktree is touched.
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")

	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", noResumeAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "push:") {
		t.Fatalf("no push line:\n%s", out.String())
	}
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; the rebase did not finish", busy, err)
	}
	if has, err := wtsync.HasPlan(gitDir); err != nil || has {
		t.Fatalf("HasPlan = %v, %v; a completed run hands nothing over", has, err)
	}
	if _, err := os.Stat(wtsync.PlanPath(gitDir)); !os.IsNotExist(err) {
		t.Fatalf("the plan file survived: %v", err)
	}
	tip := gitOut(t, ctx.Repo.MainRoot, "rev-parse", st.Branch)
	if tip == st.OldTip {
		t.Fatalf("%s is still at %s", st.Branch, short(st.OldTip))
	}
	// Same run, so the result ref carries the run's own epoch.
	got, ok, err := wtsync.ResultTip(ctx.Repo.MainRoot, st.Branch, st.Epoch)
	if err != nil || !ok {
		t.Fatalf("ResultTip = %v, %v; the run pinned no result", ok, err)
	}
	if got != tip {
		t.Fatalf("result ref %s, branch %s", short(got), short(tip))
	}
	// The strategy's answer stands and the person's does too.
	if b, _ := os.ReadFile(filepath.Join(bump, "v.txt")); string(b) != "1.0.6\n" {
		t.Fatalf("v.txt %q; the strategy's resolution did not survive", b)
	}
	if b, _ := os.ReadFile(filepath.Join(bump, "a.txt")); string(b) != "merged by hand\n" {
		t.Fatalf("a.txt %q", b)
	}
}

func TestSyncResumeRefusesAHandMergedOwnedFile(t *testing.T) {
	ctx, bump, gitDir, _ := handedOver(t)
	// What is theirs is resolved, but so is what is not: v.txt belongs to
	// the owned-line strategy and the plan file says never to touch it.
	writeFile(t, bump, "a.txt", "merged by hand\n")
	writeFile(t, bump, "v.txt", "9.9.9\n")
	gitOut(t, bump, "add", "--", "a.txt", "v.txt")
	head := gitOut(t, bump, "rev-parse", "HEAD")

	var out bytes.Buffer
	err := SyncResume(ctx, "bump", noResumeAgents(), &out)
	if err == nil {
		t.Fatalf("a hand-merged owned file must be refused:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "v.txt") || !strings.Contains(err.Error(), "owned-line") {
		t.Fatalf("the error names neither the file nor the strategy: %v", err)
	}
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; a refusal changes nothing", busy, berr)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != head {
		t.Fatal("HEAD moved")
	}
	if _, ok, serr := wtsync.ReadState(gitDir); serr != nil || !ok {
		t.Fatalf("ReadState = %v, %v; the sidecar is gone", ok, serr)
	}
	// The person's own bytes are still in the worktree: nothing was reset.
	if b, _ := os.ReadFile(filepath.Join(bump, "v.txt")); string(b) != "9.9.9\n" {
		t.Fatalf("v.txt %q; the refusal overwrote the worktree", b)
	}
}

func TestSyncResumeRefusesWhileSomethingIsUnmerged(t *testing.T) {
	ctx, bump, gitDir, _ := handedOver(t)
	var out bytes.Buffer
	err := SyncResume(ctx, "bump", noResumeAgents(), &out)
	if err == nil {
		t.Fatalf("an unmerged path must be refused:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "a.txt") || !strings.Contains(err.Error(), "git add") {
		t.Fatalf("the error does not name the path and what to do: %v", err)
	}
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; a refusal changes nothing", busy, berr)
	}
	if _, ok, serr := wtsync.ReadState(gitDir); serr != nil || !ok {
		t.Fatalf("ReadState = %v, %v; the sidecar is gone", ok, serr)
	}
}

// git refuses to continue a rebase over a tracked file that is changed but
// not staged. Reaching the loop with that would make the non-advance guard
// read the refusal as a stuck rebase — which in a run means a reset. Here
// that reset would land on a person's own edit, so it is caught first.
func TestSyncResumeRefusesUnstagedTrackedChanges(t *testing.T) {
	ctx, bump, _, _ := handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	writeFile(t, bump, "a.txt", "second thoughts\n")

	var out bytes.Buffer
	err := SyncResume(ctx, "bump", noResumeAgents(), &out)
	if err == nil {
		t.Fatalf("an unstaged tracked change must be refused:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "not staged") || !strings.Contains(err.Error(), "a.txt") {
		t.Fatalf("the error does not say the worktree has unstaged changes: %v", err)
	}
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; a refusal changes nothing", busy, berr)
	}
	if b, _ := os.ReadFile(filepath.Join(bump, "a.txt")); string(b) != "second thoughts\n" {
		t.Fatalf("a.txt %q; the later edit was thrown away", b)
	}
}

func TestSyncResumeWithoutAHandover(t *testing.T) {
	ctx, _ := runFixture(t, false)
	var out bytes.Buffer
	err := SyncResume(ctx, "other", noResumeAgents(), &out)
	if err == nil {
		t.Fatalf("there is nothing to resume:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "was not left mid-rebase by wt sync run") {
		t.Fatalf("err %v", err)
	}
}
