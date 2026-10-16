package provisiontest

import (
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// MergedInCache is a worktree whose branch git reads as unmerged — a squash
// merge — with the cache holding GitHub's answer that it landed on trunk.
func MergedInCache(t *testing.T, base string) (ctx *kit.Context, path, branch string, pr github.PR) {
	t.Helper()
	ctx, _, _ = testkit.SweepRepo(t)
	path, branch, tip := Squashed(t, ctx, "fix/login-crash")
	pr = MergedInto(34, branch, tip, base)
	// No gh anywhere: this must work off the file alone.
	testkit.StubGitHub(t, github.CLI{}, testkit.GhRepo, true)
	kit.WritePRCache(ctx, testkit.GhRepo, map[string]*github.PR{branch: &pr}, time.Now().Add(-30*kit.PRCacheTTL))
	return ctx, path, branch, pr
}
