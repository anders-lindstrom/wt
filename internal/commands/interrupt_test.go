package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

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
