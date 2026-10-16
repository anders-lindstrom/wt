package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

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
