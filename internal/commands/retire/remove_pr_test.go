package retire

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// `wt remove` runs from git hooks, so it may start no process and touch no
// network — but a merge is permanent, so the cache can answer it. A branch
// whose pull request merged into trunk at exactly this tip goes the way sweep
// would take it, however old the entry is.
func TestRemoveDeletesASquashMergedBranchFromTheCacheAlone(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, path, branch, _ := provisiontest.MergedInCache(t, "main")

	var out bytes.Buffer
	if err := Remove(ctx, path, RemoveOptions{}, &out); err != nil {
		t.Fatalf("Remove: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "branch "+branch+" was merged as #34 and has been deleted") {
		t.Errorf("wt remove said\n%s", out.String())
	}
	if _, ok := ctx.Repo.ResolveRef("refs/heads/" + branch); ok {
		t.Error("the branch is still here")
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s was left behind", path)
	}
}

// The plan says which fact it is acting on, because git's answer is the
// opposite one.
func TestRemovePlanNamesThePullRequestItBelieves(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, path, _, _ := provisiontest.MergedInCache(t, "main")
	p := planFor(ctx, worktreeRecord(ctx, path), RemoveOptions{})
	if p.Outcome != BranchDeleted || p.MergedPR != 34 {
		t.Fatalf("plan = %+v", p)
	}
	var out bytes.Buffer
	p.Render(&out)
	if !strings.Contains(out.String(), "#34 merged on GitHub") {
		t.Errorf("the plan said\n%s", out.String())
	}
}

// Merged into its parent branch is not merged onto trunk: the work is still
// only on the parent, and the branch keeps its commits.
func TestRemoveKeepsABranchMergedIntoItsParent(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, path, branch, _ := provisiontest.MergedInCache(t, "feat_wt/the-parent")

	var out bytes.Buffer
	if err := Remove(ctx, path, RemoveOptions{}, &out); err != nil {
		t.Fatalf("Remove: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "#34") {
		t.Errorf("wt remove acted on the pull request:\n%s", out.String())
	}
	// Kept the way any unmerged branch is: renamed out of the type prefix,
	// with its commits intact.
	if !strings.Contains(out.String(), "branch kept as login-crash") {
		t.Errorf("wt remove said\n%s", out.String())
	}
	if _, ok := ctx.Repo.ResolveRef("refs/heads/login-crash"); !ok {
		t.Errorf("%s went although nothing reached trunk", branch)
	}
}

// With nothing in the cache, `wt remove` is exactly what it was before any of
// this existed: the branch is renamed aside, not deleted.
func TestRemoveWithNoCacheEntryIsUnchanged(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, _, _ := testkit.SweepRepo(t)
	path, branch, _ := provisiontest.Squashed(t, ctx, "fix/login-crash")
	testkit.StubGitHub(t, github.CLI{}, testkit.GhRepo, true)

	var out bytes.Buffer
	if err := Remove(ctx, path, RemoveOptions{}, &out); err != nil {
		t.Fatalf("Remove: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "branch kept as login-crash") {
		t.Errorf("wt remove said\n%s", out.String())
	}
	if _, ok := ctx.Repo.ResolveRef("refs/heads/" + branch); ok {
		t.Error("the branch was deleted with nothing saying it had landed")
	}
}

// The person's own switch governs this as it governs everything else.
func TestRemoveIgnoresTheCacheWithGitHubOff(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, path, branch, _ := provisiontest.MergedInCache(t, "main")
	ctx.User.GitHub = false

	var out bytes.Buffer
	if err := Remove(ctx, path, RemoveOptions{}, &out); err != nil {
		t.Fatalf("Remove: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "branch kept as login-crash") {
		t.Errorf("%s went although GitHub is switched off:\n%s", branch, out.String())
	}
}
