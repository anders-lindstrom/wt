package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linearRepo builds: base -> main moves on (trunk edits) ; feature branches
// from base with the given per-commit edits. Each edit is path -> content.
func linearRepo(t *testing.T, trunkEdits []map[string]string, branchEdits []map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	write := func(edits map[string]string) {
		for p, c := range edits {
			full := filepath.Join(dir, p)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(map[string]string{"a.txt": "a\n", "b.txt": "b\n", "v.txt": "1.0.0\n"})
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	gitIn(t, dir, "branch", "feature")
	for i, e := range trunkEdits {
		write(e)
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", "trunk "+string(rune('1'+i)))
	}
	gitIn(t, dir, "checkout", "-q", "feature")
	for i, e := range branchEdits {
		write(e)
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", "branch "+string(rune('1'+i)))
	}
	gitIn(t, dir, "checkout", "-q", "main")
	return dir
}

func TestSimulateRebaseReplaysDisjointCommitsCleanly(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"b.txt": "b2\n"}, {"b.txt": "b3\n"}})
	r, err := SimulateRebase(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if r.Commits != 2 || r.Stop != nil {
		t.Errorf("replay = %+v", r)
	}
}

func TestSimulateRebaseStopsAtTheFirstConflictingCommitWithItsStages(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"b.txt": "b2\n"}, {"v.txt": "1.0.1\n"}, {"v.txt": "1.0.2\n"}})
	r, err := SimulateRebase(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || r.Stop.Index != 2 || r.Stop.Total != 3 || r.Stop.Subject != "branch 2" {
		t.Fatalf("stop = %+v", r.Stop)
	}
	if want := gitIn(t, dir, "rev-parse", "feature~1"); r.Stop.Commit != want {
		t.Errorf("stop.Commit = %s, want %s (the stopping commit)", r.Stop.Commit, want)
	}
	if len(r.Stop.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", r.Stop.Conflicts)
	}
	c := r.Stop.Conflicts[0]
	if c.Path != "v.txt" || string(c.Base) != "1.0.0\n" || string(c.Trunk) != "1.0.5\n" || string(c.Branch) != "1.0.1\n" {
		t.Errorf("stages = %q %q %q at %s", c.Base, c.Trunk, c.Branch, c.Path)
	}
}

func TestSimulateRebaseSkipsCommitsAlreadyOnTrunkLikeRebaseDoes(t *testing.T) {
	// the branch cherry-picked a trunk commit: rebase drops it, so must the simulation
	dir := linearRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"b.txt": "b2\n"}})
	gitIn(t, dir, "checkout", "-q", "feature")
	trunkTip := gitIn(t, dir, "rev-parse", "main")
	gitIn(t, dir, "cherry-pick", trunkTip)
	gitIn(t, dir, "checkout", "-q", "main")
	r, err := SimulateRebase(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if r.Commits != 1 || r.Stop != nil {
		t.Errorf("replay = %+v", r)
	}
}

func TestSimulateRebaseLeavesEveryRefAlone(t *testing.T) {
	// Disjoint edits so every commit replays cleanly and the full chain
	// runs: both commit-tree calls execute, creating real objects, which is
	// exactly the case that must not touch any ref, the index or HEAD.
	dir := linearRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"b.txt": "b2\n"}, {"b.txt": "b3\n"}})
	beforeRefs := gitIn(t, dir, "for-each-ref")
	beforeHead := gitIn(t, dir, "rev-parse", "HEAD")
	beforeSymbolic := gitIn(t, dir, "symbolic-ref", "HEAD")
	r, err := SimulateRebase(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if r.Commits != 2 || r.Stop != nil {
		t.Fatalf("replay = %+v (fixture must replay both commits cleanly to exercise commit-tree)", r)
	}
	if afterRefs := gitIn(t, dir, "for-each-ref"); afterRefs != beforeRefs {
		t.Errorf("refs changed:\n%s\n->\n%s", beforeRefs, afterRefs)
	}
	if afterHead := gitIn(t, dir, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Errorf("HEAD moved: %s -> %s", beforeHead, afterHead)
	}
	if afterSymbolic := gitIn(t, dir, "symbolic-ref", "HEAD"); afterSymbolic != beforeSymbolic {
		t.Errorf("current branch changed: %s -> %s", beforeSymbolic, afterSymbolic)
	}
	if status := gitIn(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("working tree touched: %s", status)
	}
}

func TestEndpointAndBehindAhead(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}, {"a.txt": "a2\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}})
	conflicts, err := Endpoint(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].Path != "v.txt" || string(conflicts[0].Trunk) != "1.0.5\n" {
		t.Errorf("endpoint = %+v", conflicts)
	}
	behind, ahead, err := BehindAhead(dir, "main", "feature")
	if err != nil || behind != 2 || ahead != 1 {
		t.Errorf("behind/ahead = %d/%d, %v", behind, ahead, err)
	}
	clean, err := Endpoint(dir, "main", "main")
	if err != nil || clean != nil {
		t.Errorf("clean endpoint = %+v, %v", clean, err)
	}
}

func TestSimulateRebaseMarksAModifyDeleteConflictIncomplete(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"b.txt": "b2\n"}})
	// trunk deletes a.txt after editing it; the branch edits it: modify/delete
	gitIn(t, dir, "rm", "-q", "a.txt")
	gitIn(t, dir, "commit", "-q", "-m", "trunk drops a")
	gitIn(t, dir, "checkout", "-q", "feature")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-q", "-am", "branch edits a")
	gitIn(t, dir, "checkout", "-q", "main")
	r, err := SimulateRebase(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || len(r.Stop.Conflicts) != 1 || r.Stop.Conflicts[0].Incomplete == "" {
		t.Fatalf("stop = %+v", r.Stop)
	}
	if !strings.Contains(r.Stop.Messages, "CONFLICT (modify/delete)") {
		t.Errorf("messages = %q, want the modify/delete conflict sentence", r.Stop.Messages)
	}
	if r.Stop.Messages != "" && r.Stop.Messages[0] >= '0' && r.Stop.Messages[0] <= '9' {
		t.Errorf("messages = %q, starts with a bare digit: the path-count record leaked into it", r.Stop.Messages)
	}
}

// TestSimulateRebaseMarksANonRegularModeConflictIncomplete covers the other
// half of Incomplete: not a missing stage but an entry that is not a regular
// blob. A symlink both sides repoint keeps all three stages, all present,
// all mode 120000 - the completeness check alone would call it complete.
func TestSimulateRebaseMarksANonRegularModeConflictIncomplete(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	link := filepath.Join(dir, "link")
	if err := os.Symlink("original.txt", link); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	gitIn(t, dir, "branch", "feature")

	repoint := func(target string) {
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	repoint("trunk-target.txt")
	gitIn(t, dir, "commit", "-q", "-am", "trunk repoints the symlink")
	gitIn(t, dir, "checkout", "-q", "feature")
	repoint("branch-target.txt")
	gitIn(t, dir, "commit", "-q", "-am", "branch repoints the symlink")
	gitIn(t, dir, "checkout", "-q", "main")

	r, err := SimulateRebase(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || len(r.Stop.Conflicts) != 1 {
		t.Fatalf("stop = %+v", r.Stop)
	}
	c := r.Stop.Conflicts[0]
	if c.Path != "link" || c.Incomplete == "" {
		t.Errorf("conflict = %+v, want Incomplete set for the non-regular mode", c)
	}
}
