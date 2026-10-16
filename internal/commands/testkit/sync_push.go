package testkit

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// PushOrigin gives a fixture somewhere to push: a bare repository as
// origin's push URL. Fetches still read the main checkout, which a push could
// not update while its branches are checked out in worktrees. The bare
// repository starts out holding what the remote-tracking refs say origin
// has, so the lease sees an origin that matches the last fetch.
func PushOrigin(t *testing.T, ctx *kit.Context) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	GitOut(t, ctx.Repo.MainRoot, "init", "-q", "--bare", bare)
	GitOut(t, ctx.Repo.MainRoot, "push", "-q", bare, "refs/remotes/origin/*:refs/heads/*")
	GitOut(t, ctx.Repo.MainRoot, "remote", "set-url", "--push", "origin", bare)
	return bare
}

// Pushed reports whether origin holds the branch as the worktree has it now.
func Pushed(t *testing.T, bare, wtPath string) bool {
	t.Helper()
	return GitOut(t, bare, "rev-parse", "feat_wt/bump") == GitOut(t, wtPath, "rev-parse", "HEAD")
}
