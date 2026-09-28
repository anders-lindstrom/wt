package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

func sweepPlanJSON(t *testing.T, ctx *Context, opts SweepOptions) (SweepPlanOutput, error) {
	t.Helper()
	var out, progress bytes.Buffer
	err := SweepPlanJSON(ctx, opts, &out, &progress)
	validateJSON(t, "sweep-plan", out.Bytes())
	var p SweepPlanOutput
	if jerr := json.Unmarshal(out.Bytes(), &p); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	return p, err
}

func sweepJSON(t *testing.T, ctx *Context, opts SweepOptions) (SweepResult, string, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = NewSweepJournal(&out)
	opts.Yes = true
	err := Sweep(ctx, opts, &human)
	validateJSON(t, "sweep", out.Bytes())
	var r SweepResult
	if jerr := json.Unmarshal(out.Bytes(), &r); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	return r, human.String(), err
}

func planItem(t *testing.T, p SweepPlanOutput, branch string) SweepItem {
	t.Helper()
	for _, it := range p.Items {
		if it.Branch != nil && *it.Branch == branch {
			return it
		}
	}
	t.Fatalf("no item for %s in %+v", branch, p.Items)
	return SweepItem{}
}

func resultItem(t *testing.T, r SweepResult, branch string) *SweepResultItem {
	t.Helper()
	for _, it := range r.Items {
		if it.Branch != nil && *it.Branch == branch {
			return it
		}
	}
	t.Fatalf("no item for %s in %+v", branch, r.Items)
	return nil
}

// Every part of the plan, each item with the evidence that it is merged or
// the reason it is kept; and making it writes nothing.
func TestSweepPlanJSONNamesEveryPartWithItsEvidence(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")
	clean := mergedWorktree(t, ctx, "fix/login-crash")
	dirty := mergedWorktree(t, ctx, "fix/dirty")
	mustWrite(t, filepath.Join(dirty, "scratch.txt"), "not yet\n")
	branchWithWork(t, main, "abandoned", 2)
	goneUpstream(t, main, "abandoned")
	_, squash, tip := squashed(t, ctx, "feat/squash")
	pr := mergedPR(34, squash, tip)
	pr.URL = "https://github.com/o/r/pull/34"

	before := repoState(t, main)
	p, err := sweepPlanJSON(t, ctx, SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{squash: pr}})
	if err != nil {
		t.Fatal(err)
	}
	if after := repoState(t, main); after != before {
		t.Errorf("the plan wrote to the repository:\nbefore %s\nafter %s", before, after)
	}
	if p.Command != "sweep" || p.Token == nil || p.Error != nil || p.Fetched || *p.Trunk != "main" || p.Repo == nil || *p.Repo != main {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.Bases) != 2 || p.Bases[0].Name != "origin/main" || p.Bases[1].Name != "main" {
		t.Errorf("bases = %+v", p.Bases)
	}

	it := planItem(t, p, "done-work")
	if it.Category != SweepDelete || it.Merged == nil || it.Merged.How != MergedAncestor || it.Merged.Into != "origin/main" ||
		it.Path != nil || len(it.Kept) != 0 || it.Subject == nil {
		t.Errorf("done-work = %+v", it)
	}
	it = planItem(t, p, "fix_wt/login-crash")
	if it.Category != SweepRemove || it.Path == nil || *it.Path != clean || it.Work == nil || *it.Work != "login-crash" ||
		it.Merged == nil || it.Merged.How != MergedAncestor {
		t.Errorf("login-crash = %+v", it)
	}
	it = planItem(t, p, "fix_wt/dirty")
	if it.Category != SweepInUse || !slices.Equal(it.Kept, []string{KeptDirty}) || !strings.Contains(it.Reason, "dirty") {
		t.Errorf("dirty = %+v", it)
	}
	it = planItem(t, p, "abandoned")
	if it.Category != SweepUpstreamGone || it.Merged != nil || !slices.Equal(it.Kept, []string{KeptNotMerged}) ||
		it.Ahead == nil || *it.Ahead != 2 {
		t.Errorf("abandoned = %+v", it)
	}
	it = planItem(t, p, squash)
	if it.Category != SweepRemove || it.Merged == nil || it.Merged.How != MergedPullRequest || it.Merged.PullRequest == nil ||
		*it.Merged.PullRequest != 34 || it.PullRequest == nil || it.PullRequest.URL == nil || *it.PullRequest.URL != pr.URL ||
		it.PullRequest.State != "MERGED" {
		t.Errorf("squash = %+v", it)
	}
}

// Commits found on trunk under other ids are merged by patch.
func TestSweepPlanJSONSaysMergedByPatch(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	var buf bytes.Buffer
	path, err := New(ctx, "feat/rates", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, path, "one")
	rebasedOntoOrigin(t, main, "feat_wt/rates")

	p, err := sweepPlanJSON(t, ctx, SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}})
	if err != nil {
		t.Fatal(err)
	}
	if it := planItem(t, p, "feat_wt/rates"); it.Merged == nil || it.Merged.How != MergedPatch || it.Merged.Into != "origin/main" {
		t.Errorf("rates = %+v", it)
	}
}

// Every reason sweep keeps a merged worktree has its code.
func TestSweepPlanJSONCodesWhyAWorktreeIsKept(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	session := mergedWorktree(t, ctx, "fix/session")
	locked := mergedWorktree(t, ctx, "fix/locked")
	gitIn(t, main, "worktree", "lock", "--reason", fmt.Sprintf("(pid %d start now)", os.Getpid()), locked)
	broken := mergedWorktree(t, ctx, "fix/broken")
	gitDir := gitOut(t, broken, "rev-parse", "--absolute-git-dir")
	mustWrite(t, filepath.Join(gitDir, "index"), "not an index")
	branchWithWork(t, main, "done", 0)
	detached := filepath.Join(ctx.Repo.Parent, "demo-done")
	gitIn(t, main, "worktree", "add", "-q", "--detach", detached, "done")

	p, err := sweepPlanJSON(t, ctx, SweepOptions{NoFetch: true, Agents: idleIn(t, session, "parked-1"), PRs: map[string]github.PR{}})
	if err != nil {
		t.Fatal(err)
	}
	for branch, want := range map[string]string{
		"fix_wt/session": KeptSession, "fix_wt/locked": KeptLockHeld, "fix_wt/broken": KeptStatusUnknown,
	} {
		if it := planItem(t, p, branch); it.Category != SweepInUse || !slices.Contains(it.Kept, want) {
			t.Errorf("%s: want %s in %+v", branch, want, it)
		}
	}
	var found bool
	for _, it := range p.Items {
		if it.Branch == nil && it.Path != nil && *it.Path == detached {
			found = it.Category == SweepInUse && slices.Contains(it.Kept, KeptDetached)
		}
	}
	if !found {
		t.Errorf("want the detached worktree kept as detached: %+v", p.Items)
	}

	bases, err := trunkBases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planSweep(ctx, bases, func() ([]wtsync.Agent, error) { return nil, errors.New("claude is away") }, noPullRequests)
	if err != nil {
		t.Fatal(err)
	}
	if it := planItemsOf(plan); !slices.ContainsFunc(it, func(i SweepItem) bool { return slices.Contains(i.Kept, KeptSessionsUnknown) }) {
		t.Errorf("want sessionsUnknown: %+v", it)
	}
}

// Nothing to remove or delete: no token, and kept items are still listed.
func TestSweepPlanJSONWithNothingToDoHasNoToken(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	dirty := mergedWorktree(t, ctx, "fix/dirty")
	mustWrite(t, filepath.Join(dirty, "scratch.txt"), "not yet\n")

	p, err := sweepPlanJSON(t, ctx, SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}})
	if err != nil || p.Token != nil || len(p.Items) != 1 {
		t.Fatalf("%v %+v", err, p)
	}
}

// A plan that cannot be made is still one object, with the error.
func TestSweepPlanJSONReportsWhyNoPlanWasMade(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}
	p, err := sweepPlanJSON(t, ctx, SweepOptions{Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}})
	if err == nil || p.Error == nil || p.Token != nil || len(p.Items) != 0 {
		t.Fatalf("%v %+v", err, p)
	}
}

// The token is the plan: the same plan runs, item by item, and says what
// became of each.
func TestSweepJSONCarriesOutThePlanItWasGiven(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")
	clean := mergedWorktree(t, ctx, "fix/login-crash")
	dirty := mergedWorktree(t, ctx, "fix/dirty")
	mustWrite(t, filepath.Join(dirty, "scratch.txt"), "not yet\n")
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}}
	p, err := sweepPlanJSON(t, ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	doneTip := *planItem(t, p, "done-work").Tip

	opts.Expect = *p.Token
	r, human, err := sweepJSON(t, ctx, opts)
	if err != nil || r.Outcome != OutcomeDone || r.Error != nil || r.Token == nil || *r.Token != *p.Token {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	it := resultItem(t, r, "done-work")
	if it.Result != SweepDeleted || !it.BranchDeleted || it.WorktreeRemoved ||
		!slices.Equal(it.RestoreCommand, []string{"git", "-C", main, "branch", "done-work", doneTip}) {
		t.Errorf("done-work = %+v", it)
	}
	it = resultItem(t, r, "fix_wt/login-crash")
	if it.Result != SweepRemoved || !it.BranchDeleted || !it.WorktreeRemoved || it.RestoreCommand == nil || exists(clean) {
		t.Errorf("login-crash = %+v", it)
	}
	it = resultItem(t, r, "fix_wt/dirty")
	if it.Result != SweepKept || it.Reason == nil || it.WorktreeRemoved || !exists(dirty) {
		t.Errorf("dirty = %+v", it)
	}
	if !strings.Contains(human, "✓ deleted done-work") {
		t.Errorf("the progress goes to the other writer:\n%s", human)
	}
}

// Any difference from the plan refuses the run before anything is touched.
func TestSweepJSONExpectRefusesAChangedPlan(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}}
	p, err := sweepPlanJSON(t, ctx, opts)
	if err != nil {
		t.Fatal(err)
	}

	branchWithWork(t, main, "late", 0)
	opts.Expect = *p.Token
	r, _, err := sweepJSON(t, ctx, opts)
	if err == nil || r.Outcome != OutcomeRefused || r.Error == nil || len(r.Items) != 0 {
		t.Fatalf("%v %+v", err, r)
	}
	if !ctx.Repo.BranchExists("done-work") || !ctx.Repo.BranchExists("late") {
		t.Error("nothing may be deleted when the plan changed")
	}

	// A plan with nothing to do has no token, and --expect still holds it.
	ctx2, _, _ := sweepRepo(t)
	opts.Expect = "1:0123456789abcdef0123456789abcdef"
	if r, _, err = sweepJSON(t, ctx2, opts); err == nil || r.Outcome != OutcomeRefused || r.Error == nil {
		t.Fatalf("an empty plan is not the plan a token named: %v %+v", err, r)
	}
}

// A worktree a session entered after the check is kept at the last moment,
// and says why; the rest of the plan goes ahead.
func TestSweepJSONKeepsAWorktreeASessionEnteredLate(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{},
		Relist: func() ([]wtsync.Agent, error) { return idleIn(t, path, "arrived"), nil }}
	p, err := sweepPlanJSON(t, ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Expect = *p.Token
	r, _, err := sweepJSON(t, ctx, opts)
	if err == nil || r.Outcome != OutcomePartial {
		t.Fatalf("%v %+v", err, r)
	}
	if it := resultItem(t, r, "fix_wt/login-crash"); it.Result != SweepKept || it.Reason == nil ||
		!strings.Contains(*it.Reason, "arrived") || !exists(path) {
		t.Errorf("login-crash = %+v", it)
	}
	if it := resultItem(t, r, "done-work"); it.Result != SweepDeleted {
		t.Errorf("done-work = %+v", it)
	}
}

// A signal writes the one object: the item in flight interrupted, those not
// reached not run, and nothing written a second time.
func TestSweepJournalWritesOneObjectWhenInterrupted(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "one", 0)
	branchWithWork(t, main, "two", 0)
	plan := planOf(t, ctx)

	var out bytes.Buffer
	j := NewSweepJournal(&out)
	j.begin(ctx, plan, "1:abc")
	j.start(sweepKey(plan.Delete[0]))
	j.interrupted("", "interrupted")
	j.Finish()
	validateJSON(t, "sweep", out.Bytes())
	dec := json.NewDecoder(&out)
	var r SweepResult
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if dec.More() {
		t.Error("a second object was written")
	}
	if r.Outcome != OutcomeInterrupted || r.Recovery == nil || r.Items[0].Result != SweepInterrupted || r.Items[1].Result != SweepNotRun {
		t.Errorf("%+v", r)
	}
}

// The outcome rules, as docs/json.md states them.
func TestSweepOutcomeRules(t *testing.T) {
	items := func(results ...string) []*SweepResultItem {
		var out []*SweepResultItem
		for _, r := range results {
			out = append(out, &SweepResultItem{Category: SweepDelete, Result: r})
		}
		out = append(out, &SweepResultItem{Category: SweepInUse, Result: SweepKept})
		return out
	}
	for _, tc := range []struct {
		items []*SweepResultItem
		early bool
		want  string
	}{
		{items(SweepDeleted, SweepRemoved), false, OutcomeDone},
		{items(), false, OutcomeDone},
		{items(), true, OutcomeRefused},
		{items(SweepKept, SweepNotRun), false, OutcomeRefused},
		{items(SweepDeleted, SweepKept), false, OutcomePartial},
		{items(SweepFailed), false, OutcomeRefused},
		{[]*SweepResultItem{{Category: SweepRemove, Result: SweepFailed, WorktreeRemoved: true}}, false, OutcomePartial},
	} {
		if got := sweepOutcome(tc.items, tc.early); got != tc.want {
			t.Errorf("%+v early=%v: got %s, want %s", tc.items, tc.early, got, tc.want)
		}
	}
}

// The token covers what GitHub says and every reason, not only the rows'
// parts: a pull request GitHub no longer answers for, or another session in
// a kept worktree, is another plan.
func TestSweepTokenCoversPullRequestsAndReasons(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	kept := mergedWorktree(t, ctx, "fix/kept")
	token := func(opts SweepOptions) string {
		p, err := sweepPlanJSON(t, ctx, opts)
		if err != nil || p.Token == nil {
			t.Fatalf("%v %+v", err, p)
		}
		return *p.Token
	}
	pr := github.PR{Number: 7, HeadRefName: "done-work", State: "MERGED", BaseRefName: "main"}
	base := SweepOptions{NoFetch: true, Agents: idleIn(t, kept, "one"), PRs: map[string]github.PR{"done-work": pr}}
	withPR := token(base)
	noPR := base
	noPR.PRs = map[string]github.PR{}
	if token(noPR) == withPR {
		t.Error("a pull request GitHub no longer answers for is a different plan")
	}
	other := base
	other.Agents = idleIn(t, kept, "two")
	if token(other) == withPR {
		t.Error("another session in a kept worktree is a different plan")
	}
	if token(base) != withPR {
		t.Error("the same plan has the same token")
	}
}

// A sweep --expect refuses says it fetched when it did.
func TestSweepJSONRefusalSaysItFetched(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	r, _, err := sweepJSON(t, ctx, SweepOptions{Agents: []wtsync.Agent{}, PRs: map[string]github.PR{},
		Expect: "1:0123456789abcdef0123456789abcdef"})
	if err == nil || r.Error == nil || !r.Fetched {
		t.Fatalf("%v %+v", err, r)
	}
}

// The sessions are listed once more right before each worktree goes, after
// the fresh plan: a session that arrives in between keeps it.
func TestSweepReadsTheSessionsAgainRightBeforeAWorktreeGoes(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	calls := 0
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{},
		Relist: func() ([]wtsync.Agent, error) {
			calls++
			if calls == 1 {
				return []wtsync.Agent{}, nil
			}
			return idleIn(t, path, "arrived"), nil
		}}
	r, human, err := sweepJSON(t, ctx, opts)
	if err == nil {
		t.Fatalf("want the worktree kept\n%s", human)
	}
	// Refused at the last check, as a file written then is: failed, with
	// nothing removed.
	if it := resultItem(t, r, "fix_wt/login-crash"); it.Result != SweepFailed || it.WorktreeRemoved ||
		it.BranchDeleted || it.Reason == nil || !strings.Contains(*it.Reason, "arrived") || !exists(path) {
		t.Errorf("login-crash = %+v\n%s", it, human)
	}
}

// Idle and busy sessions are two problems to wt remove, and one reason to
// keep the worktree here: the code is listed once.
func TestSweepPlanJSONListsTheSessionCodeOnce(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	busy := idleIn(t, path, "busy-1")
	busy[0].Status = "busy"
	agents := append(idleIn(t, path, "idle-1"), busy...)
	p, err := sweepPlanJSON(t, ctx, SweepOptions{NoFetch: true, Agents: agents, PRs: map[string]github.PR{}})
	if err != nil {
		t.Fatal(err)
	}
	if it := planItem(t, p, "fix_wt/login-crash"); !slices.Equal(it.Kept, []string{KeptSession}) {
		t.Errorf("kept = %q", it.Kept)
	}
}
