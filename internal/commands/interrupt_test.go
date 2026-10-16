package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// With the SIGTERM grace, a deferred step the interrupt kills returns to the
// run before the handler has exited, and the run would go on to print the
// step as owed, pin refs/wt-sync-result/<branch>/<epoch> and remove the plan,
// leaving the handler's "wt sync undo --force puts it back" stale. The
// goroutine that ran the killed step parks instead, so the rebase stands as
// the handler says, with no result pinned and nothing owed.
func TestInterruptDuringADeferredStepPinsNoResult(t *testing.T) {
	if testing.Short() {
		t.Skip("interrupts a real run with a signal")
	}
	ctx, _ := provisiontest.RunFixture(t, false)
	main := ctx.Repo.MainRoot
	started := filepath.Join(t.TempDir(), "started")
	yaml, err := os.ReadFile(filepath.Join(main, ".wt-sync.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	testkit.WriteFile(t, main, ".wt-sync.yaml", string(yaml)+"defer:\n  - run: touch "+started+"; sleep 30\n    paths: [v.txt]\n")
	testkit.GitIn(t, main, "commit", "-q", "-am", "declare a slow step")
	testkit.GitIn(t, main, "fetch", "-q", "origin")

	var out testkit.LockedBuffer
	go func() { _ = SyncRebase(ctx, []string{"bump"}, noAgents(), &out) }()
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
	if refs := testkit.GitOut(t, main, "for-each-ref", wtsync.ResultPrefix); refs != "" {
		t.Errorf("the run pinned a result after the interrupt: %s", refs)
	}
	if s := out.String(); strings.Contains(s, "owed") || strings.Contains(s, "✗") {
		t.Errorf("the run reported the killed step:\n%s", s)
	}
}

// An interrupt while a run writes the handover leaves the rebase stopped in
// the worktree, with no sidecar yet to explain it. It has to name the
// worktree and what puts it back, as an interrupt during the rebase does.
func TestSyncRunInterruptedWhileHandingOverSaysHowToPutItBack(t *testing.T) {
	if testing.Short() {
		t.Skip("interrupts a real run with a signal")
	}
	if os.Getenv(testkit.InterruptChild) == "" {
		out, status := testkit.InterruptedChild(t)
		if status != kit.InterruptStatus || !strings.Contains(out, "interrupted while rebasing bump") || !strings.Contains(out, "rebase --abort") {
			t.Fatalf("status %d; the interrupt did not say how to put bump back:\n%s", status, out)
		}
		return
	}
	ctx, bump := provisiontest.ContestedFixture(t)
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	testkit.InterruptWhileHandingOver(t, gitDir, func() bool {
		_, err := os.Stat(wtsync.PlanPath(gitDir))
		return err == nil
	})
	err = SyncRebase(ctx, []string{"bump"}, noAgents(), os.Stdout)
	t.Fatalf("the run returned (%v) instead of being interrupted", err)
}

// The same during resume's fresh handover: the rebase and a handover are
// still there for resume to pick up again, and the interrupt says so.
func TestSyncResumeInterruptedWhileHandingOverAgainSaysToResume(t *testing.T) {
	if testing.Short() {
		t.Skip("interrupts a real run with a signal")
	}
	if os.Getenv(testkit.InterruptChild) == "" {
		out, status := testkit.InterruptedChild(t)
		if status != kit.InterruptStatus || !strings.Contains(out, "interrupted while resuming bump") || !strings.Contains(out, "wt sync resume bump") {
			t.Fatalf("status %d; the interrupt did not point back at resume:\n%s", status, out)
		}
		return
	}
	ctx, bump := provisiontest.LaterStopFixture(t, true)
	gitDir, st := handOverNow(t, ctx, bump)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	testkit.GitTry(t, bump, "rebase", "--continue")
	first, err := os.ReadFile(wtsync.PlanPath(gitDir))
	if err != nil {
		t.Fatal(err)
	}
	testkit.InterruptWhileHandingOver(t, gitDir, func() bool {
		now, err := os.ReadFile(wtsync.PlanPath(gitDir))
		return err == nil && !bytes.Equal(now, first)
	})
	err = SyncResume(ctx, st.Branch, noResumeAgents(), os.Stdout)
	t.Fatalf("resume returned (%v) instead of being interrupted", err)
}

// A rebase an interrupt leaves mid-way is a person's to continue by hand,
// and the run's own break and edit lines in its list would stop them where
// the run would have stopped, and land a bump on trunk's number in silence:
// the interrupt takes them out.
func TestOnInterruptTakesTheRunsMarksOutOfTheList(t *testing.T) {
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
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
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
	watchSignalStatus(t, syscall.SIGINT)
}

func TestWatchSignalsSIGTERMStatus(t *testing.T) {
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
