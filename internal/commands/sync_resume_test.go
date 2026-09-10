package commands

import (
	"bytes"
	"os"
	"os/exec"
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
	gitDir, st = handOverNow(t, ctx, bump)
	return ctx, bump, gitDir, st
}

// handOverNow runs sync run on bump and returns what it handed over.
func handOverNow(t *testing.T, ctx *Context, bump string) (gitDir string, st wtsync.State) {
	t.Helper()
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
		t.Fatalf("ReadState = %v, %v\n%s", ok, err, out.String())
	}
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the fixture is not mid-rebase", busy, berr)
	}
	return gitDir, st
}

// laterStopFixture is contestedFixture with a second branch commit that bumps
// v.txt again, so a rebase continued past the handed-over stop meets v.txt —
// owned-line's — in conflict at 2/2. withUnclaimed also has that commit add
// b.txt, which trunk adds too and nobody claims.
func laterStopFixture(t *testing.T, withUnclaimed bool) (ctx *Context, bump string) {
	t.Helper()
	ctx, bump = contestedFixture(t)
	main := ctx.Repo.MainRoot
	if withUnclaimed {
		writeFile(t, main, "b.txt", "trunk\n")
		gitOut(t, main, "add", "-A")
		gitOut(t, main, "commit", "-q", "-m", "b on trunk")
		gitOut(t, main, "fetch", "-q", "origin")
		writeFile(t, bump, "b.txt", "branch\n")
	}
	writeFile(t, bump, "v.txt", "1.0.2\n")
	gitOut(t, bump, "add", "-A")
	gitOut(t, bump, "commit", "-q", "-m", "bump again")
	return ctx, bump
}

// noResumeAgents is the resume half of noAgents: no sessions anywhere, and a
// clock a few seconds on, so the lock a resume takes is told apart from the
// one the run kept without the run's expiring.
func noResumeAgents() ResumeOptions {
	return ResumeOptions{Agents: []wtsync.Agent{}, Now: func() time.Time { return time.Unix(5, 0) }}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitTry runs git where a non-zero exit is part of the scenario — a person's
// `rebase --continue` that stops at the next conflict — and never opens an
// editor. The caller asserts the state it was after.
func gitTry(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	out, _ := cmd.CombinedOutput()
	return string(out)
}

// assertUntouched is what "a refusal changes nothing" means: the rebase is
// still in progress, the sidecar is still there, and the lock is still the
// one the run kept.
func assertUntouched(t *testing.T, bump, gitDir string, st wtsync.State) {
	t.Helper()
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; a refusal changes nothing", busy, err)
	}
	if _, ok, err := wtsync.ReadState(gitDir); err != nil || !ok {
		t.Fatalf("ReadState = %v, %v; the sidecar is gone", ok, err)
	}
	l, ok, err := wtsync.ReadLock(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadLock = %v, %v; the refusal removed the lock the run kept", ok, err)
	}
	if l.PID != st.Lock.PID || l.Started.Unix() != st.Lock.Started {
		t.Fatalf("lock pid %d start %d; the run kept pid %d start %d", l.PID, l.Started.Unix(), st.Lock.PID, st.Lock.Started)
	}
}

func TestSyncResumeFinishesAHandedOverRebase(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	// The person does what the plan file asks: resolve what is left to them and
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
	ctx, bump, gitDir, st := handedOver(t)
	// What is left to the person is resolved, but so is what is not: v.txt belongs to
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
	assertUntouched(t, bump, gitDir, st)
	if gitOut(t, bump, "rev-parse", "HEAD") != head {
		t.Fatal("HEAD moved")
	}
	// The person's own bytes are still in the worktree: nothing was reset.
	if b, _ := os.ReadFile(filepath.Join(bump, "v.txt")); string(b) != "9.9.9\n" {
		t.Fatalf("v.txt %q; the refusal overwrote the worktree", b)
	}
}

func TestSyncResumeRefusesWhileSomethingIsUnmerged(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	var out bytes.Buffer
	err := SyncResume(ctx, "bump", noResumeAgents(), &out)
	if err == nil {
		t.Fatalf("an unmerged path must be refused:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "a.txt") || !strings.Contains(err.Error(), "git add") {
		t.Fatalf("the error does not name the path and what to do: %v", err)
	}
	assertUntouched(t, bump, gitDir, st)
}

// git refuses to continue a rebase over a tracked file that is changed but
// not staged. Reaching the loop with that would make the non-advance guard
// read the refusal as a stuck rebase — which in a run means a reset. Here
// that reset would land on a person's own edit, so it is caught first.
func TestSyncResumeRefusesUnstagedTrackedChanges(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
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
	assertUntouched(t, bump, gitDir, st)
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

// A rebase somebody started themselves after aborting the run's is not the
// run's. Continuing it would pin a result ref under the run's epoch for
// commits the run never planned, and the msgnum it stops at need not match
// the handover's, so nothing downstream would notice.
func TestSyncResumeRefusesARebaseItDidNotLeave(t *testing.T) {
	ctx, bump := laterStopFixture(t, false)
	gitDir, st := handOverNow(t, ctx, bump)
	gitOut(t, bump, "rebase", "--abort")
	// Their own interactive rebase of the same branch, onto its own base
	// rather than the run's trunk, stopped at its second commit: 2/2, where
	// the handover says 1/2.
	gitOut(t, bump, "-c", "sequence.editor=sed -i.bak 2s/^pick/edit/", "rebase", "-i", "HEAD~2")
	if p, err := wtsync.RebaseProgress(bump); err != nil || p.Index != 2 {
		t.Fatalf("progress %+v, %v; the foreign rebase did not stop at 2/2", p, err)
	}

	var out bytes.Buffer
	err := SyncResume(ctx, "bump", noResumeAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "not the one wt sync run left") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	assertUntouched(t, bump, gitDir, st)
	if _, ok, rerr := wtsync.ResultTip(ctx.Repo.MainRoot, st.Branch, st.Epoch); rerr != nil || ok {
		t.Fatalf("ResultTip = %v, %v; a foreign rebase was certified as the run's", ok, rerr)
	}
}

// At the stop the handover recorded, a path a strategy resolved can only be
// unmerged again because somebody undid the resolution. Resolving it is not
// the person's job, so resume must not tell them to.
func TestSyncResumeNeverAsksAPersonToMergeAStrategysFile(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	gitOut(t, bump, "checkout", "-m", "--", "v.txt")
	if cs, err := wtsync.StagedConflicts(bump); err != nil || len(cs) != 1 || cs[0].Path != "v.txt" {
		t.Fatalf("unmerged %+v, %v; want v.txt", cs, err)
	}

	var out bytes.Buffer
	err := SyncResume(ctx, "bump", noResumeAgents(), &out)
	if err == nil {
		t.Fatalf("an owned path unmerged again must be refused:\n%s", out.String())
	}
	for _, want := range []string{"v.txt", "owned-line", "wt sync undo bump"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "git add") {
		t.Fatalf("the error tells the person to hand-merge a strategy's file: %v", err)
	}
	assertUntouched(t, bump, gitDir, st)
}

// A person who continued by hand past the recorded stop can land on a later
// stop where a strategy's file conflicts again. That stop is the strategies'
// to take, exactly as it would have been in the run.
func TestSyncResumeTakesALaterStopThroughTheStrategies(t *testing.T) {
	ctx, bump := laterStopFixture(t, false)
	gitDir, st := handOverNow(t, ctx, bump)
	if st.Stop != 1 || st.Total != 2 {
		t.Fatalf("state = %+v; want the handover at 1/2", st)
	}
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	gitTry(t, bump, "rebase", "--continue")
	if p, err := wtsync.RebaseProgress(bump); err != nil || p.Index != 2 {
		t.Fatalf("progress %+v, %v; the hand continue did not stop at 2/2", p, err)
	}
	if cs, err := wtsync.StagedConflicts(bump); err != nil || len(cs) != 1 || cs[0].Path != "v.txt" {
		t.Fatalf("unmerged %+v, %v; want v.txt", cs, err)
	}

	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", noResumeAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "✓ v.txt owned-line") {
		t.Fatalf("the strategy did not take the later stop:\n%s", s)
	}
	if !strings.Contains(s, "not re-checked: v.txt (owned-line)") {
		t.Fatalf("the note does not name what was not re-checked:\n%s", s)
	}
	if b, _ := os.ReadFile(filepath.Join(bump, "v.txt")); string(b) != "1.0.7\n" {
		t.Fatalf("v.txt %q; want owned-line's 1.0.7", b)
	}
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; the rebase did not finish", busy, err)
	}
	if _, ok, err := wtsync.ResultTip(ctx.Repo.MainRoot, st.Branch, st.Epoch); err != nil || !ok {
		t.Fatalf("ResultTip = %v, %v; the run pinned no result", ok, err)
	}
	if has, err := wtsync.HasPlan(gitDir); err != nil || has {
		t.Fatalf("HasPlan = %v, %v; a completed run hands nothing over", has, err)
	}
}

// The same later stop with a file nobody claims in it: the strategies take
// their file and the rest is handed over afresh, not refused. Resumed by the
// branch name, the new sidecar still carries the name the run recorded.
func TestSyncResumeHandsALaterUnclaimedStopOverAgain(t *testing.T) {
	ctx, bump := laterStopFixture(t, true)
	gitDir, st := handOverNow(t, ctx, bump)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	gitTry(t, bump, "rebase", "--continue")
	if p, err := wtsync.RebaseProgress(bump); err != nil || p.Index != 2 {
		t.Fatalf("progress %+v, %v; the hand continue did not stop at 2/2", p, err)
	}

	var out bytes.Buffer
	err := SyncResume(ctx, st.Branch, noResumeAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "needs you") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "stopped again at 2/2") {
		t.Fatalf("out %s", out.String())
	}
	st2, ok, err := wtsync.ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v; no fresh handover", ok, err)
	}
	if st2.Stop != 2 || st2.Epoch != st.Epoch || st2.Work != st.Work {
		t.Fatalf("state = %+v; want stop 2, epoch %d, work %q", st2, st.Epoch, st.Work)
	}
	if st2.Strategy["v.txt"] != "owned-line" || len(st2.Left) != 1 || st2.Left[0] != "b.txt" {
		t.Fatalf("state = %+v", st2)
	}
	if staged := gitOut(t, bump, "rev-parse", ":0:v.txt"); staged != st2.Resolved["v.txt"] {
		t.Fatalf("v.txt staged %s, handover records %s", staged, st2.Resolved["v.txt"])
	}
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; want the rebase left for the person", busy, berr)
	}
	if _, ok, lerr := wtsync.ReadLock(gitDir); lerr != nil || !ok {
		t.Fatalf("ReadLock = %v, %v; want the lock kept", ok, lerr)
	}
}

// The most common hand path: resolve at the recorded stop — here also
// overwriting a file a strategy owns — and git rebase --continue to the end.
// What can no longer be checked is said out loud.
func TestSyncResumeSaysWhatAFinishedByHandRebaseDidNotRecheck(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	writeFile(t, bump, "v.txt", "9.9.9\n")
	gitOut(t, bump, "add", "--", "a.txt", "v.txt")
	gitTry(t, bump, "rebase", "--continue")
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; the hand continue did not finish", busy, err)
	}

	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", noResumeAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "not re-checked: v.txt (owned-line)") {
		t.Fatalf("the finished-by-hand path said nothing about what it could not check:\n%s", s)
	}
	if _, ok, err := wtsync.ResultTip(ctx.Repo.MainRoot, st.Branch, st.Epoch); err != nil || !ok {
		t.Fatalf("ResultTip = %v, %v", ok, err)
	}
	if has, err := wtsync.HasPlan(gitDir); err != nil || has {
		t.Fatalf("HasPlan = %v, %v", has, err)
	}
}

// restartedByHand is a person who aborted the handed-over rebase, committed
// a fix of their own, and rebased onto the run's onto again. They resolve the
// first stop exactly as the handover did: v.txt with the strategy's blob and
// a.txt by hand. Every recorded check still matches. Only the tip the rebase
// started from differs, and that tip carries a commit the run never made.
func restartedByHand(t *testing.T, bump string, st wtsync.State) {
	t.Helper()
	gitOut(t, bump, "rebase", "--abort")
	writeFile(t, bump, "x.txt", "mine\n")
	gitOut(t, bump, "add", "--", "x.txt")
	gitOut(t, bump, "commit", "-q", "-m", "a fix of my own")
	gitTry(t, bump, "-c", "rebase.backend=merge", "rebase", st.Onto)
	if p, err := wtsync.RebaseProgress(bump); err != nil || p.Index != st.Stop {
		t.Fatalf("progress %+v, %v; the restarted rebase did not stop where the handover did", p, err)
	}
	writeFile(t, bump, "v.txt", "1.0.6\n")
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "v.txt", "a.txt")
	if staged := gitOut(t, bump, "rev-parse", ":0:v.txt"); staged != st.Resolved["v.txt"] {
		t.Fatalf("v.txt staged %s, handover records %s; the test would be refused for the wrong reason", short(staged), short(st.Resolved["v.txt"]))
	}
}

// A rebase restarted from another tip is not the run's, even onto the same
// commit. Continuing it would pin a result that carries the person's own
// commit under the run's epoch, and a later plain undo would discard that
// commit without refusing.
func TestSyncResumeRefusesARebaseRestartedFromAnotherTip(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	restartedByHand(t, bump, st)

	var out bytes.Buffer
	err := SyncResume(ctx, "bump", noResumeAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "not the one wt sync run left") || !strings.Contains(err.Error(), "started from") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	assertUntouched(t, bump, gitDir, st)
	if _, ok, rerr := wtsync.ResultTip(ctx.Repo.MainRoot, st.Branch, st.Epoch); rerr != nil || ok {
		t.Fatalf("ResultTip = %v, %v; a restarted rebase was certified as the run's", ok, rerr)
	}
}

// The same restarted rebase, finished by hand. No sequencer is left to read,
// so the branch's reflog must show where the rebase started.
func TestSyncResumeRefusesARebaseFinishedByHandFromAnotherTip(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	restartedByHand(t, bump, st)
	gitTry(t, bump, "rebase", "--continue")
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; the hand continue did not finish", busy, err)
	}
	if gitOut(t, bump, "log", "-1", "--format=%s") != "a fix of my own" {
		t.Fatal("the person's commit is not on the branch; the test is vacuous")
	}
	head := gitOut(t, bump, "rev-parse", "HEAD")

	var out bytes.Buffer
	err := SyncResume(ctx, "bump", noResumeAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "wt sync undo --force bump") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if _, ok, rerr := wtsync.ResultTip(ctx.Repo.MainRoot, st.Branch, st.Epoch); rerr != nil || ok {
		t.Fatalf("ResultTip = %v, %v; a rebase finished from another tip was certified as the run's", ok, rerr)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != head {
		t.Fatal("HEAD moved")
	}
	if _, ok, rerr := wtsync.ReadState(gitDir); rerr != nil || !ok {
		t.Fatalf("ReadState = %v, %v; the refusal removed the handover", ok, rerr)
	}
	if l, ok, lerr := wtsync.ReadLock(gitDir); lerr != nil || !ok || l.PID != st.Lock.PID || l.Started.Unix() != st.Lock.Started {
		t.Fatalf("ReadLock = %+v, %v, %v; the refusal did not leave the lock the run kept", l, ok, lerr)
	}
}

// The whole seam: a run hands over, resume finishes it under the run's epoch,
// and a plain undo of that run puts the branch back where the run found it.
func TestSyncUndoPutsBackWhatAResumeFinished(t *testing.T) {
	ctx, bump, gitDir, st := handedOver(t)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")

	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", noResumeAgents(), &out); err != nil {
		t.Fatalf("resume: %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") == st.OldTip {
		t.Fatal("resume did not move the branch; the test is vacuous")
	}

	var undoOut bytes.Buffer
	if err := SyncUndo(ctx, "bump", noAgentsUndo(), &undoOut); err != nil {
		t.Fatalf("undo: %v\n%s", err, undoOut.String())
	}
	if got := gitOut(t, ctx.Repo.MainRoot, "rev-parse", st.Branch); got != st.OldTip {
		t.Fatalf("%s is at %s, want the run's old tip %s\n%s", st.Branch, short(got), short(st.OldTip), undoOut.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != st.OldTip {
		t.Fatal("HEAD not restored to the run's old tip")
	}
	if has, err := wtsync.HasPlan(gitDir); err != nil || has {
		t.Fatalf("HasPlan = %v, %v", has, err)
	}
}

// A handover aborted by hand is stale: there is nothing to resume, and a run
// refuses while its plan is there. Resume must name the command that clears
// it, that command must work, and a run must then start again.
func TestSyncResumeSendsAnAbortedHandoverToUndo(t *testing.T) {
	ctx, bump, gitDir, _ := handedOver(t)
	gitOut(t, bump, "rebase", "--abort")

	var out bytes.Buffer
	err := SyncResume(ctx, "bump", noResumeAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "wt sync undo bump") {
		t.Fatalf("err %v, want it to name wt sync undo bump\n%s", err, out.String())
	}

	var undoOut bytes.Buffer
	if err := SyncUndo(ctx, "bump", noAgentsUndo(), &undoOut); err != nil {
		t.Fatalf("undo: %v\n%s", err, undoOut.String())
	}
	if has, err := wtsync.HasPlan(gitDir); err != nil || has {
		t.Fatalf("HasPlan = %v, %v; undo left the stale handover", has, err)
	}

	// A later run, with an epoch of its own, is no longer refused as waiting
	// on a person: it walks into the contested stop and hands it over again.
	opts := noAgents()
	opts.Now = func() time.Time { return time.Unix(0, 200) }
	var runOut bytes.Buffer
	_ = SyncRun(ctx, []string{"bump"}, opts, &runOut)
	if strings.Contains(runOut.String(), "left mid-rebase") {
		t.Fatalf("the run still refuses the cleared handover:\n%s", runOut.String())
	}
	if st2, ok, err := wtsync.ReadState(gitDir); err != nil || !ok || st2.Epoch != 200 {
		t.Fatalf("ReadState = %+v, %v, %v; the run did not get past preflight to a fresh handover\n%s", st2, ok, err, runOut.String())
	}
}

// globFixture declares owned-line for v*.txt over two tracked files it
// matches, v1.txt and v[1].txt. Read as a pathspec, v[1].txt also names
// v1.txt. Only v[1].txt conflicts; a.txt, which nobody claims, makes the run
// hand the stop over.
func globFixture(t *testing.T) (ctx *Context, bump string) {
	t.Helper()
	main := committedRepo(t, minimalConf)
	writeFile(t, main, ".wt-sync.yaml", "conflicts:\n  - paths: ['v*.txt']\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n")
	writeFile(t, main, "v1.txt", "0.0.1\n")
	writeFile(t, main, "v[1].txt", "1.0.0\n")
	writeFile(t, main, "a.txt", "a\n")
	gitOut(t, main, "add", "-A")
	gitOut(t, main, "commit", "-q", "-m", "declare")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if bump, err = New(ctx, "feat/bump", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	writeFile(t, bump, "v[1].txt", "1.0.1\n")
	writeFile(t, bump, "a.txt", "branch\n")
	gitOut(t, bump, "commit", "-q", "-am", "bump")
	writeFile(t, main, "v[1].txt", "1.0.5\n")
	writeFile(t, main, "a.txt", "trunk\n")
	gitOut(t, main, "commit", "-q", "-am", "trunk bump")
	gitOut(t, main, "remote", "add", "origin", main)
	gitOut(t, main, "fetch", "-q", "origin")
	return ctx, bump
}

// A path with glob characters in it names exactly one file. The sidecar's
// Resolved is the handover's Staged, so it must hold v[1].txt's own blob, not
// v1.txt's, or resume would call the strategy's answer hand-merged.
func TestSyncHandoverRecordsTheBlobOfTheFileItNames(t *testing.T) {
	ctx, bump := globFixture(t)
	_, st := handOverNow(t, ctx, bump)
	own := gitOut(t, bump, "rev-parse", ":0:v[1].txt")
	sibling := gitOut(t, bump, "rev-parse", ":0:v1.txt")
	if own == sibling {
		t.Fatal("v1.txt and v[1].txt stage the same blob; the test is vacuous")
	}
	if got := st.Resolved["v[1].txt"]; got != own {
		t.Fatalf("handover records %s for v[1].txt; it stages %s (v1.txt is %s)", short(got), short(own), short(sibling))
	}

	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", noResumeAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "hand-merged") {
		t.Fatalf("resume called the strategy's answer hand-merged:\n%s", out.String())
	}
}
