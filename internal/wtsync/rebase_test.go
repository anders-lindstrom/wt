package wtsync

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runRepo builds a repository whose trunk declares v.txt owned-line
// max-plus-patch and w.txt take-trunk, with origin pointing at itself so
// origin/main exists, and a worktree on feature. The base has w.txt so a
// w.txt conflict is a genuine three-way one, not an add/add.
func runRepo(t *testing.T, trunkEdits, branchEdits []map[string]string) (dir string, wt string, cfg *Config) {
	t.Helper()
	dir = repoWith(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n", "v.txt": "1.0.0\n", "w.txt": "w\n"}, trunkEdits, branchEdits)
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n  - paths: [w.txt]\n    strategy: take-trunk\n"
	if err := os.WriteFile(filepath.Join(dir, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "declare")
	gitIn(t, dir, "remote", "add", "origin", dir)
	gitIn(t, dir, "fetch", "-q", "origin")
	// Pin the hooks dir so a global core.hooksPath on the developer's machine
	// can neither run its hooks against the fixture nor hide a test hook.
	gitIn(t, dir, "config", "core.hooksPath", filepath.Join(dir, ".git", "hooks"))
	cfg, err := LoadFromTrunk(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	w := featureWorktree(t, dir)
	return dir, w.Path, cfg
}

func trunkReq(wt string, epoch int64) Request {
	return Request{Path: wt, Branch: "feature", Trunk: "origin/main", Onto: "origin/main", Epoch: epoch}
}

func TestRebaseReplaysACleanBranchAndPinsASafetyRef(t *testing.T) {
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"b.txt": "b2\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	var log bytes.Buffer
	res, err := Rebase(dir, cfg, trunkReq(wt, 42), &log)
	if err != nil {
		t.Fatal(err)
	}
	if res.OldTip != old || res.Restored || res.Replayed != 1 || len(res.Stops) != 0 {
		t.Fatalf("result %+v", res)
	}
	if gitIn(t, dir, "rev-parse", "refs/wt-sync/feature/42") != old {
		t.Fatal("safety ref does not pin the old tip")
	}
	if gitIn(t, wt, "rev-parse", "HEAD~1") != gitIn(t, dir, "rev-parse", "origin/main") {
		t.Fatal("not rebased onto origin/main")
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != res.NewTip {
		t.Fatal("NewTip is not HEAD")
	}
}

func TestRebaseResolvesARecipeStopAndContinues(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"a.txt": "a2\n"}, {"v.txt": "1.0.1\n"}})
	var log bytes.Buffer
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), &log)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 2 || len(res.Stops) != 1 {
		t.Fatalf("result %+v", res)
	}
	s := res.Stops[0]
	if s.Index != 2 || s.Total != 2 || len(s.Files) != 1 || !s.Files[0].Resolved || s.Files[0].Strategy != "owned-line" {
		t.Fatalf("stop %+v", s)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.6\n" {
		t.Fatalf("v.txt %q", got)
	}
	if ok, _ := RebaseInProgress(wt); ok {
		t.Fatal("rebase still in progress")
	}
	if !strings.Contains(log.String(), "stop 2/2") {
		t.Fatalf("log %q", log.String())
	}
}

func TestRebaseDropsACommitTheResolutionMadeEmpty(t *testing.T) {
	// The branch's only change in its first commit is to w.txt, which is
	// taken from trunk: the commit becomes empty and git drops it on
	// --continue. The second commit survives.
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"w.txt": "trunk\n"}},
		[]map[string]string{{"w.txt": "branch\n"}, {"b.txt": "b2\n"}})
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 1 || len(res.Stops) != 1 || !res.Stops[0].Files[0].Resolved {
		t.Fatalf("result %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "w.txt")); string(got) != "trunk\n" {
		t.Fatalf("w.txt %q", got)
	}
	if gitIn(t, wt, "log", "-1", "--format=%s") != "branch 2" {
		t.Fatalf("top commit %q", gitIn(t, wt, "log", "-1", "--format=%s"))
	}
}

func TestRebaseHandsOverAnUnclaimedStop(t *testing.T) {
	// Nothing at this stop is claimed, so the handover stages nothing and
	// names the one file a person owns. The branch ref has not moved: the
	// rewrite only lands when the rebase completes.
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"a.txt": "trunk\n"}},
		[]map[string]string{{"a.txt": "branch\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, 7), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Left == nil || len(res.Stops) != 1 || res.Stops[0].Files[0].Resolved || res.Stops[0].Files[0].Note != "unclaimed" {
		t.Fatalf("result %+v", res)
	}
	if got := res.Left.Left; len(got) != 1 || got[0] != "a.txt" {
		t.Fatalf("Left.Left = %v, want [a.txt]", got)
	}
	if len(res.Left.Staged) != 0 || len(res.Left.Deleted) != 0 {
		t.Fatalf("nothing was resolved here, yet Staged = %v, Deleted = %v", res.Left.Staged, res.Left.Deleted)
	}
	if gitIn(t, wt, "rev-parse", "feature") != old {
		t.Fatal("the branch moved before the rebase finished")
	}
	if ok, _ := RebaseInProgress(wt); !ok {
		t.Fatal("the rebase was not left in place")
	}
}

func TestRebaseHandsOverASecondStopAndKeepsTheFirstsWork(t *testing.T) {
	// First stop resolves (v.txt, owned-line) and is replayed; the second is
	// unclaimed (a.txt) and is handed over. The handover is the second stop,
	// and the first stop's resolution is still in the worktree.
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}, {"a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}, {"a.txt": "branch\n"}})
	res, err := Rebase(dir, cfg, trunkReq(wt, 8), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Left == nil || len(res.Stops) != 2 || !res.Stops[0].Files[0].Resolved || res.Stops[1].Files[0].Resolved {
		t.Fatalf("result %+v", res)
	}
	if res.Left.Index != 2 || res.Left.Total != 2 {
		t.Fatalf("handover is at %d/%d, want the second stop", res.Left.Index, res.Left.Total)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.6\n" {
		t.Fatalf("v.txt = %q, want the first stop's resolution", got)
	}
}

func TestRebaseACommitAlreadyOnTrunkIsDroppedNotCounted(t *testing.T) {
	dir, wt, cfg := runRepo(t, nil, nil)
	if err := os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "add c")
	// The same change lands on trunk (a cherry-pick; -q is not a cherry-pick flag).
	gitIn(t, dir, "cherry-pick", "feature")
	gitIn(t, dir, "fetch", "-q", "origin")
	res, err := Rebase(dir, cfg, trunkReq(wt, 9), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 0 || len(res.Stops) != 0 {
		t.Fatalf("result %+v", res)
	}
}

func TestRebaseOntoAParentTipUsesUpstreamAndReadsScriptsFromTrunk(t *testing.T) {
	// A stack child: rebase only the child's own commits onto the parent's
	// new tip. Built by hand: parent branch p with one commit, child c on
	// top with one more, then p rewritten onto origin/main (as the parent's
	// run would have done).
	dir, wt, cfg := runRepo(t, []map[string]string{{"a.txt": "a2\n"}}, nil)
	gitIn(t, dir, "branch", "p", "feature")
	gitIn(t, wt, "checkout", "-q", "p")
	if err := os.WriteFile(filepath.Join(wt, "p.txt"), []byte("p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "parent")
	oldParent := gitIn(t, wt, "rev-parse", "HEAD")
	gitIn(t, wt, "checkout", "-q", "-b", "c")
	if err := os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "child")
	gitIn(t, dir, "checkout", "-q", "p")
	gitIn(t, dir, "rebase", "-q", "--no-gpg-sign", "origin/main")
	newParent := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "main")
	req := Request{Path: wt, Branch: "c", Trunk: "origin/main", Onto: newParent, Upstream: oldParent, Epoch: 10}
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored || res.Replayed != 1 || gitIn(t, wt, "rev-parse", "HEAD~1") != newParent {
		t.Fatalf("result %+v parent %s", res, gitIn(t, wt, "rev-parse", "HEAD~1"))
	}
}

func TestRebaseGivesUpWhenContinueDoesNotAdvance(t *testing.T) {
	// A prepare-commit-msg hook that always fails makes --continue stop at
	// the same commit with nothing unmerged. The loop must notice and
	// restore instead of spinning. (Not pre-commit: git 2.55 does not run
	// that one for the sequencer's own commit, so it never fires here.)
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}})
	hooks := filepath.Join(dir, ".git", "hooks") // what runRepo set core.hooksPath to
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "prepare-commit-msg"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, 11), nil)
	if err == nil || !strings.Contains(err.Error(), "did not advance") {
		t.Fatalf("err %v", err)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatal("not restored")
	}
	if ok, _ := RebaseInProgress(wt); ok {
		t.Fatal("rebase left in progress")
	}
	_ = res
}

func TestPreflightOrdersItsReasons(t *testing.T) {
	cases := []struct {
		a      Assessment
		v      Verdict
		reason string
	}{
		{Assessment{Class: Clean, Dirty: true}, RefuseRun, "tracked changes"},
		{Assessment{Class: Current, Dirty: true}, RefuseRun, "tracked changes"},
		{Assessment{Class: Recipe, Sessions: Sessions{{Name: "x-1"}}}, RefuseRun, "x-1"},
		{Assessment{Class: Recipe, NoConfig: true}, RefuseRun, "no declaration"},
		{Assessment{Class: Current}, SkipRun, "already on trunk"},
		{Assessment{Class: Stale}, SkipRun, "nothing ahead"},
		{Assessment{Class: Divergent, Divergent: []Collision{{Path: "spec.json", Groups: []KeyGroup{{Section: "paths", Keys: []string{"/a"}}}}}}, RefuseRun, "openapi refuses spec.json at the endpoint: both sides changed 1 path"},
		{Assessment{Class: Contested, Replay: Replay{Stop: &Stop{Index: 2, Total: 5}}, Files: []FileOutcome{{Path: "x.java", Note: "unclaimed"}}}, Proceed, ""},
		{Assessment{Class: Contested, Paused: true, Dirty: true}, RefuseRun, "resume"},
		{Assessment{Class: Contested, Paused: true, Err: errors.New("behind/ahead failed")}, RefuseRun, "assessment failed"},
		{Assessment{Class: Recipe}, Proceed, ""},
		{Assessment{Class: Clean}, Proceed, ""},
		{Assessment{Class: Detached}, RefuseRun, "no branch"},
		{Assessment{Class: Unknown}, RefuseRun, "unknown"},
	}
	for i, c := range cases {
		v, reason := Preflight(c.a)
		if v != c.v || !strings.Contains(reason, c.reason) {
			t.Errorf("case %d: got %v %q, want %v containing %q", i, v, reason, c.v, c.reason)
		}
	}
}

// abortProofRepo is a repository whose declared strategy breaks the rebase
// state and then fails: with rebase-merge/orig-head gone `git rebase
// --abort` fails, which is the only way to reach restore's --quit fallback.
// Exit 3 is a failure, not a refusal (which is 2): a refusal would be handed
// over, and only a failure still restores.
func abortProofRepo(t *testing.T) (dir string, wt string, cfg *Config) {
	t.Helper()
	dir = repoWith(t, map[string]string{"f.txt": "base\n"},
		[]map[string]string{{"f.txt": "trunk\n"}},
		[]map[string]string{{"f.txt": "branch\n"}})
	script := "#!/bin/sh\n" +
		"rm -f \"$(git rev-parse --absolute-git-dir)/rebase-merge/orig-head\"\n" +
		"echo sabotage >&2\nexit 3\n"
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "break"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "conflicts:\n  - paths: [f.txt]\n    strategy: script\n    run: bin/break\n"
	if err := os.WriteFile(filepath.Join(dir, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "declare")
	gitIn(t, dir, "remote", "add", "origin", dir)
	gitIn(t, dir, "fetch", "-q", "origin")
	gitIn(t, dir, "config", "core.hooksPath", filepath.Join(dir, ".git", "hooks"))
	var err error
	if cfg, err = LoadFromTrunk(dir, "main"); err != nil {
		t.Fatal(err)
	}
	return dir, featureWorktree(t, dir).Path, cfg
}

func TestRebaseRestoresThroughQuitWhenAbortCannotWork(t *testing.T) {
	dir, wt, cfg := abortProofRepo(t)
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, 11), nil)
	if err == nil || !strings.Contains(err.Error(), "sabotage") {
		t.Fatalf("err = %v, want the strategy's failure", err)
	}
	if strings.Contains(err.Error(), "not restored") {
		t.Fatalf("restore failed: %v", err)
	}
	if !res.Restored || res.Left != nil {
		t.Fatalf("expected a restore, got %+v", res)
	}
	if busy, _ := RebaseInProgress(wt); busy {
		t.Fatal("a rebase is still in progress")
	}
	if ref := gitIn(t, wt, "symbolic-ref", "--quiet", "HEAD"); ref != "refs/heads/feature" {
		t.Fatalf("HEAD is %q, not the branch", ref)
	}
	if got := gitIn(t, wt, "rev-parse", "HEAD"); got != old {
		t.Fatalf("HEAD is %s, want %s", got, old)
	}
	if out := gitIn(t, wt, "status", "--porcelain", "--untracked-files=no"); out != "" {
		t.Fatalf("the unmerged index survived: %q", out)
	}
}

// A stop nothing claims is left in place, not aborted: the rebase is still
// in progress, the strategy's answer for the claimed file is staged, and the
// handover names what is left.
func TestRebaseLeavesAContestedStopInPlace(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	res, err := Rebase(dir, cfg, trunkReq(wt, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Left == nil {
		t.Fatalf("Left = nil, want a handover; Restored = %v", res.Restored)
	}
	if res.Restored {
		t.Fatal("Restored = true, want the rebase left in place")
	}
	if got := res.Left.Left; len(got) != 1 || got[0] != "a.txt" {
		t.Fatalf("Left.Left = %v, want [a.txt]", got)
	}
	if res.Left.Staged["v.txt"] == "" {
		t.Fatal("Staged has no oid for v.txt")
	}
	if busy, err := RebaseInProgress(wt); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; want true", busy, err)
	}
	unmerged, err := StagedConflicts(wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmerged) != 1 || unmerged[0].Path != "a.txt" {
		t.Fatalf("unmerged = %+v, want only a.txt", unmerged)
	}
}

// A branch with children in the same run may not be left mid-rebase: the
// children would be stranded on a base that no longer exists, which is the
// half-applied stack the spec forbids.
func TestRebaseRestoresAContestedStopOnAStackParent(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	req := trunkReq(wt, 1)
	req.Stacked = true
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Left != nil || !res.Restored {
		t.Fatalf("res = %+v, want restored with no handover", res)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatal("not restored to the old tip")
	}
}

// Resume drives the same loop: with the unclaimed file resolved by hand and
// staged, the rebase finishes and the branch moves.
func TestResumeFinishesTheRebase(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	req := trunkReq(wt, 1)
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "a.txt")

	out, err := Resume(dir, cfg, req, res.OldTip, res.Safety, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Left != nil {
		t.Fatalf("Left = %+v, want nil", out.Left)
	}
	if busy, _ := RebaseInProgress(wt); busy {
		t.Fatal("still mid-rebase")
	}
	if out.NewTip == "" || out.NewTip == out.OldTip {
		t.Fatalf("NewTip = %q, OldTip = %q", out.NewTip, out.OldTip)
	}
}

// A resume never resets the worktree: a failure there would throw away a
// person's own resolution. An unstaged tracked change makes git refuse to
// continue; the loop must report that and leave everything alone.
func TestResumeNeverRestores(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	req := trunkReq(wt, 1)
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	// Staged, then changed again in the working tree: git refuses.
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "a.txt")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("unstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = Resume(dir, cfg, req, res.OldTip, res.Safety, nil)
	if err == nil {
		t.Fatal("Resume = nil error, want the refusal git made")
	}
	// The message points at undo by the name a person uses, and does not
	// claim the worktree is untouched: a resume may have applied a
	// strategy's answer before it failed.
	if !strings.Contains(err.Error(), "wt sync undo feature") || strings.Contains(err.Error(), "untouched") {
		t.Fatalf("err = %v", err)
	}
	if busy, _ := RebaseInProgress(wt); !busy {
		t.Fatal("the rebase was thrown away; a resume must never restore")
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(got) != "unstaged\n" {
		t.Fatalf("a.txt = %q; the person's work was overwritten", got)
	}
}

// Stacked forbids a handover, and on a run it means restore. On a resume it
// may not: the reset would discard a person's own resolution, which is the
// one thing a resume must never do. It refuses and leaves everything alone.
func TestResumeNeverRestoresAStackParent(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	req := trunkReq(wt, 1)
	req.Work = "feat"
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	// The branch grew a descendant in the run between the two calls, and
	// a.txt is still nobody's: the resume reaches the same stop.
	req.Stacked = true
	out, err := Resume(dir, cfg, req, res.OldTip, res.Safety, nil)
	if err == nil {
		t.Fatalf("Resume = %+v, want a refusal", out)
	}
	if !strings.Contains(err.Error(), "descendants") || !strings.Contains(err.Error(), "wt sync undo feat") {
		t.Fatalf("err = %v", err)
	}
	if out.Restored {
		t.Fatal("Restored = true; a resume must never restore")
	}
	if busy, _ := RebaseInProgress(wt); !busy {
		t.Fatal("the rebase was thrown away")
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.6\n" {
		t.Fatalf("v.txt = %q; the strategy's answer was reset away", got)
	}
}

// Someone ran git rebase --continue themselves and it finished: resume
// accepts that and reports the finished rebase rather than failing.
func TestResumeToleratesAFinishedRebase(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	req := trunkReq(wt, 1)
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "a.txt")
	gitIn(t, wt, "-c", "core.editor=true", "rebase", "--continue")

	out, err := Resume(dir, cfg, req, res.OldTip, res.Safety, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Left != nil || out.NewTip == "" {
		t.Fatalf("Resume = %+v", out)
	}
}

// The reflog is the only record of where a finished rebase started. Without
// it nothing proves the rebase is the run's, so resume refuses rather than
// certifying it.
func TestResumeRefusesAFinishedRebaseWithNoReflog(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	req := trunkReq(wt, 1)
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "a.txt")
	gitIn(t, wt, "-c", "core.editor=true", "rebase", "--continue")
	gitIn(t, wt, "reflog", "expire", "--expire=now", "--all")

	if _, err := Resume(dir, cfg, req, res.OldTip, res.Safety, nil); err == nil || !strings.Contains(err.Error(), "wt sync undo --force feature") {
		t.Fatalf("Resume err = %v, want a refusal naming wt sync undo --force", err)
	}
}

// A rebase somebody aborted is not this run's result. Certifying it would
// let a later undo discard commits the run never made.
func TestResumeRefusesARebaseThatWasAborted(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "a.txt": "branch\n"}})
	req := trunkReq(wt, 1)
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left == nil {
		t.Fatalf("Rebase = %+v, %v", res, err)
	}
	gitIn(t, wt, "rebase", "--abort")

	if _, err := Resume(dir, cfg, req, res.OldTip, res.Safety, nil); err == nil {
		t.Fatal("Resume accepted an aborted rebase as finished")
	}
}

func TestPreflightLetsContestedProceedAndRefusesPaused(t *testing.T) {
	if v, why := Preflight(Assessment{Class: Contested, Files: []FileOutcome{{Path: "a.txt"}}}); v != Proceed {
		t.Fatalf("contested = %v (%s), want Proceed", v, why)
	}
	if v, why := Preflight(Assessment{Class: Contested, Paused: true}); v != RefuseRun || !strings.Contains(why, "resume") {
		t.Fatalf("paused = %v (%s), want a refusal naming resume", v, why)
	}
}
