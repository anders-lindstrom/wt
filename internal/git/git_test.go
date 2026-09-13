package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

func newRepo(t *testing.T) string {
	t.Helper()
	return gittest.NewRepo(t, t.TempDir(), "demo")
}

func TestRunReturnsTrimmedOutput(t *testing.T) {
	dir := newRepo(t)
	got, err := Run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "main" {
		t.Errorf("got %q, want %q", got, "main")
	}
}

func TestShortIDKeepsAnIDShorterThanAsked(t *testing.T) {
	const sha = "5f3c2e9f5d4054ae6727b1966e9ab16a37c1409e"
	if got := ShortID(sha, 7); got != "5f3c2e9" {
		t.Errorf("ShortID 7 = %q", got)
	}
	if got := ShortID(sha, 12); got != "5f3c2e9f5d40" {
		t.Errorf("ShortID 12 = %q", got)
	}
	if got := ShortID("abc", 7); got != "abc" {
		t.Errorf("ShortID of a short id = %q", got)
	}
}

func TestRunOutsideRepoReturnsErrNotRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, "rev-parse", "--show-toplevel"); err != ErrNotRepo {
		t.Errorf("got %v, want ErrNotRepo", err)
	}
}

func TestLinesDropsTrailingBlank(t *testing.T) {
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := Lines(dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("Lines: %v", err)
	}
	if len(lines) != 1 {
		t.Errorf("got %d lines %q, want 1", len(lines), lines)
	}
}

func TestRunTimeoutReportsADeadlineRatherThanWaiting(t *testing.T) {
	dir := newRepo(t)
	start := time.Now()
	_, err := RunTimeout(dir, 100*time.Millisecond, "-c", "alias.slow=!sleep 5", "slow")
	if err == nil || err.Error() != "git -c alias.slow=!sleep 5 slow: timed out after 100ms" {
		t.Fatalf("err %v", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("waited %s: the deadline did not take the forked child down", elapsed)
	}
}

func TestRunNamesTheCommandAndExitCodeWhenGitSaysNothing(t *testing.T) {
	dir := newRepo(t)
	_, err := Run(dir, "-c", "alias.fail=!exit 3", "fail")
	if err == nil {
		t.Fatal("want an error")
	}
	if got, want := err.Error(), "git -c alias.fail=!exit 3 fail: exit 3"; got != want {
		t.Fatalf("err %q, want %q: an empty error reads as success", got, want)
	}
}

// A failing git's error is exactly what it said on stderr, trimmed.
func TestRunFailureIsWhatGitSaid(t *testing.T) {
	_, err := Run(newRepo(t), "rev-parse", "--verify", "nope")
	if err == nil || err.Error() != "fatal: Needed a single revision" {
		t.Fatalf("err %v", err)
	}
}

func TestKillRunningTakesDownARunningGit(t *testing.T) {
	dir := newRepo(t)
	done := make(chan error, 1)
	go func() {
		_, err := RunTimeout(dir, time.Minute, "-c", "alias.slow=!sleep 30", "slow")
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		groups.Lock()
		n := len(groups.pids)
		groups.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("RunTimeout registered no process group")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := KillRunning(); n < 1 {
		t.Fatal("KillRunning signalled nothing")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a killed git must report an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the git outlived the kill")
	}
}
