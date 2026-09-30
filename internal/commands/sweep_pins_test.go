package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// sweptRuns is what wt refs swept --json lists.
func sweptRuns(t *testing.T, ctx *Context) []RefSweptRun {
	t.Helper()
	var out bytes.Buffer
	if err := RefsSwept(ctx, true, &out); err != nil {
		t.Fatal(err)
	}
	validateJSON(t, "refs-swept", out.Bytes())
	var sw RefsSweptOutput
	if err := json.Unmarshal(out.Bytes(), &sw); err != nil {
		t.Fatal(err)
	}
	return sw.Runs
}

// sweptIDs is the ids of one run's pins.
func sweptIDs(run RefSweptRun) []string {
	var ids []string
	for _, r := range run.Refs {
		ids = append(ids, r.ID)
	}
	return ids
}

// Every branch a sweep deletes, with its worktree or without, is moved into
// one run: wt refs swept lists it, and wt refs restore puts each branch back
// at its tip.
func TestSweepPinsWhatItDeletesAndRefsRestoresIt(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")
	gitIn(t, main, "config", "branch.done-work.description", "gone with the branch")
	doneTip := gitOut(t, main, "rev-parse", "done-work")
	path := mergedWorktree(t, ctx, "fix/login")
	wtTip := gitOut(t, main, "rev-parse", "fix_wt/login")

	r, human, err := sweepJSON(t, ctx, SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}})
	if err != nil || r.Outcome != OutcomeDone || r.RunID == nil {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	run := *r.RunID
	for branch, tip := range map[string]string{"done-work": doneTip, "fix_wt/login": wtTip} {
		it := resultItem(t, r, branch)
		pin := pinOf(run, RefBranch, branch)
		if deref(it.Pin) != pin || gitOut(t, main, "rev-parse", pin) != tip ||
			!slices.Equal(it.RestoreCommand, restoreFromRun(run, branch)) {
			t.Errorf("%s = %+v", branch, it)
		}
	}
	if exists(path) || ctx.Repo.BranchExists("done-work") || ctx.Repo.BranchExists("fix_wt/login") {
		t.Fatal("the worktree and both branches must be gone")
	}
	if got, _ := git.Run(main, "config", "branch.done-work.description"); got != "" {
		t.Errorf("the branch's config goes with it, as git branch -D does: %q", got)
	}

	runs := sweptRuns(t, ctx)
	if len(runs) != 1 || runs[0].RunID != run ||
		!slices.Equal(sweptIDs(runs[0]), []string{"refs/heads/done-work", "refs/heads/fix_wt/login"}) {
		t.Fatalf("swept = %+v", runs)
	}

	res, err := restoreJSON(t, ctx, run, RefsRestoreOptions{})
	if err != nil || res.Outcome != OutcomeDone {
		t.Fatalf("%v %+v", err, res)
	}
	if gitOut(t, main, "rev-parse", "refs/heads/done-work") != doneTip ||
		gitOut(t, main, "rev-parse", "refs/heads/fix_wt/login") != wtTip {
		t.Error("restore must put each branch back at its tip")
	}
	if left := gitOut(t, main, "for-each-ref", SweptPrefix); left != "" {
		t.Errorf("a run restored whole leaves nothing: %s", left)
	}
}

// A sweep that deletes nothing mints no run: not one with nothing to do,
// and not one whose only branch moved after the plan was made.
func TestSweepThatDeletesNothingMintsNoRun(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	r, human, err := sweepJSON(t, ctx, SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}})
	if err != nil || r.RunID != nil {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}

	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")
	var out, buf bytes.Buffer
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{},
		Journal: NewSweepJournal(&out), Confirm: func(SweepPlan) (bool, error) {
			addWork(t, main, "done-work", 1)
			return true, nil
		}}
	if err := Sweep(ctx, opts, &buf); err == nil {
		t.Fatalf("a branch that moved is kept, which is an error:\n%s", buf.String())
	}
	validateJSON(t, "sweep", out.Bytes())
	var res SweepResult
	_ = json.Unmarshal(out.Bytes(), &res)
	if res.RunID != nil || resultItem(t, res, "done-work").Result != SweepKept || !ctx.Repo.BranchExists("done-work") {
		t.Errorf("%+v\n%s", res, buf.String())
	}
	if left := gitOut(t, main, "for-each-ref", SweptPrefix); left != "" {
		t.Errorf("nothing may be left under %s: %s", SweptPrefix, left)
	}
}

// With --quarantine, a worktree's branch is pinned by its quarantine only,
// and put back by wt restore; a branch in no worktree is still pinned in the
// run.
func TestSweepQuarantineDoesNotPinABranchTwice(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")
	mergedWorktree(t, ctx, "fix/login")
	dir := trashFor(t, ctx)
	r, human, err := sweepJSON(t, ctx, SweepOptions{NoFetch: true, Agents: []wtsync.Agent{},
		PRs: map[string]github.PR{}, Quarantine: dir})
	if err != nil || r.Outcome != OutcomeDone || r.RunID == nil {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	it := resultItem(t, r, "fix_wt/login")
	if it.Pin != nil || it.Quarantine == nil || !slices.Equal(it.RestoreCommand, []string{"wt", "restore", it.Quarantine.Dir}) {
		t.Errorf("fix_wt/login = %+v", it)
	}
	if it := resultItem(t, r, "done-work"); deref(it.Pin) != pinOf(*r.RunID, RefBranch, "done-work") {
		t.Errorf("done-work = %+v", it)
	}
	if pins := gitOut(t, main, "for-each-ref", "--format=%(refname)", SweptPrefix); pins !=
		pinOf(*r.RunID, RefBranch, "done-work")+"\n"+metaRef(*r.RunID) {
		t.Errorf("only done-work is pinned in the run: %q", pins)
	}
}

// A signal right after a branch is moved still writes the one object, with
// the run and the pin read back.
func TestSweepInterruptedAfterAMoveNamesThePin(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	for _, b := range []string{"one", "two"} {
		branchWithWork(t, main, b, 1)
		landOnMain(t, main, b)
	}
	oneTip := gitOut(t, main, "rev-parse", "one")
	t.Cleanup(func() { afterBranchMoved = nil })
	afterBranchMoved = func(name string) {
		if name == "one" {
			// What watchSignals does, but for the exit.
			currentInterruptJournal().interrupted("", "interrupted")
		}
	}
	r, human, _ := sweepJSON(t, ctx, SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}})
	if r.Outcome != OutcomeInterrupted || r.RunID == nil {
		t.Fatalf("%+v\n%s", r, human)
	}
	it := resultItem(t, r, "one")
	pin := pinOf(*r.RunID, RefBranch, "one")
	if it.Result != SweepInterrupted || !it.BranchDeleted || deref(it.Pin) != pin ||
		!slices.Equal(it.RestoreCommand, restoreFromRun(*r.RunID, "one")) {
		t.Errorf("one = %+v", it)
	}
	if gitOut(t, main, "rev-parse", pin) != oneTip {
		t.Error("the pin must hold the branch's tip")
	}
	if it := resultItem(t, r, "two"); it.Result != SweepNotRun {
		t.Errorf("two = %+v", it)
	}
}

// wt remove moves a merged branch into a run of its own, which wt refs
// restore puts back.
func TestRemovePinsAMergedBranchAndRefsRestoresIt(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/pinned")
	tip := strings.TrimSpace(gitOut(t, path, "rev-parse", "HEAD"))
	r, err := removeJSON(t, ctx, path, RemoveOptions{})
	if err != nil || r.Outcome != RemoveRemoved || r.RunID == nil {
		t.Fatalf("%v %+v", err, r)
	}
	run := *r.RunID
	if s := step(t, r, StepBranch); deref(s.Pin) != pinOf(run, RefBranch, "fix_wt/pinned") {
		t.Errorf("branch %+v", s)
	}
	if s := step(t, r, StepWorktree); s.Pin != nil {
		t.Errorf("only the branch step has a pin: %+v", s)
	}
	runs := sweptRuns(t, ctx)
	if len(runs) != 1 || runs[0].RunID != run || !slices.Equal(sweptIDs(runs[0]), []string{"refs/heads/fix_wt/pinned"}) {
		t.Fatalf("swept = %+v", runs)
	}
	res, err := restoreJSON(t, ctx, run, RefsRestoreOptions{Only: []string{"refs/heads/fix_wt/pinned"}})
	if err != nil || res.Outcome != OutcomeDone {
		t.Fatalf("%v %+v", err, res)
	}
	if gitOut(t, ctx.Repo.MainRoot, "rev-parse", "refs/heads/fix_wt/pinned") != tip {
		t.Error("restore must put the branch back at its tip")
	}
}

// An unmerged branch is renamed, not deleted: its commits keep a name, and
// no run is made.
func TestRemoveRenamesAnUnmergedBranchWithoutAPin(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/unfinished")
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "unfinished")
	r, err := removeJSON(t, ctx, path, RemoveOptions{})
	if err != nil || r.Outcome != RemoveRemoved || r.RunID != nil || r.KeepAs == nil {
		t.Fatalf("%v %+v", err, r)
	}
	if s := step(t, r, StepBranch); s.Result != "renamed" || s.Pin != nil {
		t.Errorf("branch %+v", s)
	}
	if left := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", SweptPrefix); left != "" {
		t.Errorf("a rename pins nothing: %s", left)
	}
}

// A run begun for a branch that then is not moved leaves nothing behind:
// its meta is dropped, and finish names no run.
func TestBranchPinsDropARunThatPinnedNothing(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/busy")
	tip := strings.TrimSpace(gitOut(t, path, "rev-parse", "HEAD"))
	var begun string
	pins := &branchPins{command: "wt test", begun: func(id string) { begun = id }}
	var inUse *repo.BranchInUseError
	if _, err := pins.move(ctx, "fix_wt/busy", tip); !errors.As(err, &inUse) {
		t.Fatalf("a branch a worktree uses is refused: %v", err)
	}
	if begun == "" || !ctx.Repo.BranchExists("fix_wt/busy") {
		t.Fatalf("the run is begun before the move, and the branch stays: %q", begun)
	}
	if run := pins.finish(ctx, &bytes.Buffer{}); run != "" {
		t.Errorf("finish named %s", run)
	}
	if left := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", SweptPrefix); left != "" {
		t.Errorf("nothing may be left under %s: %s", SweptPrefix, left)
	}
}

// A run id another run takes between the check and the meta write refuses
// the move, and the other run's meta is left as it was.
func TestBranchPinsLeaveARunTheyDidNotBegin(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 1)
	tip := gitOut(t, main, "rev-parse", "done-work")
	var told []string
	pins := &branchPins{command: "wt test", begun: func(id string) {
		told = append(told, id)
		if id != "" {
			// Another run writes its meta at this id first.
			if err := beginRun(main, id, time.Now(), nil); err != nil {
				t.Fatal(err)
			}
		}
	}}
	if _, err := pins.move(ctx, "done-work", tip); err == nil {
		t.Fatal("the move must refuse a run id another run holds")
	}
	if len(told) != 2 || told[1] != "" || !ctx.Repo.BranchExists("done-work") {
		t.Fatalf("told %q", told)
	}
	if run := pins.finish(ctx, &bytes.Buffer{}); run != "" {
		t.Errorf("finish named %s", run)
	}
	if meta := gitOut(t, main, "for-each-ref", "--format=%(refname)", SweptPrefix); meta != metaRef(told[0]) {
		t.Errorf("the other run's meta must stay: %q", meta)
	}
}
