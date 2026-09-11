package git

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// WaitDelay bounds how long RunBounded waits for a process's stdio to close
// once it has exited or the deadline has killed its group. A shell's last
// command often runs as a forked child, and a child that detached from the
// group before the kill reached it can keep a pipe open for good.
const WaitDelay = 2 * time.Second

// groups is the process group of every command RunBounded is waiting on. A
// signal handler runs on its own goroutine and has no other way to reach
// them; without this a Ctrl-C leaves a fetch, a script or a deferred step
// running after wt has exited.
var groups = struct {
	sync.Mutex
	pids map[int]bool
}{pids: map[int]bool{}}

// KillRunning sends SIGKILL to the process group of every command RunBounded
// is waiting on, and reports how many it signalled. Best effort: a group that
// has already exited is not an error.
func KillRunning() int {
	groups.Lock()
	defer groups.Unlock()
	n := 0
	for pid := range groups.pids {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
			n++
		}
	}
	return n
}

// track registers the process group pid until done is called.
func track(pid int) (done func()) {
	groups.Lock()
	groups.pids[pid] = true
	groups.Unlock()
	return func() {
		groups.Lock()
		delete(groups.pids, pid)
		groups.Unlock()
	}
}

// RunBounded runs cmd in its own process group, registered for KillRunning
// while it runs, and kills the whole group once timeout has passed; zero or
// less means no deadline. cmd must not have been started.
//
// timedOut reports that the deadline fired before Wait returned. It wins over
// err: a command that exited 0 but left a child holding stdio open past
// WaitDelay comes back as exec.ErrWaitDelay, which looks like success, yet may
// have run past its deadline. code is the exit status: 0 when err is nil, -1
// when there is none (killed by a signal, never started, ErrWaitDelay). err is
// what Start or Wait returned, for the caller to judge.
func RunBounded(timeout time.Duration, cmd *exec.Cmd) (timedOut bool, code int, err error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = WaitDelay
	deadline := time.Now().Add(timeout)
	if err := cmd.Start(); err != nil {
		return false, -1, err
	}
	pid := cmd.Process.Pid
	defer track(pid)()

	var mu sync.Mutex
	waited, fired := false, false
	if timeout > 0 {
		timer := time.AfterFunc(time.Until(deadline), func() {
			mu.Lock()
			defer mu.Unlock()
			if !waited {
				fired = true
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		})
		defer timer.Stop()
	}
	err = cmd.Wait()
	mu.Lock()
	waited = true
	timedOut = fired
	mu.Unlock()

	var exit *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		code = -1
	}
	return timedOut, code, err
}

// Environ is the process environment with extra set over it. An existing
// entry for a key extra sets is dropped rather than shadowed: some getenv
// implementations return the first match, so an inherited GIT_INDEX_FILE
// ahead of ours must not survive.
func Environ(extra ...string) []string {
	keys := make(map[string]bool, len(extra))
	for _, kv := range extra {
		if k, _, ok := strings.Cut(kv, "="); ok {
			keys[k] = true
		}
	}
	env := os.Environ()
	out := make([]string, 0, len(env)+len(extra))
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && keys[k] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// Opts is how Exec runs one git.
type Opts struct {
	Dir string
	// Env is set over the process environment; see Environ.
	Env   []string
	Stdin io.Reader
	// Timeout is the deadline; zero means GitTimeout.
	Timeout time.Duration
}

// Error is a git that failed or did not answer in time.
type Error struct {
	Args   []string
	Stderr string
	// Code is git's exit status, or -1 when it has none: cut off by the
	// deadline, killed by a signal, or never started.
	Code     int
	TimedOut bool
	// Timeout is the deadline git ran under.
	Timeout time.Duration
	// Err is what running git reported; nil when TimedOut.
	Err error
}

// Error is what git said on stderr. A git that failed silently still says
// something, since an empty error reads as success wherever it is printed:
// the command and its exit status, or what happened to it instead.
func (e *Error) Error() string {
	cmd := "git " + strings.Join(e.Args, " ")
	if e.TimedOut {
		return fmt.Sprintf("%s: timed out after %s", cmd, e.Timeout)
	}
	if msg := strings.TrimSpace(e.Stderr); msg != "" {
		return msg
	}
	var exit *exec.ExitError
	if errors.As(e.Err, &exit) {
		// A process killed by a signal has no exit code (-1); say what
		// actually happened to it instead of printing that.
		if code := exit.ExitCode(); code >= 0 {
			return fmt.Sprintf("%s: exit %d", cmd, code)
		}
		return fmt.Sprintf("%s: %s", cmd, exit.ProcessState)
	}
	return fmt.Sprintf("%s: %v", cmd, e.Err)
}

// Unwrap returns what running git reported.
func (e *Error) Unwrap() error { return e.Err }

// Exec runs git under a deadline, in its own process group so the deadline
// and KillRunning take down whatever git forked, and returns its stdout
// untrimmed. A failure is an *Error, returned with the stdout git wrote before
// failing; a timeout returns no stdout.
//
// git runs with GIT_TERMINAL_PROMPT=0, so a repository wanting credentials
// fails instead of blocking on a prompt nobody is there to answer.
func Exec(o Opts, args ...string) ([]byte, error) {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = GitTimeout
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = o.Dir
	cmd.Env = Environ(append([]string{"GIT_TERMINAL_PROMPT=0"}, o.Env...)...)
	cmd.Stdin = o.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	timedOut, code, err := RunBounded(timeout, cmd)
	if timedOut {
		return nil, &Error{Args: args, Stderr: stderr.String(), Code: -1, TimedOut: true, Timeout: timeout}
	}
	if err != nil {
		return stdout.Bytes(), &Error{Args: args, Stderr: stderr.String(), Code: code, Timeout: timeout, Err: err}
	}
	return stdout.Bytes(), nil
}

// Answer reads an exit status that is an answer rather than a failure, such
// as merge-base --is-ancestor's 1 for "no". It returns 0 for a nil err, the
// status for a git that exited with one of answers, and err itself otherwise.
func Answer(err error, answers ...int) (int, error) {
	if err == nil {
		return 0, nil
	}
	var gerr *Error
	if errors.As(err, &gerr) && gerr.Code >= 0 && slices.Contains(answers, gerr.Code) {
		return gerr.Code, nil
	}
	return 0, err
}
