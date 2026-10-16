package provisiontest

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// Squashed is a worktree whose branch carries a commit trunk does not have,
// which is what a squash or rebase merge leaves behind as far as git is
// concerned. It returns the worktree, its branch and its tip.
func Squashed(t *testing.T, ctx *kit.Context, work string) (path, branch, tip string) {
	t.Helper()
	path = MergedWorktree(t, ctx, work)
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "the work that was squashed")
	return path, ctx.Repo.BranchAt(path), testkit.GitOut(t, path, "rev-parse", "HEAD")
}

// MergedInto is the same, for a pull request merged somewhere other than
// trunk — a stacked one, merged into its parent branch.
func MergedInto(number int, branch, tip, base string) github.PR {
	return github.PR{Number: number, Title: "Fix the login crash", HeadRefName: branch,
		HeadRefOid: tip, BaseRefName: base, State: "MERGED"}
}
