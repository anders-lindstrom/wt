package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// lockedBuffer is a bytes.Buffer a run goroutine may still be writing to
// while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// With the SIGTERM grace, a deferred step the interrupt kills returns to the
// run before the handler has exited, and the run would go on to print the
// step as owed, pin refs/wt-sync-result/<branch>/<epoch> and remove the plan,
// leaving the handler's "wt sync undo --force puts it back" stale. The
// goroutine that ran the killed step parks instead, so the rebase stands as
// the handler says, with no result pinned and nothing owed.
func TestInterruptDuringADeferredStepPinsNoResult(t *testing.T) {
	ctx, _ := runFixture(t, false)
	main := ctx.Repo.MainRoot
	started := filepath.Join(t.TempDir(), "started")
	yaml, err := os.ReadFile(filepath.Join(main, ".wt-sync.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, main, ".wt-sync.yaml", string(yaml)+"defer:\n  - run: touch "+started+"; sleep 30\n    paths: [v.txt]\n")
	gitIn(t, main, "commit", "-q", "-am", "declare a slow step")
	gitIn(t, main, "fetch", "-q", "origin")

	var out lockedBuffer
	go func() { _ = SyncRun(ctx, []string{"bump"}, noAgents(), &out) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the step never started:\n%s", out.String())
		}
	}
	if n := git.Interrupt(); n < 1 {
		t.Fatalf("Interrupt signalled %d groups, want the step", n)
	}
	// Long enough for the run to have finished the step's failure path,
	// had it not parked.
	time.Sleep(500 * time.Millisecond)
	if refs := gitOut(t, main, "for-each-ref", wtsync.ResultPrefix); refs != "" {
		t.Errorf("the run pinned a result after the interrupt: %s", refs)
	}
	if s := out.String(); strings.Contains(s, "owed") || strings.Contains(s, "✗") {
		t.Errorf("the run reported the killed step:\n%s", s)
	}
}

// interruptChild is set in the environment of a test binary that
// interruptedChild re-ran: the test runs its child half there.
const interruptChild = "WT_TEST_INTERRUPT_CHILD"

// interruptedChild re-runs the calling test in a child process, where its
// child half runs a command that is interrupted, and returns what the child
// printed and its exit status. An interrupt ends the child with os.Exit and
// no cleanups, so its temporary files go under this test's TempDir instead.
func interruptedChild(t *testing.T) (out string, status int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1") //nolint:gosec // G702: re-runs this test binary with its own test name
	cmd.Env = append(os.Environ(), interruptChild+"=1", "TMPDIR="+t.TempDir())
	b, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return string(b), cmd.ProcessState.ExitCode()
}

// interruptWhileHandingOver makes the next sidecar write in gitDir block for
// good, on a FIFO nobody opens for reading, and sends this process SIGINT once
// briefWritten says the brief is on disk. The brief is written just before the
// sidecar, so the signal lands in the middle of writing the handover.
func interruptWhileHandingOver(t *testing.T, gitDir string, briefWritten func() bool) {
	t.Helper()
	if err := syscall.Mkfifo(wtsync.StatePath(gitDir)+".tmp", 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for !briefWritten() {
			time.Sleep(10 * time.Millisecond)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
}

// An interrupt while a run writes the handover leaves the rebase stopped in
// the worktree, with no sidecar yet to explain it. It has to name the
// worktree and what puts it back, as an interrupt during the rebase does.
func TestSyncRunInterruptedWhileHandingOverSaysHowToPutItBack(t *testing.T) {
	if os.Getenv(interruptChild) == "" {
		out, status := interruptedChild(t)
		if status != interruptStatus || !strings.Contains(out, "interrupted while rebasing bump") || !strings.Contains(out, "rebase --abort") {
			t.Fatalf("status %d; the interrupt did not say how to put bump back:\n%s", status, out)
		}
		return
	}
	ctx, bump := contestedFixture(t)
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	interruptWhileHandingOver(t, gitDir, func() bool {
		_, err := os.Stat(wtsync.PlanPath(gitDir))
		return err == nil
	})
	err = SyncRun(ctx, []string{"bump"}, noAgents(), os.Stdout)
	t.Fatalf("the run returned (%v) instead of being interrupted", err)
}

// The same during resume's fresh handover: the rebase and a handover are
// still there for resume to pick up again, and the interrupt says so.
func TestSyncResumeInterruptedWhileHandingOverAgainSaysToResume(t *testing.T) {
	if os.Getenv(interruptChild) == "" {
		out, status := interruptedChild(t)
		if status != interruptStatus || !strings.Contains(out, "interrupted while resuming bump") || !strings.Contains(out, "wt sync resume bump") {
			t.Fatalf("status %d; the interrupt did not point back at resume:\n%s", status, out)
		}
		return
	}
	ctx, bump := laterStopFixture(t, true)
	gitDir, st := handOverNow(t, ctx, bump)
	writeFile(t, bump, "a.txt", "merged by hand\n")
	gitOut(t, bump, "add", "--", "a.txt")
	gitTry(t, bump, "rebase", "--continue")
	first, err := os.ReadFile(wtsync.PlanPath(gitDir))
	if err != nil {
		t.Fatal(err)
	}
	interruptWhileHandingOver(t, gitDir, func() bool {
		now, err := os.ReadFile(wtsync.PlanPath(gitDir))
		return err == nil && !bytes.Equal(now, first)
	})
	err = SyncResume(ctx, st.Branch, noResumeAgents(), os.Stdout)
	t.Fatalf("resume returned (%v) instead of being interrupted", err)
}

func TestOnInterruptReleasesEveryLockItHolds(t *testing.T) {
	// Two lock files in two stand-in git dirs: a run holding several must
	// not leave any of them for LockExpiry.
	var locks []*wtsync.Lock
	var dirs []string
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		l, err := wtsync.Acquire(dir, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		locks = append(locks, l)
		dirs = append(dirs, dir)
	}
	var out bytes.Buffer
	onInterrupt(&out, locks, nil)
	for _, dir := range dirs {
		if _, ok, err := wtsync.ReadLock(dir); err != nil || ok {
			t.Fatalf("%s: lock survived (ok %v err %v)", dir, ok, err)
		}
	}
	if !strings.Contains(out.String(), "interrupted") {
		t.Fatalf("out %q", out.String())
	}
}

func TestOnInterruptSaysHowToPutBackAWorktreeLeftMidRebase(t *testing.T) {
	var out bytes.Buffer
	onInterrupt(&out, nil, &rebaseInFlight{work: "bump", path: "/w/bump", safety: "refs/wt-sync/feat_wt/bump/99"})
	for _, want := range []string{"interrupted while rebasing bump", "git -C /w/bump rebase --abort", "refs/wt-sync/feat_wt/bump/99"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out lacks %q:\n%s", want, out.String())
		}
	}
}

// A resume's rebase holds a person's own resolution: the abort that is the
// right advice for a run would discard it. The sidecar and the rebase both
// survive an interrupt, so the safe answer is to resume again.
func TestOnInterruptDuringAResumePointsAtResumeNotAbort(t *testing.T) {
	var out bytes.Buffer
	onInterrupt(&out, nil, &rebaseInFlight{work: "bump", path: "/w/bump", safety: "refs/wt-sync/feat_wt/bump/99", resuming: true})
	s := out.String()
	if !strings.Contains(s, "interrupted while resuming bump") || !strings.Contains(s, "wt sync resume bump") {
		t.Fatalf("out %q", s)
	}
	if strings.Contains(s, "rebase --abort") {
		t.Fatalf("the interrupt advises the abort that discards the person's resolution: %q", s)
	}
}

// A rebase an interrupt leaves mid-way is a person's to continue by hand,
// and the run's own break and edit lines in its list would stop them where
// the run would have stopped, and land a bump on trunk's number in silence:
// the interrupt takes them out.
func TestOnInterruptTakesTheRunsMarksOutOfTheList(t *testing.T) {
	dir := committedRepo(t, minimalConf)
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "base\n")
	write("v.txt", "1.0.0\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	gitIn(t, dir, "checkout", "-q", "-b", "feature")
	write("a.txt", "branch\n")
	gitIn(t, dir, "commit", "-q", "-am", "a on branch")
	write("v.txt", "1.0.1\n")
	gitIn(t, dir, "commit", "-q", "-am", "bump")
	bump := gitOut(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "main")
	write("a.txt", "trunk\n")
	gitIn(t, dir, "commit", "-q", "-am", "a on trunk")
	gitIn(t, dir, "checkout", "-q", "feature")
	// The list the run would have: a break before and an edit in place of
	// the bump, stopped at the conflict before it.
	editor := filepath.Join(t.TempDir(), "mark.sh")
	script := "#!/bin/sh\nwhile IFS= read -r line || [ -n \"$line\" ]; do\n  case \"$line\" in\n    \"pick \"*) id=${line#pick }; id=${id%% *}; case \"" + bump + "\" in \"$id\"*) printf 'break\\n'; printf '%s\\n' \"edit ${line#pick }\" ;; *) printf '%s\\n' \"$line\" ;; esac ;;\n    *) printf '%s\\n' \"$line\" ;;\n  esac\ndone < \"$1\" > \"$1.wt\" && mv \"$1.wt\" \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-c", "rebase.backend=merge", "rebase", "--interactive", "--empty=drop", "main")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR="+editor)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the rebase did not stop at the conflict:\n%s", out)
	}
	rebaseDir := gitOut(t, dir, "rev-parse", "--git-path", "rebase-merge")
	if !filepath.IsAbs(rebaseDir) {
		rebaseDir = filepath.Join(dir, rebaseDir)
	}
	todoPath := filepath.Join(rebaseDir, "git-rebase-todo")
	before, err := os.ReadFile(todoPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "break\nedit ") {
		t.Fatalf("the fixture's list carries no marks:\n%s", before)
	}
	var out bytes.Buffer
	onInterrupt(&out, nil, &rebaseInFlight{work: "bump", path: dir, safety: "refs/wt-sync/feature/1"})
	after, err := os.ReadFile(todoPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "break") || strings.Contains(string(after), "edit ") || !strings.Contains(string(after), "pick ") {
		t.Fatalf("the list after the interrupt:\n%s", after)
	}
	if strings.Contains(out.String(), "still carries") || !strings.Contains(out.String(), "interrupted while rebasing bump") {
		t.Fatalf("out %q", out.String())
	}
}
