package synccmd_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/commands/synccmd"
	"github.com/anlindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
	"github.com/anlindstrom/wt/internal/commands/testkit"
	"github.com/anlindstrom/wt/internal/gittest"
)

// worktreeStack is a FoldLine whose layers are each in a worktree of their
// own, every one clean: the stack mode without --stack, where each branch is
// rebased onto its parent's new tip in a rebase of its own.
func worktreeStack(t *testing.T, yaml string) (*kit.Context, *gittest.FoldLine) {
	t.Helper()
	synccmdtest.FakeDocker(t)
	ctx, s := synccmdtest.FoldLine(t, yaml)
	testkit.GitIn(t, s.Paths["f3"], "checkout", "--", ".")
	parent := filepath.Dir(s.Paths["f1"])
	for _, b := range []string{"f2", "f4", "f5"} {
		path := filepath.Join(parent, b)
		testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", path, b)
		s.Paths[b] = path
	}
	return ctx, s
}

func regeneratesAt(t *testing.T, dir, tip string) bool {
	t.Helper()
	scratch := t.TempDir()
	archive := exec.Command("sh", "-c", "git archive "+tip+" | tar -x -C "+scratch)
	archive.Dir = dir
	if out, err := archive.CombinedOutput(); err != nil {
		t.Fatalf("archive %s: %v\n%s", tip, err, out)
	}
	gittest.RunGen(t, scratch)
	got, err := os.ReadFile(filepath.Join(scratch, "gen", "spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(got) == testkit.GitOut(t, dir, "show", tip+":gen/spec.json")+"\n"
}

// What a declaration without each: layer or fold: owner does in the stack
// mode of worktrees: the step runs once per branch after its rebase and
// commits on top, and the child is rebased onto that commit. f3's own
// commit of the spec then conflicts with f1's regenerated spec, nothing
// claims it, and as a stack parent f3 is restored with everything above it.
func TestAStackOfWorktreesWithAStepAfterTheRebaseStopsAtTheChildsSpec(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	yaml := strings.Replace(gittest.FoldYAML("max-plus-patch"), "    each: layer\n    fold: owner\n", "", 1)
	ctx, s := worktreeStack(t, yaml)
	var out bytes.Buffer
	opts := synccmdtest.NoAgents()
	opts.Journal = syncrun.NewSyncRunJournal(&bytes.Buffer{}, "sync rebase")
	err := synccmd.SyncRebase(ctx, []string{gittest.FoldTmp}, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "f3 (restored)") {
		t.Fatalf("err %v, want f3 restored\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `stop 1/2 "feat(f3): c"  ✗ gen/spec.json unclaimed`) {
		t.Errorf("f3 did not stop at its own spec:\n%s", out.String())
	}
	main := ctx.Repo.MainRoot
	if subject := testkit.GitOut(t, main, "log", "-1", "--format=%s", "f1"); subject != "chore: regenerate the spec" {
		t.Errorf("f1's tip is %q, want the step's commit on top", subject)
	}
	if testkit.GitOut(t, main, "rev-parse", "f3") != s.Tips["f3"] {
		t.Error("f3 moved")
	}
}

// With fold: owner each branch of a stack of worktrees is a layer: its fold
// stop is at the end of its own rebase, a child rebased onto a folded parent
// takes its own side of the spec for its stop to write again, and every
// branch's tip passes the check CI runs on its PR.
func TestAStackOfWorktreesFoldsEachBranchIntoItsOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := worktreeStack(t, gittest.FoldYAML("max-plus-patch"))
	var out, js bytes.Buffer
	opts := synccmdtest.NoAgents()
	opts.Journal = syncrun.NewSyncRunJournal(&js, "sync rebase")
	if err := synccmd.SyncRebase(ctx, []string{gittest.FoldTmp}, opts, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	res := synccmdtest.DecodeSyncRun(t, js.Bytes())
	main := ctx.Repo.MainRoot
	for _, b := range append(append([]string{}, s.Layers...), gittest.FoldTmp) {
		p := synccmdtest.Participant(t, res, b)
		if p.Result != syncrun.ResultRebased {
			t.Errorf("%s: %s %s", b, p.Result, str(p.Reason))
		}
		if tip := testkit.GitOut(t, main, "rev-parse", b); !regeneratesAt(t, main, tip) {
			t.Errorf("%s's tip %s does not regenerate to itself\n%s", b, tip[:7], out.String())
		}
	}
	for b, subject := range s.Owners {
		p := synccmdtest.Participant(t, res, b)
		if len(p.LayerSteps) != 1 || p.LayerSteps[0].Result != "folded" || str(p.LayerSteps[0].OwnerSubject) != subject {
			t.Errorf("%s: %+v, want folded into %q", b, p.LayerSteps, subject)
		}
	}
}

// fold: owner without each: layer folds into the whole rebase as one layer,
// which on a --stack line would rewrite commits below the layers' markers:
// a --stack run refuses it before anything moves.
func TestAStackRunRefusesAFoldThatIsNotPerLayer(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	synccmdtest.FakeDocker(t)
	ctx, s := synccmdtest.FoldLine(t, strings.Replace(gittest.FoldYAML("max-plus-patch"), "    each: layer\n", "", 1))
	_, human, err := synccmdtest.StackRun(t, ctx, gittest.FoldTmp, synccmdtest.NoAgents())
	if err == nil || !strings.Contains(err.Error(), "fold: owner without each: layer") {
		t.Fatalf("err %v, want the fold refused\n%s", err, human)
	}
	main := ctx.Repo.MainRoot
	for _, b := range append(append([]string{}, s.Layers...), gittest.FoldTmp) {
		if testkit.GitOut(t, main, "rev-parse", b) != s.Tips[b] {
			t.Errorf("%s moved", b)
		}
	}
}
