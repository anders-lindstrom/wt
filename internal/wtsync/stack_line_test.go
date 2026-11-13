package wtsync

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anlindstrom/wt/internal/gittest"
)

// stackLine builds gittest's StackLine in a fresh directory, declaring yaml
// on trunk.
func stackLine(t *testing.T, yaml string) *gittest.StackLine {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := gittest.NewRepo(t, parent, "repo")
	gitIn(t, dir, "config", "core.hooksPath", filepath.Join(dir, ".git", "hooks"))
	return gittest.NewStackLine(t, dir, parent, yaml)
}

func TestReadLineFindsTheLayerBranchesBottomFirst(t *testing.T) {
	t.Parallel()
	s := stackLine(t, "")
	gitIn(t, s.Dir, "branch", "backup/s3", s.Tips["s3"])
	line, err := ReadLine(s.Dir, gittest.StackTmp, "origin/main", nil, []string{"backup/*"})
	if err != nil {
		t.Fatal(err)
	}
	if line.Base != s.Base || line.Head != s.Tips[gittest.StackTmp] || len(line.Commits) != 12 {
		t.Fatalf("line %+v", line)
	}
	var got []string
	for _, l := range line.Layers {
		got = append(got, l.Branch+"="+strconv.Itoa(l.Count)+" "+l.Subject)
	}
	want := []string{"s1=2 feat(s1): a2.txt", "s2=5 feat(s2): b3.txt", "s3=7 chore(s3): bump to 1.0.1",
		"s4=9 feat(s4): d2.txt", "s5=12 feat(s5): e3.txt"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("layers:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(line.Skipped) != 1 || line.Skipped[0].Branch != "backup/s3" {
		t.Fatalf("skipped %+v", line.Skipped)
	}
}

func TestReadLineTakesOnlyTheNamedLayers(t *testing.T) {
	t.Parallel()
	s := stackLine(t, "")
	gitIn(t, s.Dir, "branch", "backup/s3", s.Tips["s3"])
	line, err := ReadLine(s.Dir, gittest.StackTmp, "origin/main", []string{"s4", "backup/s3"}, []string{"backup/*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(line.Layers) != 2 || line.Layers[0].Branch != "backup/s3" || line.Layers[1].Branch != "s4" || !line.Layers[0].Named {
		t.Fatalf("layers %+v", line.Layers)
	}
	gitIn(t, s.Dir, "branch", "elsewhere", s.Base)
	if _, err := ReadLine(s.Dir, gittest.StackTmp, "origin/main", []string{"elsewhere"}, nil); err == nil ||
		!strings.Contains(err.Error(), "elsewhere") || !strings.Contains(err.Error(), "not on the line") {
		t.Fatalf("err %v, want a named branch off the line refused", err)
	}
}

func TestReadLineRefusesAMergeCommitOnTheLine(t *testing.T) {
	t.Parallel()
	s := stackLine(t, "")
	tmp := s.Paths[gittest.StackTmp]
	gitIn(t, s.Dir, "branch", "side", s.Tips["s1"])
	gitIn(t, s.Dir, "worktree", "add", "-q", s.Dir+"-side", "side")
	gittest.WriteFile(t, filepath.Join(s.Dir+"-side", "side.txt"), "side\n")
	gitIn(t, s.Dir+"-side", "add", "-A")
	gitIn(t, s.Dir+"-side", "commit", "-q", "-m", "side")
	gitIn(t, tmp, "merge", "-q", "--no-edit", "side")
	_, err := ReadLine(s.Dir, gittest.StackTmp, "origin/main", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "merge commit") {
		t.Fatalf("err %v, want the merge refused", err)
	}
}

func TestReadLineRefusesALayerWhoseTipIsAFixup(t *testing.T) {
	t.Parallel()
	s := stackLine(t, "")
	tmp := s.Paths[gittest.StackTmp]
	gittest.WriteFile(t, filepath.Join(tmp, "a1.txt"), "fixed\n")
	gitIn(t, tmp, "commit", "-q", "-am", "fixup! feat(s1): a1.txt")
	gitIn(t, s.Dir, "branch", "s6", gittest.StackTmp)
	_, err := ReadLine(s.Dir, gittest.StackTmp, "origin/main", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "s6") || !strings.Contains(err.Error(), "fixup!") {
		t.Fatalf("err %v, want s6 refused for its fixup tip", err)
	}
}
