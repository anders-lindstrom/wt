package wtsync

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anlindstrom/wt/internal/gittest"
)

func foldLine(t *testing.T, yaml string) *gittest.FoldLine {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := gittest.NewRepo(t, parent, "repo")
	gitIn(t, dir, "config", "core.hooksPath", filepath.Join(dir, ".git", "hooks"))
	return gittest.NewFoldLine(t, dir, parent, yaml)
}

// regenerates reports whether the generator, run at tip, leaves gen/ as tip
// has it: the check CI runs on the layer's PR.
func regenerates(t *testing.T, dir, tip string) bool {
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
	return string(got) == gitIn(t, dir, "show", tip+":gen/spec.json")+"\n"
}

// commitBySubject is the commit on the line ending at tip whose subject is
// subject.
func commitBySubject(t *testing.T, dir, tip, subject string) string {
	t.Helper()
	for _, l := range strings.Split(gitIn(t, dir, "log", "--format=%H %s", tip), "\n") {
		if sha, s, _ := strings.Cut(l, " "); s == subject {
			return sha
		}
	}
	t.Fatalf("no commit %q under %s", subject, tip)
	return ""
}

func stepsByBranch(steps []LayerStep) map[string]LayerStep {
	out := map[string]LayerStep{}
	for _, s := range steps {
		out[s.Branch] = s
	}
	return out
}

// One rebase of the line runs the generator at every layer tip, bottom
// first: a layer with a commit that owns the spec has the output folded into
// that commit, and the commits after it carry it too; a layer with drift and
// no owner gets the step's commit; a layer with no drift gets nothing. Every
// layer tip then regenerates to itself and the markers land on the folded
// tips. The line's own commits above its top layer are one more layer.
func TestRebaseOfALineFoldsEachLayersOutputIntoItsOwner(t *testing.T) {
	t.Parallel()
	s := foldLine(t, gittest.FoldYAML("max-plus-patch"))
	tmp := s.Paths[gittest.FoldTmp]
	cfg, err := LoadFromTrunk(s.Dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	line, err := ReadLine(s.Dir, gittest.FoldTmp, "origin/main", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Path: tmp, Branch: gittest.FoldTmp, Trunk: "origin/main", Onto: "origin/main", Epoch: 31,
		Stack: &StackRequest{Line: line}}
	var log bytes.Buffer
	res, err := Rebase(s.Dir, cfg, req, &log)
	if err != nil || res.Left != nil || res.Restored {
		t.Fatalf("err %v left %+v restored %v\n%s", err, res.Left, res.Restored, log.String())
	}
	if !strings.Contains(log.String(), "▸ step ./gen.sh layer 1/6") || !strings.Contains(log.String(), "▸ step ./gen.sh layer 6/6") {
		t.Errorf("no progress line per layer:\n%s", log.String())
	}
	tips, err := CheckLayers(s.Dir, res.NewTip, "origin/main", res.Layers)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	for _, l := range tips {
		if !regenerates(t, s.Dir, l.New) {
			t.Errorf("%s's new tip %s does not regenerate to itself", l.Branch, l.New[:7])
		}
	}
	steps := stepsByBranch(res.LayerSteps)
	for b, want := range map[string]string{"f1": LayerFolded, "f2": LayerUnchanged, "f3": LayerFolded, "f4": LayerUnchanged, "f5": LayerCommitted, gittest.FoldTmp: LayerUnchanged} {
		if got := steps[b]; got.Result != want || !got.Ran {
			t.Errorf("%s: %+v, want %s", b, got, want)
		}
	}
	for b, subject := range s.Owners {
		st := steps[b]
		if st.OwnerSubject != subject || st.Proof != ProofHeld {
			t.Errorf("%s folded into %q (proof %q), want %q", b, st.OwnerSubject, st.Proof, subject)
		}
		tip := tips[map[string]int{"f1": 0, "f3": 2}[b]].New
		owner := commitBySubject(t, s.Dir, tip, subject)
		if st.Owner != owner {
			t.Errorf("%s: owner %s, want %s", b, st.Owner, owner)
		}
		if gitIn(t, s.Dir, "show", owner+":gen/spec.json") == gitIn(t, s.Dir, "show", s.Tips[b]+":gen/spec.json") {
			t.Errorf("%s's owner holds the spec it had before the rebase", b)
		}
		// The bump after the owner changes v.txt and the spec it drifts,
		// nothing else.
		bump := tip
		if got := gitIn(t, s.Dir, "diff", "--name-only", owner, bump); got != "v.txt" {
			t.Errorf("%s: owner to tip changes %q, want v.txt alone", b, got)
		}
	}
	if subj := gitIn(t, s.Dir, "log", "-1", "--format=%s", tips[4].New); subj != "chore: regenerate the spec" {
		t.Errorf("f5's tip is %q, want the step's commit", subj)
	}
}

// foldRebase rebases s's line onto origin/main with its layers marked.
func foldRebase(t *testing.T, s *gittest.FoldLine, epoch int64) (Result, string) {
	t.Helper()
	cfg, err := LoadFromTrunk(s.Dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	line, err := ReadLine(s.Dir, gittest.FoldTmp, "origin/main", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Path: s.Paths[gittest.FoldTmp], Branch: gittest.FoldTmp, Trunk: "origin/main", Onto: "origin/main", Epoch: epoch,
		Stack: &StackRequest{Line: line}}
	var log bytes.Buffer
	res, err := Rebase(s.Dir, cfg, req, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	return res, log.String()
}

// A pick above a fold that changes the generated file conflicts with the
// folded output. Nothing claims the file, so the commit's side is taken and
// said, and the stop at the end of its layer writes it again: nothing is
// handed to a person and nothing is restored.
func TestRebaseTakesTheCommitsSideOfAFileALayerStepWrites(t *testing.T) {
	t.Parallel()
	s := foldLine(t, gittest.FoldYAML("max-plus-patch"))
	res, log := foldRebase(t, s, 32)
	if res.Left != nil || res.Restored {
		t.Fatalf("left %+v restored %v\n%s", res.Left, res.Restored, log)
	}
	var settled []string
	for _, st := range res.Stops {
		for _, f := range st.Files {
			if f.Path == "gen/spec.json" && f.Resolved && f.Strategy == StrategyEachLayer {
				settled = append(settled, st.Subject)
			}
		}
	}
	if strings.Join(settled, ",") != "feat(f3): c" {
		t.Fatalf("settled for the layer's stop at %v, want feat(f3): c\n%s", settled, log)
	}
	if !strings.Contains(log, "took the commit's side (unclaimed); ./gen.sh writes it again at the end of the layer") {
		t.Errorf("the stop does not say what it took:\n%s", log)
	}
}

// A step that fails at a layer leaves the layers below it folded and
// proved, and is owed at that layer and every one above, where it does not
// run again; the rebase goes on to the end and every marker lands.
func TestALayerStepThatFailsIsOwedFromThatLayerUp(t *testing.T) {
	t.Parallel()
	yaml := strings.Replace(gittest.FoldYAML("max-plus-patch"), "run: ./gen.sh", "run: test ! -e src/c.txt && ./gen.sh", 1)
	s := foldLine(t, yaml)
	res, log := foldRebase(t, s, 33)
	if res.Left != nil || res.Restored {
		t.Fatalf("left %+v restored %v\n%s", res.Left, res.Restored, log)
	}
	if _, err := CheckLayers(s.Dir, res.NewTip, "origin/main", res.Layers); err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	got := map[string]string{}
	ran := map[string]bool{}
	for _, st := range res.LayerSteps {
		got[st.Branch], ran[st.Branch] = st.Result, st.Ran
	}
	want := map[string]string{"f1": LayerFolded, "f2": LayerUnchanged, "f3": LayerFailed, "f4": LayerOwed, "f5": LayerOwed, gittest.FoldTmp: LayerOwed}
	for b, w := range want {
		if got[b] != w {
			t.Errorf("%s: %s, want %s\n%s", b, got[b], w, log)
		}
	}
	if ran["f4"] || ran["f5"] {
		t.Errorf("the step ran again above the layer it failed at")
	}
	if !regenerates(t, s.Dir, res.Layers[0].Ref) {
		t.Error("f1 is not folded")
	}
	if regenerates(t, s.Dir, res.Layers[2].Ref) {
		t.Error("f3 regenerates, yet the step failed there")
	}
}

// Output that changes a tracked file outside the step's paths is never
// folded: the step fails at that layer and the worktree is put back.
func TestALayerStepThatChangesFilesOutsideItsPathsIsNotFolded(t *testing.T) {
	t.Parallel()
	yaml := strings.Replace(gittest.FoldYAML("max-plus-patch"), "run: ./gen.sh", "run: ./gen.sh && echo touched >> src/core.txt", 1)
	s := foldLine(t, yaml)
	res, log := foldRebase(t, s, 34)
	if res.Left != nil || res.Restored || len(res.LayerSteps) == 0 {
		t.Fatalf("left %+v restored %v steps %+v\n%s", res.Left, res.Restored, res.LayerSteps, log)
	}
	first := res.LayerSteps[0]
	if first.Result != LayerFailed || !strings.Contains(first.Why, "outside its paths: src/core.txt") {
		t.Fatalf("f1: %+v", first)
	}
	if strings.Contains(gitIn(t, s.Dir, "show", res.NewTip+":src/core.txt"), "touched") {
		t.Error("the change outside the step's paths was kept")
	}
}
