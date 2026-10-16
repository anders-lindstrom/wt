package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// stoppedRebase starts a real rebase of feature onto main in a worktree and
// returns the worktree path, stopped at the first conflict.
func stoppedRebase(t *testing.T, trunkEdits, branchEdits []map[string]string) (dir, wt string) {
	t.Helper()
	dir = linearRepo(t, trunkEdits, branchEdits)
	w := featureWorktree(t, dir)
	if _, err := gittest.Try(t, w.Path, "rebase", "--no-update-refs", "--no-gpg-sign", "main"); err == nil {
		t.Fatal("rebase did not stop")
	}
	return dir, w.Path
}

func TestStagedConflictsReadsTheThreeStagesOfEachUnmergedPath(t *testing.T) {
	t.Parallel()
	_, wt := stoppedRebase(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "a2\n"}})
	cs, err := StagedConflicts(wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Path != "v.txt" {
		t.Fatalf("conflicts %+v", cs)
	}
	c := cs[0]
	if string(c.Base) != "1.0.0\n" || string(c.Trunk) != "1.0.5\n" || string(c.Branch) != "1.0.1\n" || c.Incomplete != "" {
		t.Fatalf("stages %q %q %q incomplete %q", c.Base, c.Trunk, c.Branch, c.Incomplete)
	}
}

func TestStagedConflictsMarksAModifyDeleteIncomplete(t *testing.T) {
	t.Parallel()
	dir := linearRepo(t, []map[string]string{{"v.txt": "1.0.5\n"}}, nil)
	w := featureWorktree(t, dir)
	gitIn(t, w.Path, "rm", "-q", "v.txt")
	gitIn(t, w.Path, "commit", "-q", "-m", "drop v")
	if _, err := gittest.Try(t, w.Path, "rebase", "--no-gpg-sign", "main"); err == nil {
		t.Fatal("rebase did not stop")
	}
	cs, err := StagedConflicts(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Incomplete == "" {
		t.Fatalf("conflicts %+v", cs)
	}
}

func TestRebaseProgressReadsTheStopIndexAndSubject(t *testing.T) {
	t.Parallel()
	_, wt := stoppedRebase(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"a.txt": "a2\n"}, {"v.txt": "1.0.1\n"}})
	ok, err := RebaseInProgress(wt)
	if err != nil || !ok {
		t.Fatalf("in progress %v %v", ok, err)
	}
	p, err := RebaseProgress(wt)
	if err != nil {
		t.Fatal(err)
	}
	if p.Index != 2 || p.Total != 2 || !strings.HasPrefix(p.Subject, "branch 2") || len(p.Commit) < 7 {
		t.Fatalf("progress %+v", p)
	}
}

// An edit stop is after its pick when git committed the pick and when it
// dropped it as empty, and not at a conflict, staged or not. From git 2.56 a
// dropped pick leaves no amend marker, so the test takes it away whatever
// git runs it.
func TestRebaseProgressSaysWhetherAnEditStopIsAfterItsPick(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		trunk   map[string]string
		resolve bool
		applied bool
	}{
		"committed":        {trunk: map[string]string{"a.txt": "a2\n"}, applied: true},
		"dropped as empty": {trunk: map[string]string{"v.txt": "1.0.1\n"}, applied: true},
		"conflict":         {trunk: map[string]string{"v.txt": "1.0.5\n"}},
		"conflict staged":  {trunk: map[string]string{"v.txt": "1.0.5\n"}, resolve: true},
	} {
		t.Run(name, func(t *testing.T) {
			dir := linearRepo(t, []map[string]string{tc.trunk}, []map[string]string{{"v.txt": "1.0.1\n"}})
			wt := featureWorktree(t, dir).Path
			_, _ = gittest.Try(t, wt, "-c", "sequence.editor=sed -i.bak s/^pick/edit/", "rebase", "--interactive",
				"--empty=drop", "--reapply-cherry-picks", "--no-update-refs", "--no-gpg-sign", "main")
			rm, err := rebaseDir(wt)
			if err != nil {
				t.Fatal(err)
			}
			if tc.applied && gitIn(t, wt, "rev-parse", "HEAD") == gitIn(t, wt, "rev-parse", "main") {
				if err := os.Remove(filepath.Join(rm, "amend")); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
			if tc.resolve {
				gittest.WriteFile(t, filepath.Join(wt, "v.txt"), "1.0.6\n")
				gitIn(t, wt, "add", "--", "v.txt")
			}
			p, err := RebaseProgress(wt)
			if err != nil {
				t.Fatal(err)
			}
			if p.Command != "edit" || p.Applied != tc.applied {
				t.Fatalf("progress %+v, want applied %v", p, tc.applied)
			}
		})
	}
}

func TestApplyWritesTheContentKeepsTheModeAndStagesIt(t *testing.T) {
	t.Parallel()
	_, wt := stoppedRebase(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}})
	if err := os.Chmod(filepath.Join(wt, "v.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	cs, _ := StagedConflicts(wt)
	if err := Apply(wt, cs[0], []byte("1.0.6\n")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.6\n" {
		t.Fatalf("file %q", got)
	}
	if st, _ := os.Stat(filepath.Join(wt, "v.txt")); st.Mode()&0o100 == 0 {
		t.Fatal("Apply changed the file's mode")
	}
	if out := gitIn(t, wt, "ls-files", "-u", "--", "v.txt"); out != "" {
		t.Fatalf("still unmerged: %s", out)
	}
	if out := gitIn(t, wt, "diff", "--cached", "--name-only"); out != "v.txt" {
		t.Fatalf("staged %q", out)
	}
}

func TestResolveConflictReturnsTheStrategysBytes(t *testing.T) {
	t.Parallel()
	dir := linearRepo(t, []map[string]string{{"v.txt": "1.0.5\n"}}, []map[string]string{{"v.txt": "1.0.1\n"}})
	c := Conflict{Path: "v.txt", Base: []byte("1.0.0\n"), Trunk: []byte("1.0.5\n"), Branch: []byte("1.0.1\n")}
	r, err := resolveConflict(dir, "main", triageCfg(t), c, "")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Outcome.Resolved || string(r.Content) != "1.0.6\n" || r.InPlace {
		t.Fatalf("resolution %+v", r)
	}
}
