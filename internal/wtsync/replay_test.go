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
	return repoWith(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n", "v.txt": "1.0.0\n"}, trunkEdits, branchEdits)
}

// repoWith builds: base (the given files) -> main moves on (trunk edits) ;
// feature branches from base with the given per-commit edits. Each edit is
// path -> content. The repository's identity is configured explicitly so a
// production-path commit made in it does not depend on the ambient identity.
func repoWith(t *testing.T, base map[string]string, trunkEdits []map[string]string, branchEdits []map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	gitIn(t, dir, "config", "user.name", "t")
	gitIn(t, dir, "config", "user.email", "t@example.com")
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
	write(base)
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
	r, err := SimulateRebase(dir, "main", "feature", nil)
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
	r, err := SimulateRebase(dir, "main", "feature", nil)
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
	r, err := SimulateRebase(dir, "main", "feature", nil)
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
	r, err := SimulateRebase(dir, "main", "feature", nil)
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
	r, err := SimulateRebase(dir, "main", "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || len(r.Stop.Conflicts) != 1 || r.Stop.Conflicts[0].Incomplete != "one side deleted or renamed it" {
		t.Fatalf("stop = %+v", r.Stop)
	}
	if !strings.Contains(r.Stop.Messages, "CONFLICT (modify/delete)") {
		t.Errorf("messages = %q, want the modify/delete conflict sentence", r.Stop.Messages)
	}
	if r.Stop.Messages != "" && r.Stop.Messages[0] >= '0' && r.Stop.Messages[0] <= '9' {
		t.Errorf("messages = %q, starts with a bare digit: the path-count record leaked into it", r.Stop.Messages)
	}
}

// TestSimulateRebaseMarksAnAddAddConflictAsBothSidesAddedIt covers the other
// shape of a missing stage: no base at all, with both trunk and branch
// present (an add/add), which is a materially different situation from a
// modify/delete and must say so.
func TestSimulateRebaseMarksAnAddAddConflictAsBothSidesAddedIt(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"new.txt": "trunk\n"}},
		[]map[string]string{{"b.txt": "b2\n"}, {"new.txt": "branch\n"}})
	r, err := SimulateRebase(dir, "main", "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || len(r.Stop.Conflicts) != 1 || r.Stop.Conflicts[0].Incomplete != "both sides added it" {
		t.Fatalf("stop = %+v", r.Stop)
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

	r, err := SimulateRebase(dir, "main", "feature", nil)
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

func ownedLineConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte("conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Two commits both conflict on the claimed file. Before this change the
// replay stopped at the first; now it resolves both and reports recipe.
func TestSimulateResolvesEveryStop(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"v.txt": "1.2.0\n"}},
	)
	r, err := SimulateRebase(dir, "main", "feature", ownedLineConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop != nil {
		t.Fatalf("Stop = %+v, want nil: both stops are resolved", r.Stop)
	}
	if len(r.Stops) != 2 {
		t.Fatalf("Stops = %d, want 2", len(r.Stops))
	}
	for i, s := range r.Stops {
		if !s.Resolved {
			t.Fatalf("stop %d not resolved: %+v", i+1, s.Files)
		}
		if len(s.Conflicts) != 0 {
			t.Fatalf("stop %d kept its blobs; only the deciding stop may", i+1)
		}
	}
	if r.Err != nil {
		t.Fatalf("Err = %v", r.Err)
	}
}

// The first stop resolves; the second is a file nothing claims. The replay
// must reach the second and name it, which is the whole point of Part 1.
func TestSimulateStopsAtTheFirstUnclaimedStopWhereverItIs(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"a.txt": "branch\n"}},
	)
	r, err := SimulateRebase(dir, "main", "feature", ownedLineConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil {
		t.Fatal("Stop = nil, want the second commit")
	}
	if r.Stop.Index != 2 || r.Stop.Total != 2 {
		t.Fatalf("Stop at %d/%d, want 2/2", r.Stop.Index, r.Stop.Total)
	}
	if len(r.Stop.Files) != 1 || r.Stop.Files[0].Path != "a.txt" || r.Stop.Files[0].Resolved {
		t.Fatalf("Stop.Files = %+v, want a.txt unresolved", r.Stop.Files)
	}
	if len(r.Stop.Conflicts) != 1 || len(r.Stop.Conflicts[0].Trunk) == 0 {
		t.Fatalf("the deciding stop must keep its blobs: %+v", r.Stop.Conflicts)
	}
	if len(r.Stops) != 2 || !r.Stops[0].Resolved {
		t.Fatalf("Stops = %+v, want the first one resolved", r.Stops)
	}
}

// With no declaration nothing is claimed, so the first stop still stops the
// replay: the behaviour every existing caller had.
func TestSimulateWithoutAConfigStopsAtTheFirst(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"v.txt": "1.2.0\n"}},
	)
	r, err := SimulateRebase(dir, "main", "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || r.Stop.Index != 1 {
		t.Fatalf("Stop = %+v, want 1/2", r.Stop)
	}
}

// The resolved bytes must be what the NEXT commit replays against. Trunk
// is 2.0.0, so stop 1 resolves max-plus-patch(branch 1.1.0, trunk 2.0.0) to
// 2.0.1. The second branch commit touches v.txt again AND a file nothing
// claims, so the replay stops there and keeps that stop's blobs — and the
// trunk side of its v.txt conflict is the proof: 2.0.1 means the resolved
// blob was carried forward, 2.0.0 means it was not. (Unchained, the second
// resolution would come out 2.0.1 instead of 2.0.2, so asserting on the
// blob is both simpler and stricter than asserting on the version.)
func TestSimulateChainsTheResolvedContent(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"v.txt": "1.1.1\n", "a.txt": "branch\n"}},
	)
	r, err := SimulateRebase(dir, "main", "feature", ownedLineConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || r.Stop.Index != 2 {
		t.Fatalf("Stop = %+v, want the second commit", r.Stop)
	}
	var got string
	for _, c := range r.Stop.Conflicts {
		if c.Path == "v.txt" {
			got = string(c.Trunk)
		}
	}
	if got != "2.0.1\n" {
		t.Fatalf("v.txt trunk side at stop 2 = %q, want %q: the resolved blob was not chained", got, "2.0.1\n")
	}
}

// scriptClaimingV commits an executable on trunk that claims v.txt and
// returns a declaration pointing at it. It cannot go through repoWith's
// edits, which write mode 0644.
func scriptClaimingV(t *testing.T, dir string) *Config {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\ncase \"$1\" in\n--check) exit 0 ;;\n*) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "claim"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "trunk declares a script")
	cfg, err := Parse([]byte("conflicts:\n  - paths: [v.txt]\n    strategy: script\n    run: bin/claim\n"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A script proves it owns a path but yields no bytes in the object store, so
// there is nothing to chain the next commit onto: the replay stops there and
// says so rather than pretending it reached the end.
func TestSimulateTruncatesAtAScriptStop(t *testing.T) {
	dir := linearRepo(t,
		[]map[string]string{{"v.txt": "2.0.0\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.1.0\n"}, {"a.txt": "branch\n"}},
	)
	r, err := SimulateRebase(dir, "main", "feature", scriptClaimingV(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop != nil {
		t.Fatalf("Stop = %+v, want nil: the script claims the only stop reached", r.Stop)
	}
	if !r.Truncated || !strings.Contains(r.Why, "bin/claim owns v.txt") {
		t.Fatalf("Truncated = %v, Why = %q", r.Truncated, r.Why)
	}
	if len(r.Stops) != 1 || !r.Stops[0].Resolved || len(r.Stops[0].Conflicts) != 0 {
		t.Fatalf("Stops = %+v, want one resolved stop with its blobs dropped", r.Stops)
	}
}
