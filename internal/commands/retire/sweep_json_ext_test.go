package retire_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

func planItem(t *testing.T, p retire.SweepPlanOutput, branch string) retire.SweepItem {
	t.Helper()
	for _, it := range p.Items {
		if it.Branch != nil && *it.Branch == branch {
			return it
		}
	}
	t.Fatalf("no item for %s in %+v", branch, p.Items)
	return retire.SweepItem{}
}

// Every part of the plan, each item with the evidence that it is merged or
// the reason it is kept; and making it writes nothing.
func TestSweepPlanJSONNamesEveryPartWithItsEvidence(t *testing.T) {
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 1)
	testkit.LandOnMain(t, main, "done-work")
	clean := provisiontest.MergedWorktree(t, ctx, "fix/login-crash")
	dirty := provisiontest.MergedWorktree(t, ctx, "fix/dirty")
	testkit.MustWrite(t, filepath.Join(dirty, "scratch.txt"), "not yet\n")
	testkit.BranchWithWork(t, main, "abandoned", 2)
	retire.GoneUpstream(t, main, "abandoned")
	_, squash, tip := provisiontest.Squashed(t, ctx, "feat/squash")
	pr := retire.MergedPR(34, squash, tip)
	pr.URL = "https://github.com/o/r/pull/34"

	before := testkit.RepoState(t, main)
	p, err := retiretest.SweepPlanJSON(t, ctx, retire.SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{squash: pr}})
	if err != nil {
		t.Fatal(err)
	}
	if after := testkit.RepoState(t, main); after != before {
		t.Errorf("the plan wrote to the repository:\nbefore %s\nafter %s", before, after)
	}
	if p.Command != "sweep" || p.Token == nil || p.Error != nil || p.Fetched || *p.Trunk != "main" || p.Repo == nil || *p.Repo != main {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.Bases) != 2 || p.Bases[0].Name != "origin/main" || p.Bases[1].Name != "main" {
		t.Errorf("bases = %+v", p.Bases)
	}

	it := planItem(t, p, "done-work")
	if it.Category != retire.SweepDelete || it.Merged == nil || it.Merged.How != retire.MergedAncestor || it.Merged.Into != "origin/main" ||
		it.Path != nil || len(it.Kept) != 0 || it.Subject == nil {
		t.Errorf("done-work = %+v", it)
	}
	it = planItem(t, p, "fix_wt/login-crash")
	if it.Category != retire.SweepRemove || it.Path == nil || *it.Path != clean || it.Work == nil || *it.Work != "login-crash" ||
		it.Merged == nil || it.Merged.How != retire.MergedAncestor {
		t.Errorf("login-crash = %+v", it)
	}
	it = planItem(t, p, "fix_wt/dirty")
	if it.Category != retire.SweepInUse || !slices.Equal(it.Kept, []string{retire.KeptDirty}) || !strings.Contains(it.Reason, "dirty") {
		t.Errorf("dirty = %+v", it)
	}
	it = planItem(t, p, "abandoned")
	if it.Category != retire.SweepUpstreamGone || it.Merged != nil || !slices.Equal(it.Kept, []string{retire.KeptNotMerged}) ||
		it.Ahead == nil || *it.Ahead != 2 {
		t.Errorf("abandoned = %+v", it)
	}
	it = planItem(t, p, squash)
	if it.Category != retire.SweepRemove || it.Merged == nil || it.Merged.How != retire.MergedPullRequest || it.Merged.PullRequest == nil ||
		*it.Merged.PullRequest != 34 || it.PullRequest == nil || it.PullRequest.URL == nil || *it.PullRequest.URL != pr.URL ||
		it.PullRequest.State != "MERGED" {
		t.Errorf("squash = %+v", it)
	}
}

// Commits found on trunk under other ids are merged by patch.
func TestSweepPlanJSONSaysMergedByPatch(t *testing.T) {
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	var buf bytes.Buffer
	path, err := provision.New(ctx, "feat/rates", provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	retire.CommitFile(t, path, "one")
	retire.RebasedOntoOrigin(t, main, "feat_wt/rates")

	p, err := retiretest.SweepPlanJSON(t, ctx, retire.SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{}})
	if err != nil {
		t.Fatal(err)
	}
	if it := planItem(t, p, "feat_wt/rates"); it.Merged == nil || it.Merged.How != retire.MergedPatch || it.Merged.Into != "origin/main" {
		t.Errorf("rates = %+v", it)
	}
}

// Every reason sweep keeps a merged worktree has its code.
func TestSweepPlanJSONCodesWhyAWorktreeIsKept(t *testing.T) {
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	session := provisiontest.MergedWorktree(t, ctx, "fix/session")
	locked := provisiontest.MergedWorktree(t, ctx, "fix/locked")
	testkit.GitIn(t, main, "worktree", "lock", "--reason", fmt.Sprintf("(pid %d start now)", os.Getpid()), locked)
	broken := provisiontest.MergedWorktree(t, ctx, "fix/broken")
	gitDir := testkit.GitOut(t, broken, "rev-parse", "--absolute-git-dir")
	testkit.MustWrite(t, filepath.Join(gitDir, "index"), "not an index")
	testkit.BranchWithWork(t, main, "done", 0)
	detached := filepath.Join(ctx.Repo.Parent, "demo-done")
	testkit.GitIn(t, main, "worktree", "add", "-q", "--detach", detached, "done")

	p, err := retiretest.SweepPlanJSON(t, ctx, retire.SweepOptions{NoFetch: true, Agents: testkit.IdleIn(t, session, "parked-1"), PRs: map[string]github.PR{}})
	if err != nil {
		t.Fatal(err)
	}
	for branch, want := range map[string]string{
		"fix_wt/session": retire.KeptSession, "fix_wt/locked": retire.KeptLockHeld, "fix_wt/broken": retire.KeptStatusUnknown,
	} {
		if it := planItem(t, p, branch); it.Category != retire.SweepInUse || !slices.Contains(it.Kept, want) {
			t.Errorf("%s: want %s in %+v", branch, want, it)
		}
	}
	var found bool
	for _, it := range p.Items {
		if it.Branch == nil && it.Path != nil && *it.Path == detached {
			found = it.Category == retire.SweepInUse && slices.Contains(it.Kept, retire.KeptDetached)
		}
	}
	if !found {
		t.Errorf("want the detached worktree kept as detached: %+v", p.Items)
	}

	bases, err := ctx.Repo.TrunkBases(ctx.Config.MainBranch)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := retire.PlanSweep(ctx, bases, func() ([]agents.Agent, error) { return nil, errors.New("claude is away") }, retire.NoPullRequests, false)
	if err != nil {
		t.Fatal(err)
	}
	if it := retire.PlanItemsOf(plan); !slices.ContainsFunc(it, func(i retire.SweepItem) bool { return slices.Contains(i.Kept, retire.KeptSessionsUnknown) }) {
		t.Errorf("want sessionsUnknown: %+v", it)
	}
}

// Nothing to remove or delete: no token, and kept items are still listed.
func TestSweepPlanJSONWithNothingToDoHasNoToken(t *testing.T) {
	t.Parallel()
	ctx, _, _ := testkit.SweepRepo(t)
	dirty := provisiontest.MergedWorktree(t, ctx, "fix/dirty")
	testkit.MustWrite(t, filepath.Join(dirty, "scratch.txt"), "not yet\n")

	p, err := retiretest.SweepPlanJSON(t, ctx, retire.SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{}})
	if err != nil || p.Token != nil || len(p.Items) != 1 {
		t.Fatalf("%v %+v", err, p)
	}
}

// A plan that cannot be made is still one object, with the error.
func TestSweepPlanJSONReportsWhyNoPlanWasMade(t *testing.T) {
	t.Parallel()
	ctx, main, origin := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 0)
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}
	p, err := retiretest.SweepPlanJSON(t, ctx, retire.SweepOptions{Agents: []agents.Agent{}, PRs: map[string]github.PR{}})
	if err == nil || p.Error == nil || p.Token != nil || len(p.Items) != 0 {
		t.Fatalf("%v %+v", err, p)
	}
}

// The token is the plan: the same plan runs, item by item, and says what
// became of each.
func TestSweepJSONCarriesOutThePlanItWasGiven(t *testing.T) {
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 1)
	testkit.LandOnMain(t, main, "done-work")
	clean := provisiontest.MergedWorktree(t, ctx, "fix/login-crash")
	dirty := provisiontest.MergedWorktree(t, ctx, "fix/dirty")
	testkit.MustWrite(t, filepath.Join(dirty, "scratch.txt"), "not yet\n")
	opts := retire.SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{}}
	p, err := retiretest.SweepPlanJSON(t, ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	doneTip := *planItem(t, p, "done-work").Tip

	opts.Expect = *p.Token
	r, human, err := retiretest.SweepJSON(t, ctx, opts)
	if err != nil || r.Outcome != kit.OutcomeDone || r.Error != nil || r.Token == nil || *r.Token != *p.Token {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	if r.RunID == nil {
		t.Fatalf("a sweep that deleted branches names the run it pinned them in: %+v", r)
	}
	it := retiretest.ResultItem(t, r, "done-work")
	pin := refpins.PinOf(*r.RunID, refpins.RefBranch, "done-work")
	if it.Result != retire.SweepDeleted || !it.BranchDeleted || it.WorktreeRemoved || kit.Deref(it.Pin) != pin ||
		testkit.GitOut(t, main, "rev-parse", pin) != doneTip ||
		!slices.Equal(it.RestoreCommand, []string{"wt", "refs", "restore", *r.RunID, "--only", "refs/heads/done-work", "--yes"}) {
		t.Errorf("done-work = %+v", it)
	}
	it = retiretest.ResultItem(t, r, "fix_wt/login-crash")
	if it.Result != retire.SweepRemoved || !it.BranchDeleted || !it.WorktreeRemoved || it.RestoreCommand == nil || testkit.Exists(clean) {
		t.Errorf("login-crash = %+v", it)
	}
	it = retiretest.ResultItem(t, r, "fix_wt/dirty")
	if it.Result != retire.SweepKept || it.Reason == nil || it.WorktreeRemoved || !testkit.Exists(dirty) {
		t.Errorf("dirty = %+v", it)
	}
	if !strings.Contains(human, "✓ deleted done-work") {
		t.Errorf("the progress goes to the other writer:\n%s", human)
	}
}

// Any difference from the plan refuses the run before anything is touched.
func TestSweepJSONExpectRefusesAChangedPlan(t *testing.T) {
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 0)
	opts := retire.SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{}}
	p, err := retiretest.SweepPlanJSON(t, ctx, opts)
	if err != nil {
		t.Fatal(err)
	}

	testkit.BranchWithWork(t, main, "late", 0)
	opts.Expect = *p.Token
	r, _, err := retiretest.SweepJSON(t, ctx, opts)
	if err == nil || r.Outcome != kit.OutcomeRefused || r.Error == nil || len(r.Items) != 0 {
		t.Fatalf("%v %+v", err, r)
	}
	if !ctx.Repo.BranchExists("done-work") || !ctx.Repo.BranchExists("late") {
		t.Error("nothing may be deleted when the plan changed")
	}

	// A plan with nothing to do has no token, and --expect still holds it.
	ctx2, _, _ := testkit.SweepRepo(t)
	opts.Expect = "1:0123456789abcdef0123456789abcdef"
	if r, _, err = retiretest.SweepJSON(t, ctx2, opts); err == nil || r.Outcome != kit.OutcomeRefused || r.Error == nil {
		t.Fatalf("an empty plan is not the plan a token named: %v %+v", err, r)
	}
}

// A worktree a session entered after the check is kept at the last moment,
// and says why; the rest of the plan goes ahead.
func TestSweepJSONKeepsAWorktreeASessionEnteredLate(t *testing.T) {
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 0)
	path := provisiontest.MergedWorktree(t, ctx, "fix/login-crash")
	opts := retire.SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{},
		Relist: func() ([]agents.Agent, error) { return testkit.IdleIn(t, path, "arrived"), nil }}
	p, err := retiretest.SweepPlanJSON(t, ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Expect = *p.Token
	r, _, err := retiretest.SweepJSON(t, ctx, opts)
	if err == nil || r.Outcome != kit.OutcomePartial {
		t.Fatalf("%v %+v", err, r)
	}
	if it := retiretest.ResultItem(t, r, "fix_wt/login-crash"); it.Result != retire.SweepKept || it.Reason == nil ||
		!strings.Contains(*it.Reason, "arrived") || !testkit.Exists(path) {
		t.Errorf("login-crash = %+v", it)
	}
	if it := retiretest.ResultItem(t, r, "done-work"); it.Result != retire.SweepDeleted {
		t.Errorf("done-work = %+v", it)
	}
}

// The token covers what GitHub says and every reason, not only the rows'
// parts: a pull request GitHub no longer answers for, or another session in
// a kept worktree, is another plan.
func TestSweepTokenCoversPullRequestsAndReasons(t *testing.T) {
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 0)
	kept := provisiontest.MergedWorktree(t, ctx, "fix/kept")
	token := func(opts retire.SweepOptions) string {
		p, err := retiretest.SweepPlanJSON(t, ctx, opts)
		if err != nil || p.Token == nil {
			t.Fatalf("%v %+v", err, p)
		}
		return *p.Token
	}
	pr := github.PR{Number: 7, HeadRefName: "done-work", State: "MERGED", BaseRefName: "main"}
	base := retire.SweepOptions{NoFetch: true, Agents: testkit.IdleIn(t, kept, "one"), PRs: map[string]github.PR{"done-work": pr}}
	withPR := token(base)
	noPR := base
	noPR.PRs = map[string]github.PR{}
	if token(noPR) == withPR {
		t.Error("a pull request GitHub no longer answers for is a different plan")
	}
	other := base
	other.Agents = testkit.IdleIn(t, kept, "two")
	if token(other) == withPR {
		t.Error("another session in a kept worktree is a different plan")
	}
	if token(base) != withPR {
		t.Error("the same plan has the same token")
	}
}

// A sweep --expect refuses says it fetched when it did.
func TestSweepJSONRefusalSaysItFetched(t *testing.T) {
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 0)
	r, _, err := retiretest.SweepJSON(t, ctx, retire.SweepOptions{Agents: []agents.Agent{}, PRs: map[string]github.PR{},
		Expect: "1:0123456789abcdef0123456789abcdef"})
	if err == nil || r.Error == nil || !r.Fetched {
		t.Fatalf("%v %+v", err, r)
	}
}

// The sessions are listed once more right before each worktree goes, after
// the fresh plan: a session that arrives in between keeps it.
func TestSweepReadsTheSessionsAgainRightBeforeAWorktreeGoes(t *testing.T) {
	t.Parallel()
	ctx, _, _ := testkit.SweepRepo(t)
	path := provisiontest.MergedWorktree(t, ctx, "fix/login-crash")
	calls := 0
	opts := retire.SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{},
		Relist: func() ([]agents.Agent, error) {
			calls++
			if calls == 1 {
				return []agents.Agent{}, nil
			}
			return testkit.IdleIn(t, path, "arrived"), nil
		}}
	r, human, err := retiretest.SweepJSON(t, ctx, opts)
	if err == nil {
		t.Fatalf("want the worktree kept\n%s", human)
	}
	// Refused at the last check, as a file written then is: failed, with
	// nothing removed.
	if it := retiretest.ResultItem(t, r, "fix_wt/login-crash"); it.Result != retire.SweepFailed || it.WorktreeRemoved ||
		it.BranchDeleted || it.Reason == nil || !strings.Contains(*it.Reason, "arrived") || !testkit.Exists(path) {
		t.Errorf("login-crash = %+v\n%s", it, human)
	}
}

// Idle and busy sessions are two problems to wt remove, and one reason to
// keep the worktree here: the code is listed once.
func TestSweepPlanJSONListsTheSessionCodeOnce(t *testing.T) {
	t.Parallel()
	ctx, _, _ := testkit.SweepRepo(t)
	path := provisiontest.MergedWorktree(t, ctx, "fix/login-crash")
	busy := testkit.IdleIn(t, path, "busy-1")
	busy[0].Status = "busy"
	listed := append(testkit.IdleIn(t, path, "idle-1"), busy...)
	p, err := retiretest.SweepPlanJSON(t, ctx, retire.SweepOptions{NoFetch: true, Agents: listed, PRs: map[string]github.PR{}})
	if err != nil {
		t.Fatal(err)
	}
	if it := planItem(t, p, "fix_wt/login-crash"); !slices.Equal(it.Kept, []string{retire.KeptSession}) {
		t.Errorf("kept = %q", it.Kept)
	}
}

// The plan shows the configuration fingerprint its token covers: the same
// file, the same value; another file, another value and another token.
func TestSweepPlanShowsTheConfigFingerprint(t *testing.T) {
	t.Parallel()
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 1)
	testkit.LandOnMain(t, main, "done-work")
	opts := retire.SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{}}
	p, err := retiretest.SweepPlanJSON(t, ctx, opts)
	if err != nil || p.ConfigFingerprint == nil || *p.ConfigFingerprint != hex.EncodeToString(kit.ConfigFingerprint(ctx)) {
		t.Fatalf("%v %+v", err, p)
	}
	conf := filepath.Join(main, "bin", "worktree", "worktree.conf")
	if err := os.WriteFile(conf, []byte(testkit.MinimalConf+"\n# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := retiretest.SweepPlanJSON(t, ctx, opts)
	if err != nil || kit.Deref(q.ConfigFingerprint) == kit.Deref(p.ConfigFingerprint) || kit.Deref(q.Token) == kit.Deref(p.Token) {
		t.Errorf("an edited file is another fingerprint and token: %+v %+v", p, q)
	}
}
