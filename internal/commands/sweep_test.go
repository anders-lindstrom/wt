package commands

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sweepRepoWith is a main checkout on main with a bare origin beside it,
// fetched, origin/HEAD pointing at main, opened after all of that.
func sweepRepoWith(t *testing.T, conf string) (ctx *Context, main, origin string) {
	t.Helper()
	main = committedRepo(t, conf)
	origin = filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, main, "clone", "-q", "--bare", main, origin)
	gitIn(t, main, "remote", "add", "origin", origin)
	gitIn(t, main, "fetch", "-q", "origin")
	gitIn(t, main, "remote", "set-head", "origin", "main")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, main, origin
}

func sweepRepo(t *testing.T) (ctx *Context, main, origin string) {
	t.Helper()
	return sweepRepoWith(t, minimalConf)
}

// addWork puts n empty commits on branch through update-ref, so no checkout
// moves. Messages are unique: two empty commits with the same parent, message
// and second would otherwise be the same commit.
func addWork(t *testing.T, main, branch string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		tree := gitOut(t, main, "rev-parse", branch+"^{tree}")
		c := gitOut(t, main, "commit-tree", "-p", branch, "-m", fmt.Sprintf("%s work %d", branch, i), tree)
		gitIn(t, main, "update-ref", "refs/heads/"+branch, c)
	}
}

// branchWithWork cuts branch from main and gives it n commits of its own.
func branchWithWork(t *testing.T, main, branch string, n int) {
	t.Helper()
	gitIn(t, main, "branch", branch, "main")
	addWork(t, main, branch, n)
}

// landOnMain merges branch into main and pushes, which is what merging its
// pull request amounts to.
func landOnMain(t *testing.T, main, branch string) {
	t.Helper()
	gitIn(t, main, "merge", "-q", "--no-ff", "-m", "merge "+branch, branch)
	gitIn(t, main, "push", "-q", "origin", "main")
	gitIn(t, main, "fetch", "-q", "origin")
}

// goneUpstream gives branch an upstream origin does not have, which is what a
// remote branch deleted on merge looks like after fetch --prune.
func goneUpstream(t *testing.T, main, branch string) {
	t.Helper()
	gitIn(t, main, "config", "branch."+branch+".remote", "origin")
	gitIn(t, main, "config", "branch."+branch+".merge", "refs/heads/"+branch)
}

func planOf(t *testing.T, ctx *Context) SweepPlan {
	t.Helper()
	bases, err := sweepBases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planSweep(ctx, bases)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func branchNames(bs []SweepBranch) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.Name)
	}
	return out
}

func rendered(p SweepPlan) string {
	var buf bytes.Buffer
	p.Render(&buf)
	return buf.String()
}

func inAnyGroup(p SweepPlan, name string) bool {
	return slices.Contains(branchNames(p.Delete), name) ||
		slices.Contains(branchNames(p.CheckedOut), name) ||
		slices.Contains(branchNames(p.Gone), name)
}

func TestSweepPlanDeletesAMergedBranch(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")

	p := planOf(t, ctx)
	if !slices.Equal(branchNames(p.Delete), []string{"done-work"}) {
		t.Fatalf("delete = %v", branchNames(p.Delete))
	}
	if p.Delete[0].MergedInto != "origin/main" {
		t.Errorf("merged into %q, want origin/main first", p.Delete[0].MergedInto)
	}
	if out := rendered(p); !strings.Contains(out, "done-work") || !strings.Contains(out, "merged into origin/main") {
		t.Errorf("the plan must name the branch and why it goes:\n%s", out)
	}
}

func TestSweepPlanLeavesUnmergedWorkOut(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "wip", 2)

	if p := planOf(t, ctx); inAnyGroup(p, "wip") {
		t.Fatalf("unmerged work with no gone upstream belongs in no group: %+v", p)
	}
}

// Merges happen on the remote; the main checkout's trunk is often behind it.
func TestSweepPlanCountsAMergeOnlyOriginHas(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	branchWithWork(t, main, "landed", 1)
	// To the path: a push by remote name would move origin/main without a fetch.
	gitIn(t, main, "push", "-q", origin, "landed:main")
	gitIn(t, main, "fetch", "-q", "origin")

	p := planOf(t, ctx)
	if !slices.Contains(branchNames(p.Delete), "landed") {
		t.Fatalf("landed is in origin/main; want it deleted: %v", branchNames(p.Delete))
	}
}

// Some repositories commit straight to trunk, and origin can be behind.
func TestSweepPlanCountsAMergeOnlyLocalTrunkHas(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "local-only", 1)
	gitIn(t, main, "merge", "-q", "--no-ff", "-m", "merge local-only", "local-only")

	p := planOf(t, ctx)
	if !slices.Equal(branchNames(p.Delete), []string{"local-only"}) || p.Delete[0].MergedInto != "main" {
		t.Fatalf("delete = %+v", p.Delete)
	}
}

func TestSweepPlanSendsACheckedOutBranchToRemove(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	var buf bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}

	p := planOf(t, ctx)
	if slices.Contains(branchNames(p.Delete), "fix_wt/login-crash") {
		t.Fatal("a branch a worktree has checked out must not be deleted")
	}
	if !slices.Contains(branchNames(p.CheckedOut), "fix_wt/login-crash") {
		t.Fatalf("want it in the checked-out group: %v", branchNames(p.CheckedOut))
	}
	if out := rendered(p); !strings.Contains(out, "wt remove fix_wt/login-crash") {
		t.Errorf("the plan must say how to finish it:\n%s", out)
	}
}

// A rebase detaches HEAD, so for-each-ref's worktreepath is empty for the
// branch being rebased. The worktree list's head-name is what still names it.
func TestSweepPlanSeesABranchInTheMiddleOfARebase(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	dst := foreignWorktree(t, ctx, main, "rebasing", 0)
	mustWrite(t, filepath.Join(dst, "f.txt"), "branch\n")
	gitIn(t, dst, "add", "f.txt")
	gitIn(t, dst, "commit", "-q", "-m", "branch side")
	landOnMain(t, main, "rebasing")

	other := filepath.Join(ctx.Repo.Parent, "other")
	gitIn(t, main, "worktree", "add", "-q", "-b", "other", other, "main^1")
	mustWrite(t, filepath.Join(other, "f.txt"), "other\n")
	gitIn(t, other, "add", "f.txt")
	gitIn(t, other, "commit", "-q", "-m", "other side")

	rebase := exec.Command("git", "rebase", "other")
	rebase.Dir = dst
	_ = rebase.Run() // stops at the conflict on f.txt, which is the state under test
	if wp := gitOut(t, main, "for-each-ref", "--format=%(worktreepath)", "refs/heads/rebasing"); wp != "" {
		t.Fatalf("precondition: git reports rebasing as checked out at %q; the trap is gone", wp)
	}

	p := planOf(t, ctx)
	if slices.Contains(branchNames(p.Delete), "rebasing") {
		t.Fatal("a branch mid-rebase was offered for deletion")
	}
	if !slices.Contains(branchNames(p.CheckedOut), "rebasing") {
		t.Errorf("want rebasing in the checked-out group: %v", branchNames(p.CheckedOut))
	}
}

// A bisect detaches HEAD; git still counts the branch it started from as in
// use, and only BISECT_START names it.
func TestSweepPlanSeesABranchABisectStartedFrom(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	addWork(t, main, "main", 4)
	dst := foreignWorktree(t, ctx, main, "bisecting", 0)
	gitIn(t, dst, "bisect", "start")
	gitIn(t, dst, "bisect", "bad")
	gitIn(t, dst, "bisect", "good", "HEAD~4")
	if err := exec.Command("git", "-C", dst, "symbolic-ref", "-q", "HEAD").Run(); err == nil {
		t.Fatal("precondition: the bisect left HEAD on the branch; the trap is not set")
	}

	p := planOf(t, ctx)
	if slices.Contains(branchNames(p.Delete), "bisecting") {
		t.Fatal("a branch a bisect started from was offered for deletion")
	}
	if !slices.Contains(branchNames(p.CheckedOut), "bisecting") {
		t.Errorf("want bisecting in the checked-out group: %v", branchNames(p.CheckedOut))
	}
}

// A stopped rebase --update-refs holds every branch it will move, not only
// the one being rebased; git lists them in rebase-merge/update-refs.
func TestSweepPlanSeesABranchARebaseWillUpdate(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	dst := foreignWorktree(t, ctx, main, "upper", 0)
	mustWrite(t, filepath.Join(dst, "f.txt"), "lower\n")
	gitIn(t, dst, "add", "f.txt")
	gitIn(t, dst, "commit", "-q", "-m", "lower side")
	gitIn(t, main, "branch", "lower", "upper")
	mustWrite(t, filepath.Join(dst, "f.txt"), "upper\n")
	gitIn(t, dst, "commit", "-q", "-am", "upper side")
	landOnMain(t, main, "lower")

	other := filepath.Join(ctx.Repo.Parent, "other")
	gitIn(t, main, "worktree", "add", "-q", "-b", "other", other, "main^1")
	mustWrite(t, filepath.Join(other, "f.txt"), "other\n")
	gitIn(t, other, "add", "f.txt")
	gitIn(t, other, "commit", "-q", "-m", "other side")

	rebase := exec.Command("git", "rebase", "--update-refs", "other")
	rebase.Dir = dst
	_ = rebase.Run() // stops at the conflict on f.txt, which is the state under test
	updateRefs := filepath.Join(gitOut(t, dst, "rev-parse", "--absolute-git-dir"), "rebase-merge", "update-refs")
	if err := exec.Command("grep", "-qxF", "refs/heads/lower", updateRefs).Run(); err != nil {
		t.Fatalf("precondition: the stopped rebase does not list lower in %s: %v", updateRefs, err)
	}

	p := planOf(t, ctx)
	if slices.Contains(branchNames(p.Delete), "lower") {
		t.Fatal("a branch a stopped rebase will update was offered for deletion")
	}
	if !slices.Contains(branchNames(p.CheckedOut), "lower") {
		t.Errorf("want lower in the checked-out group: %v", branchNames(p.CheckedOut))
	}
}

func TestSweepPlanNamesTheMainCheckoutWhenItIsOnAMergedBranch(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	gitIn(t, main, "switch", "-q", "done-work")

	p := planOf(t, ctx)
	if !slices.Contains(branchNames(p.CheckedOut), "done-work") {
		t.Fatalf("want done-work in the checked-out group: %+v", p)
	}
	if out := rendered(p); !strings.Contains(out, "main checkout") {
		t.Errorf("wt remove cannot remove the main checkout; say what to do instead:\n%s", out)
	}
}

func TestSweepPlanNeverOffersProtectedBranches(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	for _, name := range []string{"master", "develop", "development", "staging", "production", "release-2.1"} {
		gitIn(t, main, "branch", name)
	}

	p := planOf(t, ctx)
	if len(p.Delete)+len(p.CheckedOut)+len(p.Gone) != 0 {
		t.Fatalf("protected branches must not appear anywhere: %+v", p)
	}
}

// The configured trunk and origin's HEAD can disagree; both are trunks.
func TestSweepPlanProtectsTheConfiguredTrunkAndOriginHead(t *testing.T) {
	main := committedRepo(t, "MAIN_BRANCH=\"trunk\"\nBUILD_INIT_ENABLED=false\n")
	gitIn(t, main, "branch", "trunk")
	gitIn(t, main, "branch", "integration")
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, main, "clone", "-q", "--bare", main, origin)
	gitIn(t, main, "remote", "add", "origin", origin)
	gitIn(t, main, "fetch", "-q", "origin")
	gitIn(t, main, "remote", "set-head", "origin", "integration")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}

	p := planOf(t, ctx)
	for _, name := range []string{"trunk", "integration"} {
		if inAnyGroup(p, name) {
			t.Errorf("%s is a trunk and must never be offered: %+v", name, p)
		}
	}
}

// A HEAD whose target was pruned still names trunk.
func TestSweepPlanProtectsTheBranchADanglingOriginHeadNames(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	gitIn(t, main, "branch", "integration")
	gitIn(t, main, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/integration")

	if p := planOf(t, ctx); inAnyGroup(p, "integration") {
		t.Fatalf("integration is origin's HEAD, dangling or not: %+v", p)
	}
}

func TestSweepPlanReportsAGoneBranchThatStillHasWork(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "old-idea", 3)
	goneUpstream(t, main, "old-idea")
	branchWithWork(t, main, "gone-merged", 0)
	goneUpstream(t, main, "gone-merged")

	p := planOf(t, ctx)
	if !slices.Equal(branchNames(p.Gone), []string{"old-idea"}) || p.Gone[0].Ahead != 3 {
		t.Fatalf("gone = %+v", p.Gone)
	}
	if !slices.Contains(branchNames(p.Delete), "gone-merged") {
		t.Errorf("a gone upstream on a merged branch is still merged: %v", branchNames(p.Delete))
	}
	if out := rendered(p); !strings.Contains(out, "3 commits ahead of origin/main") {
		t.Errorf("the plan must say what is at stake:\n%s", out)
	}
}

func TestSweepPlanWithNoOriginComparesWithLocalTrunk(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	branchWithWork(t, main, "done-work", 0)

	p := planOf(t, ctx)
	if len(p.Bases) != 1 || p.Bases[0].Name != "main" {
		t.Fatalf("bases = %+v", p.Bases)
	}
	if !slices.Equal(branchNames(p.Delete), []string{"done-work"}) || p.Delete[0].MergedInto != "main" {
		t.Errorf("delete = %+v", p.Delete)
	}
}

func TestSweepPlanWithNothingMergedSaysSo(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	if out := rendered(planOf(t, ctx)); !strings.Contains(out, "No merged branches to delete.") {
		t.Errorf("an empty plan must say so:\n%s", out)
	}
}
