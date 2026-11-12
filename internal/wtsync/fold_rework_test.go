package wtsync

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// A rename the step stages from outside its paths into them is output
// outside its paths too: the source end counts, and nothing is folded.
func TestALayerStepThatRenamesAFileIntoItsPathsIsNotFolded(t *testing.T) {
	t.Parallel()
	yaml := strings.Replace(gittest.FoldYAML("max-plus-patch"), "run: ./gen.sh", "run: ./gen.sh && git mv src/core.txt gen/core.txt", 1)
	s := foldLine(t, yaml)
	res, log := foldRebase(t, s, 35)
	if res.Left != nil || res.Restored || len(res.LayerSteps) == 0 {
		t.Fatalf("left %+v restored %v steps %+v\n%s", res.Left, res.Restored, res.LayerSteps, log)
	}
	first := res.LayerSteps[0]
	if first.Result != LayerFailed || !strings.Contains(first.Why, "outside its paths: src/core.txt") {
		t.Fatalf("f1: %+v", first)
	}
	if out := gitIn(t, s.Dir, "ls-tree", "-r", "--name-only", res.NewTip, "--", "src/core.txt"); out != "src/core.txt" {
		t.Errorf("src/core.txt is gone from the line: %q", out)
	}
}

// Two per-layer steps that fold into the same layer: the second folds into
// the commits as the first rewrote them, so both outputs reach the tip.
func TestTwoLayerStepsFoldIntoTheSameLayer(t *testing.T) {
	t.Parallel()
	index := "  - run: ls src | grep -v index.txt > src/index.txt\n    paths: [src/**]\n    each: layer\n    fold: owner\n"
	yaml := strings.Replace(gittest.FoldYAML("max-plus-patch"), "defer:\n", "defer:\n"+index, 1)
	s := foldLine(t, yaml)
	res, log := foldRebase(t, s, 36)
	if res.Left != nil || res.Restored {
		t.Fatalf("left %+v restored %v\n%s", res.Left, res.Restored, log)
	}
	tips, err := CheckLayers(s.Dir, res.NewTip, "origin/main", res.Layers)
	if err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	f1 := tips[0].New
	if got := gitIn(t, s.Dir, "show", f1+":src/index.txt"); got != "a.txt\ncore.txt\nt.txt" {
		t.Errorf("f1's index is %q: the first fold was lost\n%s", got, log)
	}
	if !regenerates(t, s.Dir, f1) {
		t.Errorf("f1's tip does not regenerate to itself\n%s", log)
	}
	folds := 0
	for _, st := range res.LayerSteps {
		if st.Branch == "f1" && st.Result == LayerFolded {
			folds++
		}
	}
	if folds != 2 {
		t.Errorf("f1: %d folds, want both steps folded\n%s", folds, log)
	}
}

// A layer whose only commit, its tip, a fold below already carries is left
// empty, and its marker lands on the tip below: the plan says so, by count and by
// subject, and the layers move.
func TestALayerEmptiedByAFoldBelowEndsWhereTheLayerBelowDoes(t *testing.T) {
	t.Parallel()
	s := foldLine(t, gittest.FoldYAML("max-plus-patch"))
	tmp := s.Paths[gittest.FoldTmp]
	commit := func(msg string) {
		gitIn(t, tmp, "add", "-A")
		gitIn(t, tmp, "commit", "-q", "--no-verify", "-m", msg)
	}
	// f6 owns the spec, then bumps after it; f7 regenerates for that bump,
	// which f6's fold already carries.
	gittest.WriteFile(t, filepath.Join(tmp, "src/e.txt"), "e\n")
	gittest.RunGen(t, tmp)
	commit("feat(f6): e")
	gittest.WriteFile(t, filepath.Join(tmp, "v.txt"), "1.1.3\n")
	commit("chore(f6): bump to 1.1.3")
	gitIn(t, s.Dir, "branch", "f6", gittest.FoldTmp)
	gittest.RunGen(t, tmp)
	commit("chore(f7): regenerate")
	gitIn(t, s.Dir, "branch", "f7", gittest.FoldTmp)
	cfg, err := LoadFromTrunk(s.Dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	line, err := ReadLine(s.Dir, gittest.FoldTmp, "origin/main", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Path: tmp, Branch: gittest.FoldTmp, Trunk: "origin/main", Onto: line.Base, Epoch: 37,
		Stack: &StackRequest{Line: line, Autosquash: true}}
	var log bytes.Buffer
	res, err := Rebase(s.Dir, cfg, req, &log)
	if err != nil || res.Left != nil || res.Restored {
		t.Fatalf("err %v left %+v\n%s", err, res.Left, log.String())
	}
	tips, err := CheckLayers(s.Dir, res.NewTip, line.Base, res.Layers)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if f6, f7 := tips[5].New, tips[6].New; f7 != f6 {
		t.Errorf("f7 ends at %s, want f6's tip %s", f7[:7], f6[:7])
	}
}
