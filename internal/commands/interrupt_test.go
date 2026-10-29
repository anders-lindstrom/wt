package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// A rebase an interrupt leaves mid-way is a person's to continue by hand,
// and the run's own break and edit lines in its list would stop them where
// the run would have stopped, and land a bump on trunk's number in silence:
// the interrupt takes them out.
func TestOnInterruptTakesTheRunsMarksOutOfTheList(t *testing.T) {
	// Not parallel: signals every process group the test binary runs; removes every temporary directory the test binary has open.
	dir := testkit.CommittedRepo(t, testkit.MinimalConf)
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "base\n")
	write("v.txt", "1.0.0\n")
	testkit.GitIn(t, dir, "add", "-A")
	testkit.GitIn(t, dir, "commit", "-q", "-m", "base")
	testkit.GitIn(t, dir, "checkout", "-q", "-b", "feature")
	write("a.txt", "branch\n")
	testkit.GitIn(t, dir, "commit", "-q", "-am", "a on branch")
	write("v.txt", "1.0.1\n")
	testkit.GitIn(t, dir, "commit", "-q", "-am", "bump")
	bump := testkit.GitOut(t, dir, "rev-parse", "HEAD")
	testkit.GitIn(t, dir, "checkout", "-q", "main")
	write("a.txt", "trunk\n")
	testkit.GitIn(t, dir, "commit", "-q", "-am", "a on trunk")
	testkit.GitIn(t, dir, "checkout", "-q", "feature")
	// The list the run would have: a break before and an edit in place of
	// the bump, stopped at the conflict before it.
	editor := filepath.Join(t.TempDir(), "mark.sh")
	script := "#!/bin/sh\nwhile IFS= read -r line || [ -n \"$line\" ]; do\n  case \"$line\" in\n    \"pick \"*) id=${line#pick }; id=${id%% *}; case \"" + bump + "\" in \"$id\"*) printf 'break\\n'; printf '%s\\n' \"edit ${line#pick }\" ;; *) printf '%s\\n' \"$line\" ;; esac ;;\n    *) printf '%s\\n' \"$line\" ;;\n  esac\ndone < \"$1\" > \"$1.wt\" && mv \"$1.wt\" \"$1\"\n"
	gittest.WriteExecutable(t, editor, script)
	cmd := exec.Command("git", "-c", "rebase.backend=merge", "rebase", "--interactive", "--empty=drop", "main")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR="+editor)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the rebase did not stop at the conflict:\n%s", out)
	}
	rebaseDir := testkit.GitOut(t, dir, "rev-parse", "--git-path", "rebase-merge")
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
	kit.OnInterrupt(&out, nil, &kit.RebaseInFlight{Work: "bump", Path: dir, Safety: "refs/wt-sync/feature/1"})
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

func TestWatchSignalsSIGINTStatus(t *testing.T) {
	t.Parallel()
	watchSignalStatus(t, syscall.SIGINT)
}

func TestWatchSignalsSIGTERMStatus(t *testing.T) {
	t.Parallel()
	watchSignalStatus(t, syscall.SIGTERM)
}

func watchSignalStatus(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if os.Getenv(testkit.InterruptChild) == "" {
		out, status := testkit.InterruptedChild(t)
		if status != 128+int(sig) || !strings.Contains(out, "interrupted") {
			t.Fatalf("signal %s: status %d, output %q", sig, status, out)
		}
		return
	}
	defer kit.WatchSignals(os.Stderr, nil)()
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatal(err)
	}
	select {}
}

// slowWriter holds every write back for a moment, so that a second handler
// acting on the same signal has the time to print before this one exits.
type slowWriter struct{ w *os.File }

func (s slowWriter) Write(p []byte) (int, error) {
	time.Sleep(300 * time.Millisecond)
	return s.w.Write(p)
}

// tagWriter prefixes everything written through it, so the parent can tell
// which handler printed.
type tagWriter struct {
	tag string
	w   *os.File
}

func (t tagWriter) Write(p []byte) (int, error) {
	_, err := fmt.Fprintf(t.w, "%s%s", t.tag, p)
	return len(p), err
}

// wt sync --rebase --all watches for the whole run and each repository's
// rebase watches again inside it. Only the innermost acts on a signal: the
// outer one has no tracker, and exiting first it would cut off the way back
// for the worktree mid-rebase.
func TestWatchSignalsNestedOnlyTheInnermostActs(t *testing.T) {
	t.Parallel()
	if os.Getenv(testkit.InterruptChild) == "" {
		out, status := testkit.InterruptedChild(t)
		if status != kit.InterruptStatus || !strings.Contains(out, "interrupted while rebasing bump") {
			t.Fatalf("status %d; the inner handler did not say how to put bump back:\n%s", status, out)
		}
		if strings.Contains(out, "OUTER") {
			t.Fatalf("the outer handler acted on the signal too:\n%s", out)
		}
		return
	}
	defer kit.WatchSignals(tagWriter{tag: "OUTER", w: os.Stderr}, nil)()
	var tr kit.RebaseTracker
	tr.Set(&kit.RebaseInFlight{Work: "bump", Path: t.TempDir(), Safety: "refs/wt-sync/feature/1"})
	defer kit.WatchSignals(slowWriter{w: os.Stderr}, &tr)()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {}
}

// Once the inner watch ends, the outer one is the one a signal finds again.
func TestWatchSignalsOuterActsAgainOnceTheInnerEnds(t *testing.T) {
	t.Parallel()
	if os.Getenv(testkit.InterruptChild) == "" {
		out, status := testkit.InterruptedChild(t)
		if status != kit.InterruptStatus || !strings.Contains(out, "OUTER\ninterrupted") || strings.Contains(out, "INNER") {
			t.Fatalf("status %d; want only the outer handler:\n%s", status, out)
		}
		return
	}
	defer kit.WatchSignals(tagWriter{tag: "OUTER", w: os.Stderr}, nil)()
	kit.WatchSignals(tagWriter{tag: "INNER", w: os.Stderr}, nil)()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {}
}
