// Package git is a thin wrapper over the git CLI. Every worktree fact wt
// relies on comes from git itself rather than from the shape of a path.
package git

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// ErrNotRepo is returned when a command runs outside a git repository.
var ErrNotRepo = errors.New("not a git repository")

// Run executes git in dir and returns trimmed stdout.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "not a git repository") {
			return "", ErrNotRepo
		}
		return "", errors.New(strings.TrimSpace(stderr.String()))
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
