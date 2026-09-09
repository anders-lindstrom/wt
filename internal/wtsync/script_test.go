package wtsync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeScript commits an executable to origin's main that answers the
// contract by inspecting the index it was given: it exits 0 when stage 2 of
// the file contains "ok", 2 with a reason otherwise, and 1 for a path
// outside its claim. It is committed, not written into the checkout, because
// scripts are read from trunk.
func fakeScript(t *testing.T, local, origin string) string {
	t.Helper()
	body := `#!/usr/bin/env bash
case $1 in
--claims) echo 'special/*.txt' ;;
--check|--resolve)
  [[ $2 == special/* ]] || exit 1
  stages=$(git ls-files -u -- "$2" | awk '{print $3}' | sort -u | tr -d '\n')
  [[ $stages == 123 ]] || { echo "not three-staged: $stages" >&2; exit 2; }
  if git show ":2:$2" | grep -q ok; then exit 0; fi
  echo "trunk side is not ok" >&2; exit 2 ;;
esac
`
	full := filepath.Join(origin, "bin", "conflict", "special")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", "bin/conflict/special")
	gitIn(t, origin, "commit", "-q", "-m", "add the special resolver")
	gitIn(t, local, "fetch", "-q", "origin")
	return "bin/conflict/special"
}

func TestScriptChecksThroughATemporaryIndexWithoutTouchingTheRealOne(t *testing.T) {
	local, origin := repoWithOrigin(t)
	run := fakeScript(t, local, origin)
	before := gitIn(t, local, "--no-optional-locks", "status", "--porcelain")

	s := Script{Root: local, Trunk: "origin/main", Run: run}
	c := Conflict{Path: "special/a.txt", Base: []byte("base\n"), Trunk: []byte("ok\n"), Branch: []byte("branch\n")}
	if _, err := s.Resolve(c); err != nil {
		t.Fatalf("expected the script to accept, got %v", err)
	}

	c.Trunk = []byte("nope\n")
	_, err := s.Resolve(c)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "trunk side is not ok") {
		t.Errorf("err = %v", err)
	}

	if after := gitIn(t, local, "--no-optional-locks", "status", "--porcelain"); after != before {
		t.Errorf("the real index changed: %q -> %q", before, after)
	}
	if entries := gitIn(t, local, "ls-files", "-u"); entries != "" {
		t.Errorf("real index has unmerged entries: %s", entries)
	}
}

func TestScriptRunsTrunksCopyNotTheCheckouts(t *testing.T) {
	local, origin := repoWithOrigin(t)
	run := fakeScript(t, local, origin)
	// a different, always-accepting script in the working tree must be ignored
	full := filepath.Join(local, run)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := Script{Root: local, Trunk: "origin/main", Run: run}
	c := Conflict{Path: "special/a.txt", Base: []byte("base\n"), Trunk: []byte("nope\n"), Branch: []byte("branch\n")}
	if _, err := s.Resolve(c); !IsRefusal(err) {
		t.Errorf("trunk's script refuses this; the checkout's copy must not have run: %v", err)
	}
}

func TestScriptNotMineIsARefusalThatSaysSo(t *testing.T) {
	local, origin := repoWithOrigin(t)
	run := fakeScript(t, local, origin)
	s := Script{Root: local, Trunk: "origin/main", Run: run}
	_, err := s.Resolve(Conflict{Path: "other/a.txt", Base: []byte("b"), Trunk: []byte("ok"), Branch: []byte("r")})
	if !IsRefusal(err) || !strings.Contains(err.Error(), "does not claim") {
		t.Errorf("err = %v", err)
	}
}

func TestScriptMissingExecutableIsAnErrorNotARefusal(t *testing.T) {
	local, _ := repoWithOrigin(t)
	s := Script{Root: local, Trunk: "origin/main", Run: "bin/conflict/missing"}
	_, err := s.Resolve(Conflict{Path: "x", Base: []byte("b"), Trunk: []byte("t"), Branch: []byte("r")})
	if err == nil || IsRefusal(err) {
		t.Errorf("err = %v, want a hard error", err)
	}
}

func TestFromRuleBuildsScript(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "script", Run: "bin/conflict/x"}, "/repo", "origin/main")
	if err != nil || s.Name() != "script" {
		t.Errorf("FromRule = %v, %v", s, err)
	}
}

// stoppedRebaseWithScript builds a repository whose trunk declares run as a
// script strategy for v.txt (trunk 1.0.5, branch 1.0.1, so a rebase always
// stops on it), fetches it into an "origin" remote pointing at the same
// repository the way commands.syncRepo does, and stops a rebase of the
// feature worktree onto main.
func stoppedRebaseWithScript(t *testing.T, script string) (dir, wt string) {
	t.Helper()
	dir = linearRepo(t,
		[]map[string]string{{"v.txt": "1.0.5\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n"}})
	full := filepath.Join(dir, "bin", "resolve")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "conflicts:\n  - paths: [v.txt]\n    strategy: script\n    run: bin/resolve\n"
	if err := os.WriteFile(filepath.Join(dir, ".wt-sync.yaml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "declare the script")
	gitIn(t, dir, "remote", "add", "origin", dir)
	gitIn(t, dir, "fetch", "-q", "origin")
	w := featureWorktree(t, dir)
	if err := gitCmd(w.Path, "rebase", "--no-update-refs", "--no-gpg-sign", "main").Run(); err == nil {
		t.Fatal("rebase did not stop")
	}
	return dir, w.Path
}

func TestResolveInWorktreeRunsTheScriptAgainstTheRealIndex(t *testing.T) {
	// A script that resolves by taking the branch side (stage 3) and staging it.
	script := "#!/bin/sh\ncase \"$1\" in\n--resolve) git show \":3:$2\" > \"$2\" && git add -- \"$2\" ;;\n*) exit 1 ;;\nesac\n"
	dir, wt := stoppedRebaseWithScript(t, script) // fixture: trunk declares v.txt -> script bin/resolve; rebase stopped on v.txt
	s := Script{Root: dir, Trunk: "origin/main", Run: "bin/resolve"}
	if err := s.ResolveInWorktree(wt, "v.txt"); err != nil {
		t.Fatal(err)
	}
	if out := gitIn(t, wt, "ls-files", "-u", "--", "v.txt"); out != "" {
		t.Fatalf("still unmerged: %s", out)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "v.txt")); string(got) != "1.0.1\n" {
		t.Fatalf("content %q", got)
	}
}

func TestResolveInWorktreeExitTwoIsARefusalWithTheReason(t *testing.T) {
	script := "#!/bin/sh\necho 'not my shape' >&2; exit 2\n"
	dir, wt := stoppedRebaseWithScript(t, script)
	s := Script{Root: dir, Trunk: "origin/main", Run: "bin/resolve"}
	err := s.ResolveInWorktree(wt, "v.txt")
	if !IsRefusal(err) || !strings.Contains(err.Error(), "not my shape") {
		t.Fatalf("err %v", err)
	}
}

func TestResolveInWorktreeAScriptThatExitsZeroWithoutStagingIsAnError(t *testing.T) {
	script := "#!/bin/sh\nexit 0\n"
	dir, wt := stoppedRebaseWithScript(t, script)
	s := Script{Root: dir, Trunk: "origin/main", Run: "bin/resolve"}
	err := s.ResolveInWorktree(wt, "v.txt")
	if err == nil || IsRefusal(err) || !strings.Contains(err.Error(), "left v.txt unmerged") {
		t.Fatalf("err %v", err)
	}
}

func TestScriptTimesOutInsteadOfHanging(t *testing.T) {
	// Echoes on every tick rather than once before a single sleep, so the
	// marker is captured regardless of scheduling jitter in when the script
	// actually starts running under test load, and the script would hang
	// forever without the deadline actually killing it.
	script := "#!/bin/sh\nwhile :; do\n  echo 'still working' >&2\n  sleep 0.1\ndone\n"
	dir, wt := stoppedRebaseWithScript(t, script)
	s := Script{Root: dir, Trunk: "origin/main", Run: "bin/resolve", Timeout: 300 * time.Millisecond}
	start := time.Now()
	err := s.ResolveInWorktree(wt, "v.txt")
	elapsed := time.Since(start)
	if err == nil || IsRefusal(err) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(err.Error(), "still working") {
		t.Errorf("err %v, want the stderr captured before the kill", err)
	}
	// Bounds real wall-clock time: without a real kill, the loop runs forever
	// and this test would hang past its Timeout instead of returning.
	if bound := s.Timeout + scriptWaitDelay; elapsed > bound {
		t.Fatalf("took %s, want under Timeout+scriptWaitDelay (%s): the process group was not actually killed", elapsed, bound)
	}
}

// TestResolveInWorktreeAcceptsAScriptThatBackgroundsAJob covers a script that
// resolves the conflict, then backgrounds a job that outlives it (a
// daemonising build tool, say). The direct process exits 0, but the
// backgrounded child keeps holding the stderr pipe past scriptWaitDelay, so
// Wait returns exec.ErrWaitDelay for an otherwise-successful run: that must
// still be treated as exit 0, not a failure.
func TestResolveInWorktreeAcceptsAScriptThatBackgroundsAJob(t *testing.T) {
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"--resolve) git show \":3:$2\" > \"$2\" && git add -- \"$2\"; sleep 3 & ;;\n" +
		"*) exit 1 ;;\nesac\n"
	dir, wt := stoppedRebaseWithScript(t, script)
	s := Script{Root: dir, Trunk: "origin/main", Run: "bin/resolve"}
	if err := s.ResolveInWorktree(wt, "v.txt"); err != nil {
		t.Fatal(err)
	}
	if out := gitIn(t, wt, "ls-files", "-u", "--", "v.txt"); out != "" {
		t.Fatalf("still unmerged: %s", out)
	}
}

func TestScriptCheckTimesOutInsteadOfHanging(t *testing.T) {
	script := "#!/bin/sh\nsleep 5\n"
	dir, _ := stoppedRebaseWithScript(t, script)
	s := Script{Root: dir, Trunk: "origin/main", Run: "bin/resolve", Timeout: 300 * time.Millisecond}
	c := Conflict{Path: "v.txt", Base: []byte("1.0.0\n"), Trunk: []byte("1.0.5\n"), Branch: []byte("1.0.1\n")}
	start := time.Now()
	_, err := s.Resolve(c)
	elapsed := time.Since(start)
	if err == nil || IsRefusal(err) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err %v", err)
	}
	if bound := s.Timeout + scriptWaitDelay; elapsed > bound {
		t.Fatalf("took %s, want under Timeout+scriptWaitDelay (%s): the process group was not actually killed", elapsed, bound)
	}
}

func TestResolveConflictInAWorktreeUsesTheScriptInPlace(t *testing.T) {
	script := "#!/bin/sh\ncase \"$1\" in\n--resolve) git show \":3:$2\" > \"$2\" && git add -- \"$2\" ;;\n*) exit 1 ;;\nesac\n"
	dir, wt := stoppedRebaseWithScript(t, script)
	cfg, _ := Parse([]byte("conflicts:\n  - paths: [v.txt]\n    strategy: script\n    run: bin/resolve\n"))
	cs, _ := StagedConflicts(wt)
	r, err := resolveConflict(dir, "origin/main", cfg, cs[0], wt)
	if err != nil || !r.Outcome.Resolved || !r.InPlace || r.Content != nil {
		t.Fatalf("resolution %+v err %v", r, err)
	}
}

func TestKillRunningTakesDownARegisteredProcessGroup(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sleep", "30")
	done := make(chan error, 1)
	go func() { done <- runScript(cmd) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		groups.Lock()
		n := len(groups.pids)
		groups.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runScript registered no process group")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := KillRunning(); n < 1 {
		t.Fatal("KillRunning signalled nothing")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the command outlived the kill")
	}
}

func TestGitEnvAllowReturnsTheAllowedExitStatus(t *testing.T) {
	// main and feature have diverged, so neither is the other's ancestor.
	dir := repoWith(t,
		map[string]string{"a.txt": "a\n"},
		[]map[string]string{{"a.txt": "trunk\n"}},
		[]map[string]string{{"a.txt": "branch\n"}})

	// The allowed status is an answer, not a failure: this is the whole
	// point of the function, so it is what the test must exercise.
	out, code, err := gitEnvAllow(dir, nil, nil, 1, "merge-base", "--is-ancestor", "main", "feature")
	if err != nil || code != 1 || out != "" {
		t.Fatalf("diverged: %q, %d, %v; want code 1 and no error", out, code, err)
	}
	// A true answer still exits 0.
	if _, code, err := gitEnvAllow(dir, nil, nil, 1, "merge-base", "--is-ancestor", "main", "main"); err != nil || code != 0 {
		t.Fatalf("same commit: %d, %v; want code 0", code, err)
	}
	// Any other status is still an error (cat-file exits 128 here).
	if _, _, err := gitEnvAllow(dir, nil, nil, 1, "cat-file", "-p", "notacommit"); err == nil {
		t.Fatal("an unexpected exit status must still be an error")
	}
	// allow < 0 tolerates nothing: that is gitEnv's contract.
	if _, _, err := gitEnvAllow(dir, nil, nil, -1, "merge-base", "--is-ancestor", "main", "feature"); err == nil {
		t.Fatal("allow -1 must not tolerate exit 1")
	}
}
