package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

func TestPortIsStableAndDiffersBetweenBranches(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := kit.Open(main)
	first, err := Port(ctx, "fix/login-crash")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Port(ctx, "fix/login-crash")
	if first != again {
		t.Errorf("the same branch gave %d, then %d", first, again)
	}
	if first < 10000 || first > 19999 {
		t.Errorf("port %d outside 10000–19999", first)
	}
	// Fixed names, so a hash collision cannot make this flaky.
	if a, b := PortFor("/r", "fix_wt/login-crash"), PortFor("/r", "fix_wt/logout-crash"); a == b {
		t.Errorf("two branches share port %d", a)
	}
	if a, b := PortFor("/r", "fix_wt/login-crash"), PortFor("/r-elsewhere", "fix_wt/login-crash"); a == b {
		t.Errorf("two repositories share port %d for one branch", a)
	}
}

// The hash is pinned: changing it moves every worktree's port.
func TestPortForIsPinned(t *testing.T) {
	if got := PortFor("/r", "fix_wt/a"); got != 14215 {
		t.Errorf("PortFor = %d, want 14215", got)
	}
}

// An existing worktree's port is its branch's, by any name, and "." from
// inside it; the work `wt new` would create gets the port it would have.
func TestPortOfAnExistingWorktree(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := kit.Open(main)
	before, err := Port(ctx, "fix/login-crash")
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(filepath.Dir(main), "demo_wt", "fix_wt", "login-crash")
	gitIn(t, main, "worktree", "add", "-q", "-b", "fix_wt/login-crash", wt)
	for _, spec := range []string{"fix/login-crash", "login-crash", "fix_wt/login-crash", wt} {
		if got, err := Port(ctx, spec); err != nil || got != before {
			t.Errorf("%s: %d %v, want %d", spec, got, err, before)
		}
	}
	inside, _ := kit.Open(wt)
	if got, err := Port(inside, "."); err != nil || got != before {
		t.Errorf(".: %d %v, want %d", got, err, before)
	}
}

// provision.sh and the build command get the worktree's port, branch and
// work name, and the port is the one `wt port` prints.
func TestProvisionAndBuildGetWTPort(t *testing.T) {
	main := committedRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_COMMAND=\"env | grep ^WT_ > build-env.txt\"\n")
	p := filepath.Join(main, "bin", "worktree", "provision.sh")
	mustWrite(t, p, "#!/bin/sh\nenv | grep ^WT_ > provision-env.txt\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	// A value the caller's shell exported for another worktree is replaced.
	t.Setenv("WT_PORT", "1")
	ctx, _ := kit.Open(main)
	var buf bytes.Buffer
	path, err := New(ctx, "fix/login-crash", NewOptions{}, &buf)
	if err != nil {
		t.Fatalf("New: %v\n%s", err, buf.String())
	}
	port, err := Port(ctx, "fix/login-crash")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"provision-env.txt", "build-env.txt"} {
		env := mustRead(t, filepath.Join(path, f))
		lines := strings.Split(env, "\n")
		if slices.Contains(lines, "WT_PORT=1") {
			t.Errorf("%s kept the inherited WT_PORT:\n%s", f, env)
		}
		for _, want := range []string{
			"WT_PORT=" + strconv.Itoa(port),
			"WT_BRANCH=fix_wt/login-crash",
			"WT_WORK=login-crash",
		} {
			if !slices.Contains(lines, want) {
				t.Errorf("%s lacks %q:\n%s", f, want, env)
			}
		}
	}
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// wt port and WT_PORT agree in a worktree stopped in a rebase, where
// symbolic-ref has no branch but git worktree list still names it.
func TestWorktreeEnvDuringARebaseMatchesWtPort(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := kit.Open(main)
	mustWrite(t, filepath.Join(main, "x.txt"), "base")
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-qm", "base")
	wt := filepath.Join(filepath.Dir(main), "demo_wt", "fix_wt", "login-crash")
	gitIn(t, main, "worktree", "add", "-q", "-b", "fix_wt/login-crash", wt)
	mustWrite(t, filepath.Join(wt, "x.txt"), "branch")
	gitIn(t, wt, "commit", "-qam", "branch")
	mustWrite(t, filepath.Join(main, "x.txt"), "main")
	gitIn(t, main, "commit", "-qam", "main")
	if out, err := exec.Command("git", "-C", wt, "rebase", "main").CombinedOutput(); err == nil {
		t.Fatalf("the rebase did not stop:\n%s", out)
	}
	want, err := Port(ctx, "login-crash")
	if err != nil {
		t.Fatal(err)
	}
	env := worktreeEnv(ctx, wt)
	if got := envValue(env, "WT_PORT"); got != strconv.Itoa(want) {
		t.Errorf("WT_PORT %s, wt port %d", got, want)
	}
	if got := envValue(env, "WT_BRANCH"); got != "fix_wt/login-crash" {
		t.Errorf("WT_BRANCH %q", got)
	}
}

// From a subdirectory of a worktree, as wt setup is run, the environment is
// the containing worktree's.
func TestWorktreeEnvInASubdirectoryIsTheWorktrees(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := kit.Open(main)
	wt := filepath.Join(filepath.Dir(main), "demo_wt", "fix_wt", "login-crash")
	gitIn(t, main, "worktree", "add", "-q", "-b", "fix_wt/login-crash", wt)
	sub := filepath.Join(wt, "pkg", "deep")
	mustMkdir(t, sub)
	want := worktreeEnv(ctx, wt)
	if got := worktreeEnv(ctx, sub); !slices.Equal(got, want) {
		t.Errorf("from a subdirectory %v, want %v", got, want)
	}
	if got := envValue(want, "WT_WORK"); got != "login-crash" {
		t.Fatalf("WT_WORK %q", got)
	}
	// Detached, the port hashes the worktree's root, not the subdirectory.
	gitIn(t, wt, "checkout", "-q", "--detach")
	if got := worktreeEnv(ctx, sub); !slices.Equal(got, worktreeEnv(ctx, wt)) {
		t.Errorf("detached, from a subdirectory %v, want %v", got, worktreeEnv(ctx, wt))
	}
}
