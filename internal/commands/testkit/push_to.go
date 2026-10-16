package testkit

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// OwnBareOrigin makes origin a bare repository of its own that holds trunk and
// nothing else: the fixture's branches are unpushed, and every push, fetch
// and lease goes to a real remote.
func OwnBareOrigin(t *testing.T, ctx *kit.Context) string {
	t.Helper()
	main := ctx.Repo.MainRoot
	bare := filepath.Join(t.TempDir(), "origin.git")
	GitOut(t, main, "init", "-q", "--bare", bare)
	GitOut(t, main, "push", "-q", bare, "refs/remotes/origin/main:refs/heads/main")
	GitOut(t, main, "remote", "set-url", "origin", bare)
	for _, ref := range strings.Fields(GitOut(t, main, "for-each-ref", "--format=%(refname)", "refs/remotes/origin")) {
		if ref != "refs/remotes/origin/main" {
			GitOut(t, main, "update-ref", "--no-deref", "-d", ref)
		}
	}
	return bare
}

// OnBare is the commit the bare remote has branch at, "" when it has none.
func OnBare(t *testing.T, bare, branch string) string {
	t.Helper()
	out, err := gittest.Try(t, bare, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// MoveOnRemote puts a commit on the remote's branch from another clone:
// somebody else's push.
func MoveOnRemote(t *testing.T, bare, branch string) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	GitOut(t, bare, "clone", "-q", "--branch", branch, bare, other)
	GitOut(t, other, "config", "user.email", "o@example.com")
	GitOut(t, other, "config", "user.name", "O")
	WriteFile(t, other, "theirs.txt", "theirs\n")
	GitOut(t, other, "add", "-A")
	GitOut(t, other, "commit", "-q", "-m", "theirs")
	GitOut(t, other, "push", "-q", "origin", branch)
	return GitOut(t, other, "rev-parse", "HEAD")
}
