package retire_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A sweep that deletes nothing mints no run: not one with nothing to do,
// and not one whose only branch moved after the plan was made.
func TestSweepThatDeletesNothingMintsNoRun(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	r, human, err := retiretest.SweepJSON(t, ctx, retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}})
	if err != nil || r.RunID != nil {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}

	testkit.BranchWithWork(t, main, "done-work", 1)
	testkit.LandOnMain(t, main, "done-work")
	var out, buf bytes.Buffer
	opts := retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{},
		Journal: retire.NewSweepJournal(&out), Confirm: func(retire.SweepPlan) (bool, error) {
			testkit.AddWork(t, main, "done-work", 1)
			return true, nil
		}}
	if err := retire.Sweep(ctx, opts, &buf); err == nil {
		t.Fatalf("a branch that moved is kept, which is an error:\n%s", buf.String())
	}
	testkit.ValidateJSON(t, "sweep", out.Bytes())
	var res retire.SweepResult
	_ = json.Unmarshal(out.Bytes(), &res)
	if res.RunID != nil || retiretest.ResultItem(t, res, "done-work").Result != retire.SweepKept || !ctx.Repo.BranchExists("done-work") {
		t.Errorf("%+v\n%s", res, buf.String())
	}
	if left := testkit.GitOut(t, main, "for-each-ref", refpins.SweptPrefix); left != "" {
		t.Errorf("nothing may be left under %s: %s", refpins.SweptPrefix, left)
	}
}

// With --move-to, a worktree's branch is pinned by its quarantine only,
// and put back by wt restore; a branch in no worktree is still pinned in the
// run.
func TestSweepQuarantineDoesNotPinABranchTwice(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 1)
	testkit.LandOnMain(t, main, "done-work")
	provisiontest.MergedWorktree(t, ctx, "fix/login")
	dir := testkit.TrashFor(t, ctx)
	r, human, err := retiretest.SweepJSON(t, ctx, retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{},
		PRs: map[string]github.PR{}, Quarantine: dir})
	if err != nil || r.Outcome != kit.OutcomeDone || r.RunID == nil {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	it := retiretest.ResultItem(t, r, "fix_wt/login")
	if it.Pin != nil || it.Quarantine == nil || !slices.Equal(it.RestoreCommand, []string{"wt", "restore", it.Quarantine.Dir}) {
		t.Errorf("fix_wt/login = %+v", it)
	}
	if it := retiretest.ResultItem(t, r, "done-work"); retire.Deref(it.Pin) != refpins.PinOf(*r.RunID, refpins.RefBranch, "done-work") {
		t.Errorf("done-work = %+v", it)
	}
	if pins := testkit.GitOut(t, main, "for-each-ref", "--format=%(refname)", refpins.SweptPrefix); pins !=
		refpins.PinOf(*r.RunID, refpins.RefBranch, "done-work")+"\n"+refpins.MetaRef(*r.RunID) {
		t.Errorf("only done-work is pinned in the run: %q", pins)
	}
}

// A signal right after a branch is moved still writes the one object, with
// the run and the pin read back.
func TestSweepInterruptedAfterAMoveNamesThePin(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	for _, b := range []string{"one", "two"} {
		testkit.BranchWithWork(t, main, b, 1)
		testkit.LandOnMain(t, main, b)
	}
	oneTip := testkit.GitOut(t, main, "rev-parse", "one")
	t.Cleanup(func() { refpins.AfterBranchMoved = nil })
	refpins.AfterBranchMoved = func(name string) {
		if name == "one" {
			// What watchSignals does, but for the exit.
			kit.CurrentInterruptJournal().Interrupted("", "interrupted")
		}
	}
	r, human, _ := retiretest.SweepJSON(t, ctx, retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}})
	if r.Outcome != kit.OutcomeInterrupted || r.RunID == nil {
		t.Fatalf("%+v\n%s", r, human)
	}
	it := retiretest.ResultItem(t, r, "one")
	pin := refpins.PinOf(*r.RunID, refpins.RefBranch, "one")
	if it.Result != retire.SweepInterrupted || !it.BranchDeleted || retire.Deref(it.Pin) != pin ||
		!slices.Equal(it.RestoreCommand, refpins.RestoreFromRun(*r.RunID, "one")) {
		t.Errorf("one = %+v", it)
	}
	if testkit.GitOut(t, main, "rev-parse", pin) != oneTip {
		t.Error("the pin must hold the branch's tip")
	}
	if it := retiretest.ResultItem(t, r, "two"); it.Result != retire.SweepNotRun {
		t.Errorf("two = %+v", it)
	}
}

// An unmerged branch is renamed, not deleted: its commits keep a name, and
// no run is made.
func TestRemoveRenamesAnUnmergedBranchWithoutAPin(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/unfinished")
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "unfinished")
	r, err := retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{})
	if err != nil || r.Outcome != retire.RemoveRemoved || r.RunID != nil || r.KeepAs == nil {
		t.Fatalf("%v %+v", err, r)
	}
	if s := retiretest.Step(t, r, kit.StepBranch); s.Result != "renamed" || s.Pin != nil {
		t.Errorf("branch %+v", s)
	}
	if left := testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", refpins.SweptPrefix); left != "" {
		t.Errorf("a rename pins nothing: %s", left)
	}
}
