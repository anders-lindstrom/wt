package synccmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A run whose generator could not run leaves it owed at every layer, and on
// disk. wt sync steps <tmp> --stack then runs it, in place, at every layer
// tip, folds it into each owner and moves the layers; it replays nothing
// onto trunk, and once nothing is owed a second wt sync steps has nothing
// to do.
func TestSyncStepsRunsWhatARunLeftOwedAtEveryLayer(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	block := filepath.Join(t.TempDir(), "docker-down")
	if err := os.WriteFile(block, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	yaml := strings.Replace(gittest.FoldYAML("max-plus-patch"), "run: ./gen.sh", "run: test ! -e "+block+" && ./gen.sh", 1)
	synccmdtest.FakeDocker(t)
	ctx, s := synccmdtest.FoldLine(t, yaml)
	testkit.GitIn(t, s.Paths["f3"], "checkout", "--", ".")
	main := ctx.Repo.MainRoot
	step := "test ! -e " + block + " && ./gen.sh"

	opts := synccmdtest.NoAgents()
	opts.Now = func() time.Time { return time.Unix(0, 400) }
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.FoldTmp, opts)
	if err == nil || !strings.Contains(err.Error(), "owed") {
		t.Fatalf("err %v, want the step owed\n%s", err, human)
	}
	owed, rerr := wtsync.ReadOwed(main, gittest.FoldTmp)
	if rerr != nil || owed == nil || !owed.StepSet()[step] {
		t.Fatalf("owed on disk %+v %v", owed, rerr)
	}
	trunkTip := testkit.GitOut(t, main, "rev-parse", "origin/main")
	lineBase := testkit.GitOut(t, main, "merge-base", "origin/main", gittest.FoldTmp)
	if lineBase != trunkTip {
		t.Fatalf("the line is not on trunk")
	}
	_ = res

	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	opts = synccmdtest.NoAgents()
	opts.Now = func() time.Time { return time.Unix(0, 500) }
	opts.Steps = &synccmd.StepsOptions{}
	res, human, err = synccmdtest.StackRun(t, ctx, gittest.FoldTmp, opts)
	if err != nil {
		t.Fatalf("steps: %v\n%s", err, human)
	}
	p := synccmdtest.Participant(t, res, gittest.FoldTmp)
	if res.Command != "sync steps" || p.Result != syncrun.ResultRebased {
		t.Fatalf("steps: %s %+v\n%s", res.Command, p, human)
	}
	results := map[string]string{}
	for _, st := range p.LayerSteps {
		results[st.Branch] = st.Result
	}
	for b, want := range map[string]string{"f1": wtsync.LayerFolded, "f3": wtsync.LayerFolded, "f5": wtsync.LayerCommitted} {
		if results[b] != want {
			t.Errorf("steps: %s %s, want %s\n%s", b, results[b], want, human)
		}
	}
	if base := testkit.GitOut(t, main, "merge-base", "origin/main", gittest.FoldTmp); base != trunkTip {
		t.Errorf("steps moved the line off trunk")
	}
	for _, b := range append(append([]string{}, s.Layers...), gittest.FoldTmp) {
		if tip := testkit.GitOut(t, main, "rev-parse", b); !regeneratesAt(t, main, tip) {
			t.Errorf("steps: %s's tip does not regenerate to itself", b)
		}
	}
	if owed, _ := wtsync.ReadOwed(main, gittest.FoldTmp); owed != nil {
		t.Errorf("still owed after the steps ran: %+v", owed)
	}

	opts.Now = func() time.Time { return time.Unix(0, 600) }
	_, human, err = synccmdtest.StackRun(t, ctx, gittest.FoldTmp, opts)
	if err == nil || !strings.Contains(err.Error(), "nothing is owed") {
		t.Fatalf("steps again: err %v, want nothing owed\n%s", err, human)
	}
}

// wt sync steps works on the branch as it is here: a commit its own remote
// has and the branch does not is not taken in, and the steps still run.
func TestSyncStepsDoesNotTakeInTheBranchsRemoteCommits(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	synccmdtest.FakeDocker(t)
	ctx, s := synccmdtest.FoldLine(t, gittest.FoldYAML("max-plus-patch"))
	main := ctx.Repo.MainRoot
	tip := s.Tips[gittest.FoldTmp]
	tree := testkit.GitOut(t, main, "rev-parse", tip+"^{tree}")
	theirs := testkit.GitOut(t, main, "commit-tree", tree, "-p", tip, "-m", "theirs: on the remote only")
	testkit.GitIn(t, main, "update-ref", "refs/remotes/origin/"+gittest.FoldTmp, theirs)
	opts := synccmdtest.NoAgents()
	opts.Now = func() time.Time { return time.Unix(0, 800) }
	opts.Steps = &synccmd.StepsOptions{All: true}
	opts.Stack = nil
	var human strings.Builder
	err := synccmd.SyncRebase(ctx, []string{gittest.FoldTmp}, opts, &human)
	line := testkit.GitOut(t, main, "rev-parse", gittest.FoldTmp)
	if strings.Contains(testkit.GitOut(t, main, "log", "--format=%s", line), "theirs: on the remote only") {
		t.Fatalf("the remote's commit was taken in (err %v)\n%s", err, human.String())
	}
	if !strings.Contains(human.String(), "▸ step ./gen.sh layer 1/1") {
		t.Errorf("no step ran at %s (err %v)\n%s", line[:7], err, human.String())
	}
}
