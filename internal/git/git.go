// Package git is a thin wrapper over the git CLI. Every worktree fact wt
// relies on comes from git itself rather than from the shape of a path.
package git

import (
	"errors"
	"strings"
	"time"
)

// ErrNotRepo is returned when a command runs outside a git repository.
var ErrNotRepo = errors.New("not a git repository")

// GitTimeout is the deadline every git invocation gets when the caller names
// none. Nothing here is interactive, so a git that has not answered by then
// is stuck rather than slow, and wt must not sit on it forever.
const GitTimeout = 10 * time.Minute

// Run executes git in dir under GitTimeout and returns trimmed stdout.
func Run(dir string, args ...string) (string, error) {
	return RunTimeout(dir, GitTimeout, args...)
}

// RunTimeout is Run with an explicit deadline, for the calls that reach a
// network (a fetch) or that a caller wants bounded tighter. git runs through
// Exec; one outside a repository fails with ErrNotRepo.
func RunTimeout(dir string, d time.Duration, args ...string) (string, error) {
	out, err := Exec(Opts{Dir: dir, Timeout: d}, args...)
	if err != nil {
		var gerr *Error
		if errors.As(err, &gerr) && !gerr.TimedOut && strings.Contains(gerr.Stderr, "not a git repository") {
			return "", ErrNotRepo
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
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
