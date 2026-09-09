package wtsync

import (
	"bytes"
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

func TestRebaseAbortsAndRestoresOnAnUnclaimedStop(t *testing.T) {
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"a.txt": "trunk\n"}},
		[]map[string]string{{"a.txt": "branch\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, 7), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Restored || len(res.Stops) != 1 || res.Stops[0].Files[0].Resolved || res.Stops[0].Files[0].Note != "unclaimed" {
		t.Fatalf("result %+v", res)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old || gitIn(t, wt, "rev-parse", "feature") != old {
		t.Fatal("not restored to the old tip")
	}
	if gitIn(t, wt, "symbolic-ref", "HEAD") != "refs/heads/feature" {
		t.Fatal("HEAD is detached after restore")
	}
	if ok, _ := RebaseInProgress(wt); ok {
		t.Fatal("rebase left in progress")
	}
	if out := gitIn(t, wt, "status", "--porcelain"); out != "" {
		t.Fatalf("worktree not clean: %q", out)
	}
}

func TestRebaseRestoresOnASecondUnclaimedStop(t *testing.T) {
	// First stop resolves (v.txt, owned-line); second is unclaimed (a.txt).
	// The first stop's resolution must not survive the restore.
	dir, wt, cfg := runRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}, {"a.txt": "trunk\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}, {"a.txt": "branch\n"}})
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, 8), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Restored || len(res.Stops) != 2 || !res.Stops[0].Files[0].Resolved || res.Stops[1].Files[0].Resolved {
		t.Fatalf("result %+v", res)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatal("not restored")
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.1\n" {
		t.Fatalf("v.txt after restore %q", got)
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
		{Assessment{Class: Recipe, Agent: &Agent{Name: "x-1"}}, RefuseRun, "x-1"},
		{Assessment{Class: Recipe, NoConfig: true}, RefuseRun, "no declaration"},
		{Assessment{Class: Current}, SkipRun, "already on trunk"},
		{Assessment{Class: Stale}, SkipRun, "nothing ahead"},
		{Assessment{Class: Divergent, Divergent: []string{"openapi refuses spec.json"}}, RefuseRun, "openapi refuses"},
		{Assessment{Class: Contested, Replay: Replay{Stop: &Stop{Index: 2, Total: 5}}, Files: []FileOutcome{{Path: "x.java", Note: "unclaimed"}}}, RefuseRun, "2/5"},
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
// state before refusing: with rebase-merge/orig-head gone `git rebase
// --abort` fails, which is the only way to reach restore's --quit fallback.
func abortProofRepo(t *testing.T) (dir string, wt string, cfg *Config) {
	t.Helper()
	dir = repoWith(t, map[string]string{"f.txt": "base\n"},
		[]map[string]string{{"f.txt": "trunk\n"}},
		[]map[string]string{{"f.txt": "branch\n"}})
	script := "#!/bin/sh\n" +
		"rm -f \"$(git rev-parse --absolute-git-dir)/rebase-merge/orig-head\"\n" +
		"echo sabotage >&2\nexit 2\n"
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "refuse"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "conflicts:\n  - paths: [f.txt]\n    strategy: script\n    run: bin/refuse\n"
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
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if !res.Restored {
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
