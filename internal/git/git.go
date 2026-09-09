// Package git is a thin wrapper over the git CLI. Every worktree fact wt
// relies on comes from git itself rather than from the shape of a path.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ErrNotRepo is returned when a command runs outside a git repository.
var ErrNotRepo = errors.New("not a git repository")

// GitTimeout is the deadline every git invocation gets when the caller names
// none. Nothing here is interactive, so a git that has not answered by then
// is stuck rather than slow, and wt must not sit on it forever.
const GitTimeout = 10 * time.Minute

// waitDelay bounds how long Run waits for stdio to close once the deadline
// has killed the process group, for the rare child that detaches from the
// group before the kill reaches it and keeps the pipe open.
const waitDelay = 2 * time.Second

// Run executes git in dir under GitTimeout and returns trimmed stdout.
func Run(dir string, args ...string) (string, error) {
	return RunTimeout(dir, GitTimeout, args...)
}

// RunTimeout is Run with an explicit deadline, for the calls that reach a
// network (a fetch) or that a caller wants bounded tighter.
//
// git runs with GIT_TERMINAL_PROMPT=0, so a repository wanting credentials
// fails instead of blocking on a prompt nobody is there to answer, and in
// its own process group, so the deadline takes down whatever git forked.
func RunTimeout(dir string, d time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s: timed out after %s", strings.Join(args, " "), d)
	}
	if err != nil {
		if strings.Contains(stderr.String(), "not a git repository") {
			return "", ErrNotRepo
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", errors.New(msg)
		}
		// A git that fails silently must still say something: an empty
		// error reads as success wherever it is printed.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("git %s: exit %d", strings.Join(args, " "), exit.ExitCode())
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Lines runs git and splits stdout into lines, dropping a trailing blank.
func Lines(dir string, args ...string) ([]string, error) {
	out, err := Run(dir, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}
