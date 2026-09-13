// Package gittest runs git for tests and builds the repositories they run
// against, so every package's tests share one helper rather than a copy.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// identity is who every test commit is made by, so a repository does not
// depend on the identity of whoever runs the tests. Nothing here sets an
// editor: a test that wants one names it itself, and the environment would
// win over the config it passes.
var identity = []string{
	"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
	"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com",
}

func command(dir string, env []string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), identity...), env...)
	return cmd
}

// Git runs git in dir and returns its output without the trailing newline,
// failing the test when git fails.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := command(dir, nil, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// Try runs git where a non-zero exit is part of the scenario — a rebase meant
// to stop on a conflict, or a `rebase --continue` that stops at the next one
// — and returns its output and that exit. It never opens an editor.
func Try(t testing.TB, dir string, args ...string) (string, error) {
	t.Helper()
	out, err := command(dir, []string{"GIT_EDITOR=true"}, args...).CombinedOutput()
	return string(out), err
}

// NewRepo creates parent/name: a repository on main, with an identity, no
// signing, and one empty commit to be the base of everything after it.
func NewRepo(t testing.TB, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	Git(t, parent, "init", "-q", "-b", "main", name)
	Git(t, dir, "config", "user.name", "T")
	Git(t, dir, "config", "user.email", "t@example.com")
	Git(t, dir, "config", "commit.gpgsign", "false")
	Git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

// WithOrigin gives dir a bare origin holding what it has now, registers it as
// the remote and fetches it, so the remote-tracking refs match. It returns
// origin's path.
func WithOrigin(t testing.TB, dir string) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	Git(t, dir, "clone", "-q", "--bare", dir, origin)
	Git(t, dir, "remote", "add", "origin", origin)
	Git(t, dir, "fetch", "-q", "origin")
	return origin
}

// WriteFile writes content to path, creating the directories above it.
func WriteFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
