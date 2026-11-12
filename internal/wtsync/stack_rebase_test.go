package wtsync

import (
	"bytes"
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
