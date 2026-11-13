package synccmd_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anlindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anlindstrom/wt/internal/commands/synccmd"
	"github.com/anlindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
	"github.com/anlindstrom/wt/internal/commands/testkit"
	"github.com/anlindstrom/wt/internal/git"
	"github.com/anlindstrom/wt/internal/gittest"
	"github.com/anlindstrom/wt/internal/wtsync"
)

// A line rebased onto an integration branch: every layer moved lands on it
// with its own commits, counted from where the line leaves the target, and
// a layer the target already carries says so by the target's name.
func TestOntoStackLineMovesItsLayersOntoTheTarget(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, _ := synccmdtest.StackLine(t, "")
	integ := synccmdtest.Branch(t, ctx, "integ", "main", map[string]string{"k.txt": "k\n"})
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, synccmdtest.Onto("integ", ""))
	if err == nil || !strings.Contains(err.Error(), "2 layers held") {
		t.Fatalf("err %v, want the two held layers\n%s", err, human)
	}
	p := synccmdtest.Participant(t, res, gittest.StackTmp)
	if p.Result != syncrun.ResultRebased || p.Stack == nil {
		t.Fatalf("line %+v\n%s", p, human)
	}
	main := ctx.Repo.MainRoot
	if !testkit.GitAncestor(t, main, integ, gittest.StackTmp) {
		t.Fatalf("the line is not on integ\n%s", human)
	}
	layers := layersOf(p.Stack)
	for b, count := range map[string]string{"s1": "2", "s2": "4", "s4": "6"} {
		l := layers[b]
		if str(l.Result) != wtsync.MoveMoved {
			t.Errorf("%s: %s (%s)", b, str(l.Result), str(l.Held))
			continue
		}
		if n := testkit.GitOut(t, main, "rev-list", "--count", "integ.."+b); n != count {
			t.Errorf("%s has %s commits above integ, want %s", b, n, count)
		}
	}
	if why := str(layers["s4"].PatchWhy); why != "integ already carries all of its commits" {
		t.Errorf("s4: %q", why)
	}
	if strings.Contains(human, "trunk already carries") || strings.Contains(human, "trunk carries") {
		t.Errorf("a layer the target carries is said to be on trunk:\n%s", human)
	}
}

// A line onto a target with a step at every layer that folds into the
// commit owning its output: at each layer's new tip on the target, the
// generator leaves the spec as it is.
func TestOntoStackLineRunsTheLayerStepsOnTheTarget(t *testing.T) {
	skipShort(t)
	synccmdtest.FakeDocker(t)
	ctx, s := synccmdtest.FoldLine(t, gittest.FoldYAML("max-plus-patch"))
	testkit.GitIn(t, s.Paths["f3"], "checkout", "--", ".")
	integ := synccmdtest.Branch(t, ctx, "integ", "main", map[string]string{"src/i.txt": "i\n"})
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.FoldTmp, synccmdtest.Onto("integ", ""))
	if err != nil {
		t.Fatalf("err %v\n%s", err, human)
	}
	p := synccmdtest.Participant(t, res, gittest.FoldTmp)
	if p.Result != syncrun.ResultRebased {
		t.Fatalf("line %+v\n%s", p, human)
	}
	main := ctx.Repo.MainRoot
	if !testkit.GitAncestor(t, main, integ, gittest.FoldTmp) {
		t.Fatalf("the line is not on integ\n%s", human)
	}
	for _, b := range []string{"f1", "f3", "f5"} {
		if !testkit.GitAncestor(t, main, integ, b) {
			t.Errorf("%s is not on integ", b)
			continue
		}
		if !regeneratesAt(t, main, b) {
			t.Errorf("at %s the generator changes the spec: the step did not run there\n%s", b, human)
		}
	}
	if len(p.LayerSteps) == 0 {
		t.Errorf("no per-layer step came to anything:\n%s", human)
	}
}

// Above an --upstream, git's list keeps the commits the target already
// carries patch for patch, and the rebase empties them: the layers are
// still counted as git replays them, and every layer moves.
func TestOntoStackLineAboveAnUpstreamCountsWhatTheTargetCarries(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, s := synccmdtest.StackLine(t, "")
	integ := synccmdtest.Branch(t, ctx, "integ", "main", map[string]string{"k.txt": "k\n"})
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, synccmdtest.Onto("integ", s.Base))
	if err == nil || !strings.Contains(err.Error(), "2 layers held") {
		t.Fatalf("err %v, want the two held layers\n%s", err, human)
	}
	p := synccmdtest.Participant(t, res, gittest.StackTmp)
	if p.Result != syncrun.ResultRebased || p.Stack == nil || p.Stack.Refusal != nil {
		t.Fatalf("line %+v stack %+v\n%s", p, p.Stack, human)
	}
	main := ctx.Repo.MainRoot
	if !testkit.GitAncestor(t, main, integ, gittest.StackTmp) {
		t.Fatalf("the line is not on integ\n%s", human)
	}
	layers := layersOf(p.Stack)
	for b, want := range map[string]struct{ mapped, count string }{
		"s1": {wtsync.MappedTip, "2"}, "s2": {wtsync.MappedTipDropped, "4"}, "s4": {wtsync.MappedEmptied, "6"},
	} {
		l := layers[b]
		if str(l.Result) != wtsync.MoveMoved || str(l.Mapped) != want.mapped {
			t.Errorf("%s: %s %s, want moved %s (%s)", b, str(l.Result), str(l.Mapped), want.mapped, str(l.Held))
			continue
		}
		if n := testkit.GitOut(t, main, "rev-list", "--count", "integ.."+b); n != want.count {
			t.Errorf("%s has %s commits above integ, want %s", b, n, want.count)
		}
	}
}

// B—C—D with a layer at C, onto C—E above B: git empties C, and the
// layer at C ends where the line's new base is. Its count is what git
// replays, and every layer moves.
func TestOntoStackLineAboveAnUpstreamCountsASharedCommitGitEmpties(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, bump := provisiontest.RunFixture(t, false)
	main := ctx.Repo.MainRoot
	b := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	commit := func(file string) string {
		gittest.WriteFile(t, filepath.Join(bump, file), file+"\n")
		testkit.GitOut(t, bump, "add", "-A")
		testkit.GitOut(t, bump, "commit", "-q", "-m", file)
		return testkit.GitOut(t, bump, "rev-parse", "HEAD")
	}
	c := commit("c.txt")
	testkit.GitOut(t, main, "branch", "lc", c)
	commit("d.txt")
	e := synccmdtest.Branch(t, ctx, "integ", c, map[string]string{"e.txt": "e\n"})
	opts := synccmdtest.Onto("integ", b)
	opts.Stack = &synccmd.StackOptions{Layers: []string{"lc"}}
	res, human, err := synccmdtest.StackRun(t, ctx, "bump", opts)
	if err != nil {
		t.Fatalf("err %v\n%s", err, human)
	}
	p := synccmdtest.Participant(t, res, "feat_wt/bump")
	l := layersOf(p.Stack)["lc"]
	if str(l.Result) != wtsync.MoveMoved || str(l.Mapped) != wtsync.MappedEmptied {
		t.Errorf("lc: %s %s, want moved and emptied (%s)\n%s", str(l.Result), str(l.Mapped), str(l.Held), human)
	}
	if got := testkit.GitOut(t, main, "rev-parse", "lc"); got != e {
		t.Errorf("lc is at %s, want integ's tip %s", git.ShortID(got, 7), git.ShortID(e, 7))
	}
	if n := testkit.GitOut(t, main, "rev-list", "--count", "integ..feat_wt/bump"); n != "1" {
		t.Errorf("%s commits above integ, want d alone", n)
	}
}

// B—C—D with a layer at C, onto a target that cherry-picked C as C′ and
// moved on with E, above B, in a run with no stop: git empties C, and the
// layer at C ends where the line's new base is.
func TestOntoStackLineAboveAnUpstreamCountsAPickTheTargetCherryPicked(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, bump := provisiontest.RunFixture(t, false)
	main := ctx.Repo.MainRoot
	b := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	commit := func(file string) string {
		gittest.WriteFile(t, filepath.Join(bump, file), file+"\n")
		testkit.GitOut(t, bump, "add", "-A")
		testkit.GitOut(t, bump, "commit", "-q", "-m", file)
		return testkit.GitOut(t, bump, "rev-parse", "HEAD")
	}
	c := commit("c.txt")
	testkit.GitOut(t, main, "branch", "lc", c)
	commit("d.txt")
	testkit.GitOut(t, main, "checkout", "-q", "-b", "integ", "main")
	testkit.GitOut(t, main, "cherry-pick", c)
	testkit.GitOut(t, main, "checkout", "-q", "main")
	e := synccmdtest.Branch(t, ctx, "integ2", "integ", map[string]string{"e.txt": "e\n"})
	testkit.GitOut(t, main, "branch", "-f", "integ", e)
	opts := synccmdtest.Onto("integ", b)
	opts.Stack = &synccmd.StackOptions{Layers: []string{"lc"}}
	res, human, err := synccmdtest.StackRun(t, ctx, "bump", opts)
	if err != nil {
		t.Fatalf("err %v\n%s", err, human)
	}
	l := layersOf(synccmdtest.Participant(t, res, "feat_wt/bump").Stack)["lc"]
	if str(l.Result) != wtsync.MoveMoved || str(l.Mapped) != wtsync.MappedEmptied {
		t.Errorf("lc: %s %s, want moved and emptied (%s)\n%s", str(l.Result), str(l.Mapped), str(l.Held), human)
	}
	if got := testkit.GitOut(t, main, "rev-parse", "lc"); got != e {
		t.Errorf("lc is at %s, want integ's tip %s", git.ShortID(got, 7), git.ShortID(e, 7))
	}
	if n := testkit.GitOut(t, main, "rev-list", "--count", "integ..feat_wt/bump"); n != "1" {
		t.Errorf("%s commits above integ, want d alone", n)
	}
}

// Picks are matched to the rebased line by author, author date and subject:
// two picks of one layer made by one author in the same second are still
// told apart, so the one the target carries is the one counted as dropped.
func TestOntoStackLineAboveAnUpstreamTellsApartPicksOfTheSameSecond(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, bump := provisiontest.RunFixture(t, false)
	main := ctx.Repo.MainRoot
	b := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	commit := func(file string) string {
		gittest.WriteFile(t, filepath.Join(bump, file), file+"\n")
		testkit.GitOut(t, bump, "add", "-A")
		testkit.GitOut(t, bump, "commit", "-q", "--date=@1700000000 +0000", "-m", file)
		return testkit.GitOut(t, bump, "rev-parse", "HEAD")
	}
	c1 := commit("c1.txt")
	c2 := commit("c2.txt")
	testkit.GitOut(t, main, "branch", "lc", c2)
	commit("d.txt")
	testkit.GitOut(t, main, "checkout", "-q", "-b", "integ", "main")
	testkit.GitOut(t, main, "cherry-pick", c1)
	testkit.GitOut(t, main, "checkout", "-q", "main")
	opts := synccmdtest.Onto("integ", b)
	opts.Stack = &synccmd.StackOptions{Layers: []string{"lc"}}
	res, human, err := synccmdtest.StackRun(t, ctx, "bump", opts)
	if err != nil {
		t.Fatalf("err %v\n%s", err, human)
	}
	l := layersOf(synccmdtest.Participant(t, res, "feat_wt/bump").Stack)["lc"]
	if str(l.Result) != wtsync.MoveMoved || str(l.Mapped) != wtsync.MappedTip {
		t.Errorf("lc: %s %s, want moved, its tip kept (%s)\n%s", str(l.Result), str(l.Mapped), str(l.Held), human)
	}
	if s := testkit.GitOut(t, main, "log", "-1", "--format=%s", "lc"); s != "c2.txt" {
		t.Errorf("lc's tip is %q, want c2.txt", s)
	}
	if n := testkit.GitOut(t, main, "rev-list", "--count", "integ..lc"); n != "1" {
		t.Errorf("%s commits on lc above integ, want c2 alone", n)
	}
}
