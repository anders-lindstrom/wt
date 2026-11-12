package syncfix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A handover in a middle layer keeps what the layers below came to. The
// run's first step fails at f3; the generator folds f1 and f3; trunk's own
// docs/d.txt conflicts with f4's, which is yours. The resume runs f4's stop
// and the ones above, keeps the step that failed at f3 owed there and above,
// records it owed on disk, and the layers move on the plan the run made.
func TestSyncResumeKeepsTheLayerStepsOfTheRunItResumes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	synccmdtest.FakeDocker(t)
	yaml := strings.Replace(gittest.FoldYAML("max-plus-patch"), "defer:\n",
		"defer:\n  - run: test ! -e src/c.txt\n    paths: [gen/**]\n    each: layer\n", 1)
	ctx, s := synccmdtest.FoldLine(t, yaml)
	main := ctx.Repo.MainRoot
	testkit.GitIn(t, s.Paths["f3"], "checkout", "--", ".")
	gittest.WriteFile(t, filepath.Join(main, "docs/d.txt"), "trunk's d\n")
	testkit.GitIn(t, main, "add", "-A")
	testkit.GitIn(t, main, "commit", "-q", "--no-verify", "-m", "docs: trunk's d")
	testkit.GitIn(t, main, "fetch", "-q", "origin")

	res, human, err := synccmdtest.StackRun(t, ctx, gittest.FoldTmp, runAt(700, &synccmd.StackOptions{}))
	if err == nil || !strings.Contains(err.Error(), "needs you") {
		t.Fatalf("err %v, want f4's conflict handed over\n%s", err, human)
	}
	if p := synccmdtest.Participant(t, res, gittest.FoldTmp); p.Result != syncrun.ResultHandedOver {
		t.Fatalf("line %+v\n%s", p, human)
	}
	tmp := s.Paths[gittest.FoldTmp]
	if err := os.WriteFile(filepath.Join(tmp, "docs/d.txt"), []byte("d, and trunk's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, tmp, "add", "docs/d.txt")
	opts := noResumeAgents()
	opts.Push = syncrun.PushNever
	resumed, err := syncResumeJSON(t, ctx, gittest.FoldTmp, opts)
	if err == nil || !strings.Contains(err.Error(), "owed") {
		t.Fatalf("resume: err %v, want the failed step owed", err)
	}
	p := synccmdtest.Participant(t, resumed, gittest.FoldTmp)
	if p.Stack == nil || p.Stack.Refusal != nil {
		why := "no stack"
		if p.Stack != nil {
			why = deref(p.Stack.Refusal)
		}
		t.Fatalf("resume: no layer moved: %s", why)
	}
	type key struct{ step, branch string }
	got := map[key]syncrun.LayerStepJSON{}
	for _, st := range p.LayerSteps {
		got[key{st.Step, st.Branch}] = st
	}
	first := "test ! -e src/c.txt"
	for _, want := range []struct {
		step, branch, result string
	}{
		{first, "f1", wtsync.LayerUnchanged}, {first, "f3", wtsync.LayerFailed}, {first, "f4", wtsync.LayerOwed}, {first, "f5", wtsync.LayerOwed},
		{"./gen.sh", "f1", wtsync.LayerFolded}, {"./gen.sh", "f3", wtsync.LayerFolded}, {"./gen.sh", "f5", wtsync.LayerCommitted},
	} {
		if st := got[key{want.step, want.branch}]; st.Result != want.result {
			t.Errorf("resume: %s at %s: %+v, want %s", want.step, want.branch, st, want.result)
		}
	}
	if st := got[key{"./gen.sh", "f4"}]; !st.Ran {
		t.Errorf("resume: the generator did not run at f4's stop: %+v", st)
	}
	owed, rerr := wtsync.ReadOwed(main, gittest.FoldTmp)
	if rerr != nil || !owed.StepSet()[first] {
		t.Errorf("resume: owed on disk %+v %v, want %q", owed, rerr, first)
	}
	for _, l := range p.Stack.Layers {
		if tip := deref(l.NewTip); tip == "" || !regenerates(t, main, tip) {
			t.Errorf("resume: %s's new tip %q does not regenerate to itself", l.Branch, tip)
		}
	}
}

// A wt sync steps run handed over at a check resumes as the same run: only
// the step it was owed runs, whatever the rebase changed, and the record is
// cleared because it ran, not because it was skipped.
func TestSyncResumeOfStepsRunsTheOwedAfterStep(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories and worktrees")
	}
	dir := t.TempDir()
	log, fail, block := filepath.Join(dir, "log"), filepath.Join(dir, "fail"), filepath.Join(dir, "block")
	yaml := "defer:\n" +
		"  - run: echo B >> " + log + "\n" +
		"  - run: echo A >> " + log + " && test ! -e " + fail + "\n    paths: [gen/**]\n" +
		"each_commit:\n  - run: test ! -e " + block + "\n"
	ctx, _ := synccmdtest.FoldLine(t, yaml)
	main := ctx.Repo.MainRoot
	steps := func(nanos int64, all bool) (syncrun.RunResult, string, error) {
		opts := runAt(nanos, nil)
		opts.Steps = &synccmd.StepsOptions{All: all}
		opts.Stack = &synccmd.StackOptions{}
		return synccmdtest.StackRun(t, ctx, gittest.FoldTmp, opts)
	}
	gittest.WriteFile(t, fail, "")
	if _, human, err := steps(900, true); err == nil || !strings.Contains(err.Error(), "owed") {
		t.Fatalf("first: err %v, want A owed\n%s", err, human)
	}
	if owed, _ := wtsync.ReadOwed(main, gittest.FoldTmp); !owed.StepSet()["echo A >> "+log+" && test ! -e "+fail] || owed.StepSet()["echo B >> "+log] {
		t.Fatalf("first: owed %+v, want A alone", owed)
	}
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	gittest.WriteFile(t, block, "")
	if _, human, err := steps(901, false); err == nil || !strings.Contains(err.Error(), "needs you") {
		t.Fatalf("steps: err %v, want the check handed over\n%s", err, human)
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	opts := noResumeAgents()
	opts.Push = syncrun.PushNever
	if _, err := syncResumeJSON(t, ctx, gittest.FoldTmp, opts); err != nil {
		t.Fatalf("resume: %v", err)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "B\nA\nA\n" {
		t.Errorf("the steps ran %q, want B and A in the first run and A alone in the resume", got)
	}
	if owed, _ := wtsync.ReadOwed(main, gittest.FoldTmp); owed != nil {
		t.Errorf("still owed: %+v", owed)
	}
}
