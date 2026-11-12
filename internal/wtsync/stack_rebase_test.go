package wtsync

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

const stackYAML = "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n"

// stackRebase rebases s's line onto origin/main with its layers marked, and
// returns the result.
func stackRebase(t *testing.T, s *gittest.StackLine, epoch int64) (Line, Result) {
	t.Helper()
	cfg, err := LoadFromTrunk(s.Dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	line, err := ReadLine(s.Dir, gittest.StackTmp, "origin/main", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Path: s.Paths[gittest.StackTmp], Branch: gittest.StackTmp, Trunk: "origin/main", Onto: "origin/main",
		Epoch: epoch, Stack: &StackRequest{Line: line}}
	var log bytes.Buffer
	res, err := Rebase(s.Dir, cfg, req, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	return line, res
}

// One rebase of the line maps every layer to its new tip: by count and
// subject, a layer whose tip trunk already carries to its last commit left,
// and a layer trunk carries whole to the tip of the layer below it.
func TestRebaseOfALineMapsEveryLayerToItsNewTip(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	_, res := stackRebase(t, s, 11)
	if res.Left != nil || res.Restored || len(res.Layers) != 5 {
		t.Fatalf("result %+v", res)
	}
	tips, err := CheckLayers(s.Dir, res.NewTip, "origin/main", res.Layers)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range tips {
		got = append(got, l.Branch+" "+l.Mapped+" "+gitIn(t, s.Dir, "rev-list", "--count", "origin/main.."+l.New)+" "+
			gitIn(t, s.Dir, "log", "-1", "--format=%s", l.New))
	}
	want := []string{
		"s1 tip 2 feat(s1): a2.txt",
		"s2 tipDropped 4 feat(s2): b2.txt",
		"s3 tip 6 chore(s3): bump to 1.0.1",
		"s4 emptied 6 chore(s3): bump to 1.0.1",
		"s5 tip 9 feat(s5): e3.txt",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("layers:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if v := gitIn(t, s.Dir, "show", tips[2].New+":v.txt"); v != "1.0.6" {
		t.Fatalf("s3's bump is %s, want 1.0.6", v)
	}
	// The markers are the run's own refs: the branches have not moved.
	for _, l := range s.Layers {
		if gitIn(t, s.Dir, "rev-parse", l) != s.Tips[l] {
			t.Errorf("%s moved", l)
		}
	}
}

// A marker the rebase did not write, or one at a commit the plan did not
// expect, fails the check before any branch could move.
func TestCheckLayersRefusesAMarkerThatDoesNotMatchThePlan(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	_, res := stackRebase(t, s, 12)
	ref := res.Layers[2].Ref
	gitIn(t, s.Dir, "update-ref", ref, ref+"~1")
	if _, err := CheckLayers(s.Dir, res.NewTip, "origin/main", res.Layers); err == nil || !strings.Contains(err.Error(), "s3") {
		t.Fatalf("err %v, want s3's marker refused", err)
	}
	gitIn(t, s.Dir, "update-ref", "-d", ref)
	if _, err := CheckLayers(s.Dir, res.NewTip, "origin/main", res.Layers); err == nil || !strings.Contains(err.Error(), "s3") {
		t.Fatalf("err %v, want s3's missing marker refused", err)
	}
}

// A fold of the line in place: the fixups land in their targets, the tree
// of the line is the same, every layer is re-pointed, a layer whose prefix
// held a fixup loses one commit and the layers below keep their counts, and
// a check sees the commit with its fixup folded in.
func TestFoldOfALineCarriesTheFixesIntoTheirLayers(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML+"each_commit:\n  - run: grep -q fixed b1.txt\n    paths: [b1.txt]\n")
	tmp := s.Paths[gittest.StackTmp]
	commit := func(file, content, msg string) {
		gittest.WriteFile(t, filepath.Join(tmp, file), content)
		gitIn(t, tmp, "add", "-A")
		gitIn(t, tmp, "commit", "-q", "--no-verify", "-m", msg)
	}
	gittest.WriteFile(t, filepath.Join(tmp, "b1.txt"), "s2 b1.txt fixed\n")
	gitIn(t, tmp, "commit", "-q", "-am", "fixup! feat(s2): b1.txt")
	commit("f1.txt", "s6\n", "feat(s6): f1.txt")
	gitIn(t, s.Dir, "branch", "s6", gittest.StackTmp)
	commit("d1.txt", "s4 d1.txt fixed\n", "fixup! feat(s4): d1.txt")
	before := gitIn(t, tmp, "rev-parse", "HEAD^{tree}")
	cfg, err := LoadFromTrunk(s.Dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	line, err := ReadLine(s.Dir, gittest.StackTmp, "origin/main", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Path: tmp, Branch: gittest.StackTmp, Trunk: "origin/main", Onto: line.Base, Epoch: 21,
		Stack: &StackRequest{Line: line, Autosquash: true}}
	var log bytes.Buffer
	res, err := Rebase(s.Dir, cfg, req, &log)
	if err != nil || res.Left != nil {
		t.Fatalf("err %v left %+v\n%s", err, res.Left, log.String())
	}
	if after := gitIn(t, tmp, "rev-parse", "HEAD^{tree}"); after != before {
		t.Fatal("the fold changed the line's content")
	}
	if res.Checked == 0 {
		t.Fatalf("no check ran:\n%s", log.String())
	}
	tips, err := CheckLayers(s.Dir, res.NewTip, line.Base, res.Layers)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]string{}
	for _, l := range tips {
		counts[l.Branch] = gitIn(t, s.Dir, "rev-list", "--count", line.Base+".."+l.New)
	}
	want := map[string]string{"s1": "2", "s2": "5", "s3": "7", "s4": "9", "s5": "12", "s6": "13"}
	for b, n := range want {
		if counts[b] != n {
			t.Errorf("%s has %s commits from the base, want %s", b, counts[b], n)
		}
	}
	if got := gitIn(t, s.Dir, "show", tips[1].New+":b1.txt"); got != "s2 b1.txt fixed" {
		t.Errorf("s2's new tip has b1.txt %q", got)
	}
	if got := gitIn(t, s.Dir, "show", tips[3].New+":d1.txt"); got != "s4 d1.txt fixed" {
		t.Errorf("s4's new tip has d1.txt %q", got)
	}
	if tips[5].Folded != 1 || tips[4].Folded != 0 {
		t.Errorf("folded: s5 %d, s6 %d", tips[4].Folded, tips[5].Folded)
	}
}

// A conflict before a layer's boundary, resolved and resumed: the check of
// the layer's last pick still runs, though a marker follows it in the list.
func TestResumeChecksTheLastPickOfALayerBeforeItsMarker(t *testing.T) {
	t.Parallel()
	s := stackLine(t, "conflicts: []\neach_commit:\n  - run: test -f e3.txt\n    paths: [e3.txt]\n")
	cfg, err := LoadFromTrunk(s.Dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	line, err := ReadLine(s.Dir, gittest.StackTmp, "origin/main", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tmp := s.Paths[gittest.StackTmp]
	req := Request{Path: tmp, Branch: gittest.StackTmp, Trunk: "origin/main", Onto: "origin/main", Epoch: 41,
		Stack: &StackRequest{Line: line}}
	var log bytes.Buffer
	res, err := Rebase(s.Dir, cfg, req, &log)
	if err != nil || res.Left == nil {
		t.Fatalf("err %v, left %+v: want a handover at s3's bump\n%s", err, res.Left, log.String())
	}
	gittest.WriteFile(t, filepath.Join(tmp, "v.txt"), "1.0.6\n")
	gitIn(t, tmp, "add", "v.txt")
	again, err := Resume(s.Dir, cfg, req, res.OldTip, res.Safety, &log)
	if err != nil || again.Left != nil {
		t.Fatalf("err %v, left %+v\n%s", err, again.Left, log.String())
	}
	if again.Checked != 1 {
		t.Errorf("checked %d commits after the resume, want s5's last\n%s", again.Checked, log.String())
	}
}

// A fold whose target touches none of a check's paths, and whose fixup
// does, is checked, with the fixup folded in.
func TestFoldChecksAGroupByItsFixupsPathsToo(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML+"each_commit:\n  - run: test -f src/x.go\n    paths: [\"src/**\"]\n")
	tmp := s.Paths[gittest.StackTmp]
	gittest.WriteFile(t, filepath.Join(tmp, "src", "x.go"), "package x\n")
	gitIn(t, tmp, "add", "-A")
	gitIn(t, tmp, "commit", "-q", "--no-verify", "-m", "fixup! feat(s1): a1.txt")
	cfg, err := LoadFromTrunk(s.Dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	line, err := ReadLine(s.Dir, gittest.StackTmp, "origin/main", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Path: tmp, Branch: gittest.StackTmp, Trunk: "origin/main", Onto: line.Base, Epoch: 42,
		Stack: &StackRequest{Line: line, Autosquash: true}}
	var log bytes.Buffer
	res, err := Rebase(s.Dir, cfg, req, &log)
	if err != nil || res.Left != nil {
		t.Fatalf("err %v left %+v\n%s", err, res.Left, log.String())
	}
	if res.Checked != 1 {
		t.Errorf("checked %d commits, want the s1 group with its fixup\n%s", res.Checked, log.String())
	}
}
