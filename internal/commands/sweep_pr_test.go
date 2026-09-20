package commands

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// squashed is a worktree whose branch carries a commit trunk does not have,
// which is what a squash or rebase merge leaves behind as far as git is
// concerned. It returns the worktree, its branch and its tip.
func squashed(t *testing.T, ctx *Context, work string) (path, branch, tip string) {
	t.Helper()
	path = mergedWorktree(t, ctx, work)
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "the work that was squashed")
	return path, ctx.Repo.BranchAt(path), gitOut(t, path, "rev-parse", "HEAD")
}

// mergedPR is what GitHub says about a branch it merged at tip.
func mergedPR(number int, branch, tip string) github.PR {
	return github.PR{Number: number, Title: "Fix the login crash", HeadRefName: branch,
		HeadRefOid: tip, State: "MERGED"}
}

// sweptWith is a sweep plan made against a fixed set of pull requests.
func sweptWith(t *testing.T, ctx *Context, prs map[string]github.PR) SweepPlan {
	t.Helper()
	return planWith(t, ctx, SweepOptions{Agents: []wtsync.Agent{}, PRs: prs})
}

// The case git cannot answer: the branch is not on trunk and never will be,
// so only the pull request says the work landed.
func TestSweepPlanSweepsASquashMergedWorktree(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path, branch, tip := squashed(t, ctx, "fix/login-crash")

	if p := sweptWith(t, ctx, nil); inAnyGroup(p, branch) {
		t.Fatal("without GitHub this branch is invisible to sweep, as it was")
	}

	p := sweptWith(t, ctx, map[string]github.PR{branch: mergedPR(34, branch, tip)})
	if !slices.Equal(worktreeNames(p.Remove), []string{branch}) {
		t.Fatalf("remove = %v, want the squash-merged worktree", worktreeNames(p.Remove))
	}
	if got := p.Remove[0]; got.MergedPR != 34 || got.Worktree != path || got.Plan.Outcome != BranchDeleted {
		t.Errorf("remove = %+v", got)
	}
	// The row has to say which fact it acted on, since git disagrees.
	want := "#34 merged on GitHub (squashed or rebased, so git cannot see it)"
	if out := rendered(p); !strings.Contains(out, want) {
		t.Errorf("the plan must say why:\nwant %q in\n%s", want, out)
	}
}

// End to end, with the branch deleted at the tip the plan showed.
func TestSweepRemovesASquashMergedWorktree(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path, branch, tip := squashed(t, ctx, "fix/login-crash")
	prs := map[string]github.PR{branch: mergedPR(34, branch, tip)}

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true, Agents: []wtsync.Agent{}, PRs: prs}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if exists(path) {
		t.Error("the worktree should have been removed")
	}
	if ctx.Repo.BranchExists(branch) {
		t.Error("the branch should have gone with its worktree")
	}
	want := "✓ worktree removed; branch " + branch + " was merged as #34 and has been deleted"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("want %q in\n%s", want, buf.String())
	}
}

// A commit made after the merge is work that went nowhere, and it takes the
// branch off the pull request's head.
func TestSweepPlanLeavesACommitMadeAfterTheMerge(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path, branch, tip := squashed(t, ctx, "fix/login-crash")
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "and then some more")

	p := sweptWith(t, ctx, map[string]github.PR{branch: mergedPR(34, branch, tip)})
	if inAnyGroup(p, branch) {
		t.Fatalf("a branch past the merged tip is not swept: %+v", p)
	}
}

// A closed pull request is not a merged one: nothing landed.
func TestSweepPlanIgnoresAClosedPullRequest(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	_, branch, tip := squashed(t, ctx, "fix/login-crash")
	closed := mergedPR(34, branch, tip)
	closed.State = "CLOSED"

	if p := sweptWith(t, ctx, map[string]github.PR{branch: closed}); inAnyGroup(p, branch) {
		t.Fatalf("a closed pull request swept the branch: %+v", p)
	}
}

// sweep's own safety checks are untouched by any of this.
func TestSweepPlanKeepsADirtySquashMergedWorktree(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path, branch, tip := squashed(t, ctx, "fix/login-crash")
	mustWrite(t, filepath.Join(path, "scratch.txt"), "not yet\n")

	p := sweptWith(t, ctx, map[string]github.PR{branch: mergedPR(34, branch, tip)})
	if len(p.Remove) != 0 {
		t.Fatalf("a dirty worktree must not be removed: %v", worktreeNames(p.Remove))
	}
	if !slices.Contains(branchNames(p.CheckedOut), branch) {
		t.Fatalf("want it kept: %+v", p)
	}
	if out := rendered(p); !strings.Contains(out, "dirty; commit or discard the changes") ||
		!strings.Contains(out, "#34 merged") {
		t.Errorf("the row must say both why it is kept and what GitHub says:\n%s", out)
	}
}

// Where git can tell, git tells: the pull request is printed beside the
// reason, not instead of it.
func TestSweepPlanNamesThePullRequestOfAMergedWorktree(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	mergedWorktree(t, ctx, "fix/login-crash")
	branch := "fix_wt/login-crash"

	p := sweptWith(t, ctx, map[string]github.PR{branch: openPR(35, branch, "Still open")})
	if !slices.Equal(worktreeNames(p.Remove), []string{branch}) {
		t.Fatalf("remove = %v", worktreeNames(p.Remove))
	}
	if got := p.Remove[0].MergedPR; got != 0 {
		t.Errorf("MergedPR = %d; trunk containing the branch is the reason here", got)
	}
	out := rendered(p)
	if !strings.Contains(out, "merged into origin/main · #35 open") {
		t.Errorf("want the pull request beside the reason:\n%s", out)
	}
}

// A branch nothing has checked out, merged only on GitHub, is the same litter
// as a merged one.
func TestSweepPlanDeletesASquashMergedBranchWithNoWorktree(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "left-behind", 1)
	tip := gitOut(t, main, "rev-parse", "left-behind")

	p := sweptWith(t, ctx, map[string]github.PR{"left-behind": mergedPR(34, "left-behind", tip)})
	if !slices.Contains(branchNames(p.Delete), "left-behind") {
		t.Fatalf("delete = %v", branchNames(p.Delete))
	}
	if out := rendered(p); !strings.Contains(out, "#34 merged on GitHub") {
		t.Errorf("the row must say why:\n%s", out)
	}
}

// An upstream deleted on merge is what a squash merge usually leaves; the row
// stops being a puzzle and says what happened.
func TestSweepPlanNamesThePullRequestOfAGoneBranch(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "went-nowhere", 1)
	goneUpstream(t, main, "went-nowhere")
	closed := mergedPR(34, "went-nowhere", "some-other-commit")
	closed.State = "CLOSED"

	p := sweptWith(t, ctx, map[string]github.PR{"went-nowhere": closed})
	if !slices.Contains(branchNames(p.Gone), "went-nowhere") {
		t.Fatalf("gone = %v", branchNames(p.Gone))
	}
	if out := rendered(p); !strings.Contains(out, "#34 closed") {
		t.Errorf("want the pull request on the row:\n%s", out)
	}
}
