package purge

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/commands/refs"
	"github.com/anders-lindstrom/wt/internal/commands/refs/refstest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// sweptRuns is what wt refs swept --json lists.
func sweptRuns(t *testing.T, ctx *kit.Context) []RefSweptRun {
	t.Helper()
	var out bytes.Buffer
	if err := RefsSwept(ctx, true, &out); err != nil {
		t.Fatal(err)
	}
	testkit.ValidateJSON(t, "refs-swept", out.Bytes())
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
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 1)
	testkit.LandOnMain(t, main, "done-work")
	testkit.GitIn(t, main, "config", "branch.done-work.description", "gone with the branch")
	doneTip := testkit.GitOut(t, main, "rev-parse", "done-work")
	path := provisiontest.MergedWorktree(t, ctx, "fix/login")
	wtTip := testkit.GitOut(t, main, "rev-parse", "fix_wt/login")

	r, human, err := retiretest.SweepJSON(t, ctx, retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}})
	if err != nil || r.Outcome != kit.OutcomeDone || r.RunID == nil {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	run := *r.RunID
	for branch, tip := range map[string]string{"done-work": doneTip, "fix_wt/login": wtTip} {
		it := retiretest.ResultItem(t, r, branch)
		pin := refpins.PinOf(run, refpins.RefBranch, branch)
		if retire.Deref(it.Pin) != pin || testkit.GitOut(t, main, "rev-parse", pin) != tip ||
			!slices.Equal(it.RestoreCommand, refpins.RestoreFromRun(run, branch)) {
			t.Errorf("%s = %+v", branch, it)
		}
	}
	if testkit.Exists(path) || ctx.Repo.BranchExists("done-work") || ctx.Repo.BranchExists("fix_wt/login") {
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

	res, err := refstest.RestoreJSON(t, ctx, run, refs.RestoreRefsOptions{})
	if err != nil || res.Outcome != kit.OutcomeDone {
		t.Fatalf("%v %+v", err, res)
	}
	if testkit.GitOut(t, main, "rev-parse", "refs/heads/done-work") != doneTip ||
		testkit.GitOut(t, main, "rev-parse", "refs/heads/fix_wt/login") != wtTip {
		t.Error("restore must put each branch back at its tip")
	}
	if left := testkit.GitOut(t, main, "for-each-ref", refpins.SweptPrefix); left != "" {
		t.Errorf("a run restored whole leaves nothing: %s", left)
	}
}

// wt remove moves a merged branch into a run of its own, which wt refs
// restore puts back.
func TestRemovePinsAMergedBranchAndRefsRestoresIt(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/pinned")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	r, err := retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{})
	if err != nil || r.Outcome != retire.RemoveRemoved || r.RunID == nil {
		t.Fatalf("%v %+v", err, r)
	}
	run := *r.RunID
	if s := retiretest.Step(t, r, kit.StepBranch); retire.Deref(s.Pin) != refpins.PinOf(run, refpins.RefBranch, "fix_wt/pinned") {
		t.Errorf("branch %+v", s)
	}
	if s := retiretest.Step(t, r, kit.StepWorktree); s.Pin != nil {
		t.Errorf("only the branch step has a pin: %+v", s)
	}
	runs := sweptRuns(t, ctx)
	if len(runs) != 1 || runs[0].RunID != run || !slices.Equal(sweptIDs(runs[0]), []string{"refs/heads/fix_wt/pinned"}) {
		t.Fatalf("swept = %+v", runs)
	}
	res, err := refstest.RestoreJSON(t, ctx, run, refs.RestoreRefsOptions{Only: []string{"refs/heads/fix_wt/pinned"}})
	if err != nil || res.Outcome != kit.OutcomeDone {
		t.Fatalf("%v %+v", err, res)
	}
	if testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", "refs/heads/fix_wt/pinned") != tip {
		t.Error("restore must put the branch back at its tip")
	}
}
