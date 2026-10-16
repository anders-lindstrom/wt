package retire

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// MergedPR is what GitHub says about a branch it merged into trunk at tip.
func MergedPR(number int, branch, tip string) github.PR {
	return provisiontest.MergedInto(number, branch, tip, "main")
}

// sweptWith is a sweep plan made against a fixed set of pull requests.
func sweptWith(t *testing.T, ctx *kit.Context, prs map[string]github.PR) SweepPlan {
	t.Helper()
	return planWith(t, ctx, SweepOptions{Agents: []wtsync.Agent{}, PRs: prs})
}

// The case git cannot answer: the branch is not on trunk and never will be,
// so only the pull request says the work landed.
func TestSweepPlanSweepsASquashMergedWorktree(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	path, branch, tip := provisiontest.Squashed(t, ctx, "fix/login-crash")

	if p := sweptWith(t, ctx, nil); inAnyGroup(p, branch) {
		t.Fatal("without GitHub this branch is invisible to sweep, as it was")
	}

	p := sweptWith(t, ctx, map[string]github.PR{branch: MergedPR(34, branch, tip)})
	if !slices.Equal(WorktreeNames(p.Remove), []string{branch}) {
		t.Fatalf("remove = %v, want the squash-merged worktree", WorktreeNames(p.Remove))
	}
	if got := p.Remove[0]; got.MergedPR != 34 || got.Worktree != path || got.Plan.Outcome != BranchDeleted {
		t.Errorf("remove = %+v", got)
	}
	// The row has to say which fact it acted on, since git disagrees.
	want := "#34 merged on GitHub (squashed or rebased, so git cannot see it)"
	if out := Rendered(p); !strings.Contains(out, want) {
		t.Errorf("the plan must say why:\nwant %q in\n%s", want, out)
	}
}

// End to end, with the branch deleted at the tip the plan showed.
func TestSweepRemovesASquashMergedWorktree(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	path, branch, tip := provisiontest.Squashed(t, ctx, "fix/login-crash")
	prs := map[string]github.PR{branch: MergedPR(34, branch, tip)}

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true, Agents: []wtsync.Agent{}, PRs: prs}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if testkit.Exists(path) {
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
	ctx, _, _ := testkit.SweepRepo(t)
	path, branch, tip := provisiontest.Squashed(t, ctx, "fix/login-crash")
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "and then some more")

	p := sweptWith(t, ctx, map[string]github.PR{branch: MergedPR(34, branch, tip)})
	if inAnyGroup(p, branch) {
		t.Fatalf("a branch past the merged tip is not swept: %+v", p)
	}
}

// A closed pull request is not a merged one: nothing landed.
func TestSweepPlanIgnoresAClosedPullRequest(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	_, branch, tip := provisiontest.Squashed(t, ctx, "fix/login-crash")
	closed := MergedPR(34, branch, tip)
	closed.State = "CLOSED"

	if p := sweptWith(t, ctx, map[string]github.PR{branch: closed}); inAnyGroup(p, branch) {
		t.Fatalf("a closed pull request swept the branch: %+v", p)
	}
}

// sweep's own safety checks are untouched by any of this.
func TestSweepPlanKeepsADirtySquashMergedWorktree(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	path, branch, tip := provisiontest.Squashed(t, ctx, "fix/login-crash")
	testkit.MustWrite(t, filepath.Join(path, "scratch.txt"), "not yet\n")

	p := sweptWith(t, ctx, map[string]github.PR{branch: MergedPR(34, branch, tip)})
	if len(p.Remove) != 0 {
		t.Fatalf("a dirty worktree must not be removed: %v", WorktreeNames(p.Remove))
	}
	if !slices.Contains(branchNames(p.CheckedOut), branch) {
		t.Fatalf("want it kept: %+v", p)
	}
	if out := Rendered(p); !strings.Contains(out, "dirty; commit or discard the changes") ||
		!strings.Contains(out, "#34 merged") {
		t.Errorf("the row must say both why it is kept and what GitHub says:\n%s", out)
	}
}

// Where git can tell, git tells: the pull request is printed beside the
// reason, not instead of it.
func TestSweepPlanNamesThePullRequestOfAMergedWorktree(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	provisiontest.MergedWorktree(t, ctx, "fix/login-crash")
	branch := "fix_wt/login-crash"

	p := sweptWith(t, ctx, map[string]github.PR{branch: testkit.OpenPR(35, branch, "Still open")})
	if !slices.Equal(WorktreeNames(p.Remove), []string{branch}) {
		t.Fatalf("remove = %v", WorktreeNames(p.Remove))
	}
	if got := p.Remove[0].MergedPR; got != 0 {
		t.Errorf("MergedPR = %d; trunk containing the branch is the reason here", got)
	}
	out := Rendered(p)
	if !strings.Contains(out, "merged into origin/main · #35 open") {
		t.Errorf("want the pull request beside the reason:\n%s", out)
	}
}

// A branch nothing has checked out, merged only on GitHub, is the same litter
// as a merged one.
func TestSweepPlanDeletesASquashMergedBranchWithNoWorktree(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "left-behind", 1)
	tip := testkit.GitOut(t, main, "rev-parse", "left-behind")

	p := sweptWith(t, ctx, map[string]github.PR{"left-behind": MergedPR(34, "left-behind", tip)})
	if !slices.Contains(branchNames(p.Delete), "left-behind") {
		t.Fatalf("delete = %v", branchNames(p.Delete))
	}
	if out := Rendered(p); !strings.Contains(out, "#34 merged on GitHub") {
		t.Errorf("the row must say why:\n%s", out)
	}
}

// An upstream deleted on merge is what a squash merge usually leaves; the row
// stops being a puzzle and says what happened.
func TestSweepPlanNamesThePullRequestOfAGoneBranch(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "went-nowhere", 1)
	GoneUpstream(t, main, "went-nowhere")
	closed := MergedPR(34, "went-nowhere", "some-other-commit")
	closed.State = "CLOSED"

	p := sweptWith(t, ctx, map[string]github.PR{"went-nowhere": closed})
	if !slices.Contains(branchNames(p.Gone), "went-nowhere") {
		t.Fatalf("gone = %v", branchNames(p.Gone))
	}
	if out := Rendered(p); !strings.Contains(out, "#34 closed") {
		t.Errorf("want the pull request on the row:\n%s", out)
	}
}

// A stacked pull request merged into its parent branch has landed nothing on
// trunk: the parent still holds every commit, and deleting this branch under
// a header that says "merged means reachable from origin/main" would throw
// the work away.
func TestSweepLeavesAPullRequestMergedIntoItsParent(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	_, branch, tip := provisiontest.Squashed(t, ctx, "fix/login-crash")

	stacked := provisiontest.MergedInto(34, branch, tip, "feat_wt/the-parent")
	p := sweptWith(t, ctx, map[string]github.PR{branch: stacked})
	if inAnyGroup(p, branch) {
		t.Fatalf("a branch merged into its parent was swept:\n%s", Rendered(p))
	}
	// It is still named, and named honestly, wherever the branch does show.
	if got := kit.PRLabel(stacked, []string{"main"}); got != "#34 merged into feat_wt/the-parent" {
		t.Errorf("prLabel = %q", got)
	}
}

// An answer written before wt read the base branch says nothing about where
// the work went, and an unanswered question is not a yes.
func TestSweepLeavesAPullRequestWithNoBaseRecorded(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	_, branch, tip := provisiontest.Squashed(t, ctx, "fix/login-crash")

	p := sweptWith(t, ctx, map[string]github.PR{branch: provisiontest.MergedInto(34, branch, tip, "")})
	if inAnyGroup(p, branch) {
		t.Fatalf("a pull request with no base recorded was swept:\n%s", Rendered(p))
	}
}

// The second read is a guard, not a decision. When it cannot confirm that the
// pull request still reads as landed at this tip — usually because GitHub
// could not be asked again — the row says that, and never that GitHub said
// something new.
func TestSweepSaysWhyAConfirmedPullRequestWasNotDeleted(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	_, branch, tip := provisiontest.Squashed(t, ctx, "fix/login-crash")
	was := SweepBranch{Branch: repo.Branch{Name: branch, Tip: tip}, MergedPR: 34}

	// A fresh plan that knows nothing about the pull request: the branch is
	// where it was, and nothing else claims it.
	fresh := planWith(t, ctx, SweepOptions{Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}})
	got := fresh.changedFrom(ctx, was)
	if got != "#34 could not be read as merged at this tip a second time" {
		t.Errorf("changedFrom = %q", got)
	}
}

// ghAnsweringOnce is a fake gh that answers the branch lookup the first time
// and fails every time after, which is what sweep's second read meets when
// GitHub goes away between the plan and the deletion.
func ghAnsweringOnce(t *testing.T, prs ...github.PR) {
	t.Helper()
	counter := filepath.Join(t.TempDir(), "calls")
	testkit.FakeGitHubWith(t, `if [ "$1 $2" = 'api graphql' ]; then
  n=$(cat `+counter+` 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > `+counter+`
  [ "$n" -gt 1 ] && { echo 'offline' >&2; exit 1; }
fi`, prs...)
}

// The apply-time read is a guard, not a decision: a GitHub that has gone away
// since the plan keeps the branch and says so, and never claims GitHub said
// something new.
func TestSweepKeepsASquashMergedBranchWhenTheSecondReadFails(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "fix_wt/login-crash", 1)
	tip := testkit.GitOut(t, main, "rev-parse", "fix_wt/login-crash")
	GoneUpstream(t, main, "fix_wt/login-crash")
	ghAnsweringOnce(t, MergedPR(34, "fix_wt/login-crash", tip))

	var out bytes.Buffer
	err := Sweep(ctx, SweepOptions{NoFetch: true, Yes: true, Agents: []wtsync.Agent{}}, &out)
	if err == nil {
		t.Fatalf("the sweep reported success:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "#34 could not be read as merged at this tip a second time") {
		t.Errorf("sweep said\n%s", out.String())
	}
	if _, ok := ctx.Repo.ResolveRef("refs/heads/fix_wt/login-crash"); !ok {
		t.Error("the branch was deleted although the second read failed")
	}
}

// Sweep decides what to delete, so it asks GitHub afresh: a young cached
// answer must not stand in for one.
func TestSweepAsksGitHubPastAYoungCache(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "fix_wt/login-crash", 1)
	tip := testkit.GitOut(t, main, "rev-parse", "fix_wt/login-crash")
	GoneUpstream(t, main, "fix_wt/login-crash")
	// The cache says there is no pull request, written just now.
	kit.WritePRCache(ctx, testkit.GhRepo, map[string]*github.PR{"fix_wt/login-crash": nil}, time.Now())
	testkit.FakeGitHub(t, MergedPR(34, "fix_wt/login-crash", tip))

	p := planWith(t, ctx, SweepOptions{Agents: []wtsync.Agent{}})
	if !slices.Contains(branchNames(p.Delete), "fix_wt/login-crash") {
		t.Errorf("sweep believed the cache instead of asking GitHub: %s", Rendered(p))
	}
}

// A squash-merged branch nothing has checked out goes the same way, end to
// end and at the tip the plan showed.
func TestSweepDeletesASquashMergedBranchWithNoWorktree(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "fix_wt/login-crash", 1)
	tip := testkit.GitOut(t, main, "rev-parse", "fix_wt/login-crash")
	GoneUpstream(t, main, "fix_wt/login-crash")
	testkit.FakeGitHub(t, MergedPR(34, "fix_wt/login-crash", tip))

	var out bytes.Buffer
	if err := Sweep(ctx, SweepOptions{NoFetch: true, Yes: true, Agents: []wtsync.Agent{}}, &out); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, out.String())
	}
	if _, ok := ctx.Repo.ResolveRef("refs/heads/fix_wt/login-crash"); ok {
		t.Errorf("the branch is still here:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "✓ deleted fix_wt/login-crash") {
		t.Errorf("sweep said\n%s", out.String())
	}
}

// The answers that look like a merge from a distance and are not. Each one
// leaves the branch exactly where it was, through the real lookup.
func TestSweepDoesNotDeleteOnAPullRequestThatDidNotLandHere(t *testing.T) {
	forkOf := func(pr github.PR) github.PR {
		pr.IsCrossRepository = true
		pr.HeadRepositoryOwner = github.Account{Login: "a-stranger"}
		return pr
	}
	for name, prs := range map[string]func(branch, tip string) []github.PR{
		// A bare branch name here is this repository's branch. A stranger's
		// fork calling its own branch the same thing, and having it merged,
		// says nothing about the commits sitting on this one.
		"a fork's merge beside this repository's own open pull request": func(branch, tip string) []github.PR {
			own := testkit.OpenPR(35, branch, "Still going")
			own.HeadRefOid, own.BaseRefName = tip, "main"
			return []github.PR{forkOf(MergedPR(34, branch, tip)), own}
		},
		// Open at this very commit is work in progress, not work that landed.
		"an open pull request at the tip": func(branch, tip string) []github.PR {
			p := testkit.OpenPR(34, branch, "Still going")
			p.HeadRefOid, p.BaseRefName = tip, "main"
			return []github.PR{p}
		},
		// The branch name was reused after an earlier pull request merged;
		// that merge says nothing about the commits on it now.
		"an older merge of the same branch name": func(branch, _ string) []github.PR {
			return []github.PR{MergedPR(20, branch, "0000000000000000000000000000000000000000")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, main, _ := testkit.SweepRepo(t)
			testkit.BranchWithWork(t, main, "fix_wt/login-crash", 1)
			tip := testkit.GitOut(t, main, "rev-parse", "fix_wt/login-crash")
			// Gone upstream is what brings a branch into the question at all;
			// without it sweep never asks about this one and the case is
			// about nothing.
			GoneUpstream(t, main, "fix_wt/login-crash")
			log := testkit.FakeGitHub(t, prs("fix_wt/login-crash", tip)...)

			var out bytes.Buffer
			if err := Sweep(ctx, SweepOptions{NoFetch: true, Yes: true, Agents: []wtsync.Agent{}}, &out); err != nil {
				t.Fatalf("Sweep: %v\n%s", err, out.String())
			}
			if len(testkit.ArgvOf(t, log)) == 0 {
				t.Fatal("GitHub was never asked about the branch")
			}
			if _, ok := ctx.Repo.ResolveRef("refs/heads/fix_wt/login-crash"); !ok {
				t.Errorf("the branch was deleted:\n%s", out.String())
			}
		})
	}
}

// A gh that could not be asked costs the squash merges and nothing else: the
// line says what sweep is going on without, in sweep's own terms rather than
// `wt list`'s.
func TestSweepSaysWhatItLosesWithoutGitHub(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "fix_wt/login-crash", 1)
	GoneUpstream(t, main, "fix_wt/login-crash")
	testkit.FakeGitHubWith(t, `[ "$1 $2" = 'api graphql' ] && { echo offline >&2; exit 1; }`)

	var errs bytes.Buffer
	ctx.WarnTo(&errs)
	var out bytes.Buffer
	if err := Sweep(ctx, SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}}, &out); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	want := "wt: sweeping without GitHub's answers, so a squash-merged branch is not recognised"
	if !strings.Contains(errs.String(), want) {
		t.Errorf("stderr = %q, want %q", errs.String(), want)
	}
	if strings.Contains(errs.String(), "no pull requests shown") {
		t.Errorf("stderr = %q, which is `wt list`'s sentence, not sweep's", errs.String())
	}
}

// Sweep names a worktree the way `wt list` does. A pull request's worktree
// sits on somebody else's branch, so the name comes from the path — and a
// name that only sweep uses is a name you cannot type back at it.
func TestSweepNamesAWorktreeTheWayListDoes(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	path := ctx.Scheme().Dir("feat", "pr-2135-sanitize")
	if err := ctx.Repo.AddWorktree(path, "sanitize-refactor", "HEAD"); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "the work that was squashed")
	tip := testkit.GitOut(t, path, "rev-parse", "HEAD")

	p := sweptWith(t, ctx, map[string]github.PR{
		"sanitize-refactor": MergedPR(2135, "sanitize-refactor", tip)})
	if len(p.Remove) != 1 {
		t.Fatalf("remove = %+v", p.Remove)
	}
	if got := p.Remove[0].Work; got != "pr-2135-sanitize" {
		t.Errorf("sweep calls it %q; `wt list` calls it pr-2135-sanitize", got)
	}
	names, err := kit.WorkNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if n.Path == path && n.Work != p.Remove[0].Work {
			t.Errorf("wt list calls it %q and sweep calls it %q", n.Work, p.Remove[0].Work)
		}
	}
}
