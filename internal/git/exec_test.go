package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	killRunningTakesDown(t, 1)
}

// Several commands waited on at once, as the sync overview runs them, are
// all registered and all killed by the one call.
func TestKillRunningTakesDownEveryBoundedCommand(t *testing.T) {
	killRunningTakesDown(t, 3)
}

func killRunningTakesDown(t *testing.T, n int) {
	t.Helper()
	done := make(chan error, n)
	for range n {
		go func() {
			_, _, err := RunBounded(time.Minute, exec.Command("sleep", "30"))
			done <- err
		}()
	}
	waitRegistered(t, n)
	if killed := KillRunning(); killed < n {
		t.Fatalf("KillRunning signalled %d groups, want %d", killed, n)
	}
	for range n {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("a killed command must report an error")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a command outlived the kill")
		}
	}
}

// waitRegistered blocks until n process groups are registered.
func waitRegistered(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		groups.Lock()
		registered := len(groups.pids)
		groups.Unlock()
		if registered >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("RunBounded registered %d process groups, want %d", registered, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A command that handles SIGTERM gets to finish its cleanup before anything
// harder arrives: a git fetch removes its lock files that way.
func TestKillRunningLetsACommandCleanUpOnSIGTERM(t *testing.T) {
	dir := t.TempDir()
	marker, ready := filepath.Join(dir, "cleaned-up"), filepath.Join(dir, "ready")
	// The shell says when its trap is in place: a SIGTERM before that ends it
	// the default way, with nothing to show for it.
	cmd := exec.Command("sh", "-c", "trap 'touch \"$0\"; exit 0' TERM; touch \"$1\"; sleep 30 & wait $!", marker, ready)
	done := make(chan struct{})
	go func() {
		_, _, _ = RunBounded(time.Minute, cmd)
		close(done)
	}()
	waitRegistered(t, 1)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the shell never installed its trap")
		}
	}
	KillRunning()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the command outlived the kill")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the command was not given the chance to clean up: %v", err)
	}
}

// One that ignores SIGTERM is killed once the grace has passed, so Ctrl-C
// stays snappy.
func TestKillRunningKillsWhatIgnoresSIGTERMAfterTheGrace(t *testing.T) {
	cmd := exec.Command("sh", "-c", "trap '' TERM; while :; do sleep 1; done")
	done := make(chan struct{})
	go func() {
		_, _, _ = RunBounded(time.Minute, cmd)
		close(done)
	}()
	waitRegistered(t, 1)
	start := time.Now()
	KillRunning()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the command outlived the kill")
	}
	if elapsed := time.Since(start); elapsed > TermGrace+WaitDelay+time.Second {
		t.Fatalf("took %s: the group was not killed after the grace", elapsed)
	}
}

// A signal handler that exits the process must not race the goroutine whose
// git it killed: that goroutine would go on to report a failure, drop a lock
// or start another git during the grace. Interrupt marks the groups it
// signals, and the RunBounded waiting on each parks once it is reaped.
func TestInterruptParksTheCallerOfAKilledCommand(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	returned := make(chan struct{})
	go func() {
		_, _, _ = RunBounded(time.Minute, cmd)
		close(returned)
	}()
	waitRegistered(t, 1)
	if n := Interrupt(); n != 1 {
		t.Fatalf("Interrupt signalled %d groups, want 1", n)
	}
	if left := stillRunning([]int{cmd.Process.Pid}); len(left) != 0 {
		t.Fatalf("the group is still registered after the grace: %v", left)
	}
	select {
	case <-returned:
		t.Fatal("RunBounded returned after an interrupt")
	case <-time.After(300 * time.Millisecond):
	}
}
