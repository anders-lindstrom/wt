package git

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestExecReturnsStdoutUntrimmedAndReadsStdin(t *testing.T) {
	out, err := Exec(Opts{Dir: t.TempDir(), Stdin: strings.NewReader("hello\n")}, "hash-object", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "ce013625030ba8dba906f756967f9e9ca394464a\n"; got != want {
		t.Fatalf("out %q, want %q", got, want)
	}
}

// An inherited key must not win over the one a caller sets.
func TestExecSetsEnvOverTheInheritedEntry(t *testing.T) {
	dir := newRepo(t)
	t.Setenv("WT_TEST_VALUE", "inherited")
	out, err := Exec(Opts{Dir: dir, Env: []string{"WT_TEST_VALUE=ours"}}, "-c", "alias.print-value=!printenv WT_TEST_VALUE", "print-value")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "ours\n" {
		t.Fatalf("WT_TEST_VALUE %q, want ours", got)
	}
	t.Setenv("GIT_INDEX_FILE", "/inherited")
	n := 0
	for _, kv := range Environ("GIT_INDEX_FILE=/ours") {
		if strings.HasPrefix(kv, "GIT_INDEX_FILE=") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("Environ kept %d GIT_INDEX_FILE entries, want 1", n)
	}
}

func TestExecErrorCarriesStderrAndTheExitStatus(t *testing.T) {
	dir := newRepo(t)
	_, err := Exec(Opts{Dir: dir}, "rev-parse", "--verify", "nope")
	var gerr *Error
	if !errors.As(err, &gerr) {
		t.Fatalf("err %T %v, want *Error", err, err)
	}
	if gerr.Code != 128 || gerr.TimedOut || strings.Join(gerr.Args, " ") != "rev-parse --verify nope" {
		t.Fatalf("error %+v", gerr)
	}
	if got, want := err.Error(), "fatal: Needed a single revision"; got != want {
		t.Fatalf("text %q, want %q", got, want)
	}

	_, err = Exec(Opts{Dir: dir}, "-c", "alias.fail=!exit 3", "fail")
	if got, want := err.Error(), "git -c alias.fail=!exit 3 fail: exit 3"; got != want {
		t.Fatalf("silent failure %q, want %q", got, want)
	}
}

func TestExecReportsADeadlineAndKillsWhatGitForked(t *testing.T) {
	dir := newRepo(t)
	start := time.Now()
	out, err := Exec(Opts{Dir: dir, Timeout: 100 * time.Millisecond}, "-c", "alias.slow=!sleep 5", "slow")
	var gerr *Error
	if !errors.As(err, &gerr) || !gerr.TimedOut || gerr.Code != -1 || out != nil {
		t.Fatalf("out %q err %v", out, err)
	}
	if got, want := err.Error(), "git -c alias.slow=!sleep 5 slow: timed out after 100ms"; got != want {
		t.Fatalf("text %q, want %q", got, want)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("waited %s: the deadline did not take the forked child down", elapsed)
	}
}

func TestAnswerTellsAnAnswerFromAFailure(t *testing.T) {
	dir := newRepo(t)
	if _, err := Exec(Opts{Dir: dir}, "commit", "-q", "--allow-empty", "-m", "second"); err != nil {
		t.Fatal(err)
	}
	_, err := Exec(Opts{Dir: dir}, "merge-base", "--is-ancestor", "HEAD", "HEAD~1")
	if code, err := Answer(err, 1); code != 1 || err != nil {
		t.Fatalf("exit 1 allowed: %d, %v", code, err)
	}
	if code, err := Answer(nil, 1); code != 0 || err != nil {
		t.Fatalf("success: %d, %v", code, err)
	}
	_, err = Exec(Opts{Dir: dir}, "cat-file", "-p", "notacommit")
	if _, got := Answer(err, 1); got != err {
		t.Fatalf("exit 128 is not an answer: %v", got)
	}
	if _, got := Answer(&Error{Code: -1, TimedOut: true}, -1); got == nil {
		t.Fatal("a git with no exit status gave no answer")
	}
}

func TestRunBoundedReportsTheExitCode(t *testing.T) {
	timedOut, code, err := RunBounded(time.Minute, exec.Command("sh", "-c", "exit 3"))
	if timedOut || code != 3 || err == nil {
		t.Fatalf("timedOut %v code %d err %v", timedOut, code, err)
	}
	timedOut, code, err = RunBounded(time.Minute, exec.Command("wt-no-such-command"))
	if timedOut || code != -1 || err == nil {
		t.Fatalf("never started: timedOut %v code %d err %v", timedOut, code, err)
	}
}

// The shell's sleep is a child holding stdout, so only a kill of the whole
// group returns in time.
func TestRunBoundedKillsTheGroupAtTheDeadline(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30; echo done")
	var out strings.Builder
	cmd.Stdout = &out
	start := time.Now()
	timedOut, _, _ := RunBounded(200*time.Millisecond, cmd)
	if !timedOut {
		t.Fatal("the deadline did not fire")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond+WaitDelay+time.Second {
		t.Fatalf("took %s: the group was not killed", elapsed)
	}
}

func TestKillRunningTakesDownABoundedCommand(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, _, err := RunBounded(time.Minute, exec.Command("sleep", "30"))
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
			t.Fatal("RunBounded registered no process group")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := KillRunning(); n < 1 {
		t.Fatal("KillRunning signalled nothing")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a killed command must report an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the command outlived the kill")
	}
}
