// Package gittest runs git for tests and builds the repositories they run
// against, so every package's tests share one helper rather than a copy.
package gittest

import (
	"fmt"
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

// Isolate keeps a package's tests off the machine running them, for the
// tests' own git and the code's under test alike. Git gets a global config
// of its own and no system one, so no hooksPath, signing, credential helper or
// url rewrite of the user's applies, and none of the GIT_ variables the
// environment carries (an agent launcher's GIT_CONFIG_* and GIT_SSH_COMMAND,
// a hook's GIT_DIR). HOME is an empty directory, with no XDG_CONFIG_HOME,
// CODEX_HOME or WT_ROOTS pointing elsewhere, and the PATH finds no claude:
// where none is installed, as in CI, is the state a test starts from, and one
// that wants a claude puts its own first. The global config holds one thing:
// no automatic maintenance, whose detached git would still be working in a
// repository when the test's next command, a clone of it say, reads the same
// files. Call it from TestMain before m.Run and the returned func after.
func Isolate() func() {
	dir, err := os.MkdirTemp("", "wt-gittest")
	if err != nil {
		panic(err)
	}
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, []byte("[maintenance]\n\tauto = false\n"), 0o644); err != nil {
		panic(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		panic(err)
	}
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "GIT_") {
			_ = os.Unsetenv(name)
		}
	}
	_ = os.Unsetenv("WT_ROOTS")
	_ = os.Unsetenv("XDG_CONFIG_HOME")
	_ = os.Unsetenv("CODEX_HOME")
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("GIT_CONFIG_GLOBAL", global)
	_ = os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	hideClaude(dir)
	return func() { _ = os.RemoveAll(dir) }
}

// hideClaude puts in place of each PATH directory holding a claude one under
// dir that links to everything else in it, so every other tool, git among
// them, is still found where it was. It panics if git is not.
func hideClaude(dir string) {
	git, _ := exec.LookPath("git")
	path := filepath.SplitList(os.Getenv("PATH"))
	for i, d := range path {
		if _, err := os.Stat(filepath.Join(d, "claude")); err != nil { //nolint:gosec // G703: only asks whether a PATH entry holds a claude
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			panic(err)
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			panic(err)
		}
		shadow := filepath.Join(dir, fmt.Sprintf("path%d", i))
		if err := os.Mkdir(shadow, 0o755); err != nil {
			panic(err)
		}
		for _, e := range entries {
			if e.Name() != "claude" {
				if err := os.Symlink(filepath.Join(abs, e.Name()), filepath.Join(shadow, e.Name())); err != nil {
					panic(err)
				}
			}
		}
		path[i] = shadow
	}
	_ = os.Setenv("PATH", strings.Join(path, string(os.PathListSeparator)))
	if now, _ := exec.LookPath("git"); realPath(now) != realPath(git) {
		panic(fmt.Sprintf("hiding claude moved git from %s to %s", git, now))
	}
}

// realPath is p with every symlink resolved, or p when that fails.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
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
