package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// The plan names the stack wt up would move, parents first, with a token, and
// writes nothing at all.
func TestPlanNamesTheStackAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	child := provisiontest.StackFixture(t, ctx)
	before := testkit.RepoState(t, ctx.Repo.MainRoot)
	p := synccmdtest.PlanOfWork(t, ctx, "child")
	if after := testkit.RepoState(t, ctx.Repo.MainRoot); after != before {
		t.Errorf("the plan wrote to the repository:\nbefore %s\nafter %s", before, after)
	}
	if !p.UpEligible || p.Token == nil || p.Schema != 1 || p.Command != "status" {
		t.Fatalf("plan = %+v", p)
	}
	var branches, paths []string
	for _, m := range p.Stack {
		branches, paths = append(branches, m.Branch), append(paths, m.Path)
	}
	if !slices.Equal(branches, []string{"feat_wt/bump", "feat_wt/child"}) || paths[0] != bump || paths[1] != child {
		t.Errorf("stack = %+v", p.Stack)
	}
	if p.Worktree == nil || p.Worktree.Work != "child" || p.Worktree.Behind == nil || *p.Worktree.Behind != 1 {
		t.Errorf("worktree = %+v", p.Worktree)
	}
	if *p.TrunkRef != "origin/main" || !p.TrunkRefExists || p.TrunkTip == nil {
		t.Errorf("trunk = %+v", p)
	}
}

// Every reason wt up would not start is a plan saying so, never an error.
func TestPlanSaysWhyWtUpWouldNotStart(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	for work, code := range map[string]string{"/": synccmd.IneligibleMainCheckout, "nope": synccmd.IneligibleNotAWorktree} {
		if p := synccmdtest.PlanOfWork(t, ctx, work); p.UpEligible || p.UpIneligibleCode == nil || *p.UpIneligibleCode != code || p.Token != nil {
			t.Errorf("%s: %+v", work, p)
		}
	}
	testkit.GitIn(t, bump, "checkout", "-q", "--detach")
	if p := synccmdtest.PlanOfWork(t, ctx, bump); p.UpEligible || *p.UpIneligibleCode != synccmd.IneligibleDetached {
		t.Errorf("detached: %+v", p)
	}

	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	if err := os.Remove(filepath.Join(main, "bin", "worktree", "worktree.conf")); err != nil {
		t.Fatal(err)
	}
	loose := kit.OpenLenient(main, &bytes.Buffer{})
	if p := synccmdtest.PlanOfWork(t, loose, "/"); p.UpEligible || p.Configured {
		t.Errorf("no configuration: %+v", p)
	}
}

// A branch trunk already contains has nothing to rebase: the plan says so
// with its own code, wherever the branch's remote is. A branch with a commit
// trunk lacks stays eligible.
func TestPlanSaysABranchTrunkContainsHasNothingToRebase(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	if p := synccmdtest.PlanOfWork(t, ctx, bump); !p.UpEligible || p.Worktree.Ahead == nil || *p.Worktree.Ahead != 1 {
		t.Fatalf("a branch with its own commit: %+v", p)
	}
	main := ctx.Repo.MainRoot
	testkit.GitIn(t, main, "merge", "-q", "-s", "ours", "--no-edit", "feat_wt/bump")
	testkit.GitIn(t, main, "fetch", "-q", "origin")
	p := synccmdtest.PlanOfWork(t, ctx, bump)
	if p.UpEligible || p.UpIneligibleCode == nil || *p.UpIneligibleCode != synccmd.IneligibleInTrunk || p.Token != nil {
		t.Fatalf("a branch trunk contains: %+v", p)
	}
	if p.UpIneligibleReason == nil || !strings.Contains(*p.UpIneligibleReason, "origin/main") {
		t.Errorf("reason = %v", p.UpIneligibleReason)
	}
	// Local trunk alone containing it is not enough once origin is behind: wt up rebases onto origin.
	testkit.GitIn(t, main, "update-ref", "refs/remotes/origin/main", "HEAD~1")
	if p := synccmdtest.PlanOfWork(t, ctx, bump); !p.UpEligible {
		t.Errorf("origin lacks the commit: %+v", p)
	}
}

// The token from the plan is what wt up checks: the same plan runs, and a
// stack that grew since is refused with nothing touched.
func TestUpExpectHoldsTheRunToThePlan(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	token := *synccmdtest.PlanOfWork(t, ctx, "bump").Token

	provisiontest.StackFixture(t, ctx)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	opts := synccmdtest.NoAgents()
	opts.Expect = token
	r, err := synccmdtest.UpJSON(t, ctx, "bump", opts)
	if err == nil || r.Outcome != kit.OutcomeRefused || r.Error == nil || len(r.Worktrees) != 0 {
		t.Fatalf("a grown stack is refused before any participant: %v %+v", err, r)
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("the worktree moved")
	}

	opts.Expect = *synccmdtest.PlanOfWork(t, ctx, "bump").Token
	if r, err = synccmdtest.UpJSON(t, ctx, "bump", opts); err != nil || r.Outcome != kit.OutcomeDone {
		t.Fatalf("the plan as read runs: %v %+v", err, r)
	}
}

// A clean run: done, every participant rebased, with before, after and the
// push as an argv.
func TestUpJSONReportsARebasedStack(t *testing.T) {
	t.Parallel()
	ctx, _ := provisiontest.RunFixture(t, false)
	provisiontest.StackFixture(t, ctx)
	r, err := synccmdtest.UpJSON(t, ctx, "bump", synccmdtest.NoAgents())
	if err != nil || r.Outcome != kit.OutcomeDone || r.Onto == nil || len(r.Worktrees) != 2 {
		t.Fatalf("%v %+v", err, r)
	}
	for _, p := range r.Worktrees {
		if p.Result != synccmd.ResultRebased || p.Before == nil || p.After == nil || *p.Before == *p.After {
			t.Errorf("participant %+v", p)
		}
		if len(p.PushCommand) < 4 || p.PushCommand[0] != "git" || p.PushCommand[1] != "-C" || p.PushCommand[2] != p.Path {
			t.Errorf("push command %q", p.PushCommand)
		}
	}
}

// A refused parent: refused itself, the child not run, nothing changed.
func TestUpJSONReportsARefusedParentAndTheChildNotRun(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	provisiontest.StackFixture(t, ctx)
	if err := os.WriteFile(filepath.Join(bump, "a.txt"), []byte("dirt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := synccmdtest.UpJSON(t, ctx, "bump", synccmdtest.NoAgents())
	if err == nil || r.Outcome != kit.OutcomeRefused || r.Error != nil || len(r.Worktrees) != 2 {
		t.Fatalf("%v %+v", err, r)
	}
	if r.Worktrees[0].Result != synccmd.ResultRefused || r.Worktrees[1].Result != synccmd.ResultNotRun || r.Worktrees[1].Reason == nil {
		t.Errorf("results %+v %+v", r.Worktrees[0], r.Worktrees[1])
	}
}

// A deferred step that fails after the rebase: the branch moved, the step is
// named, and the run is partial.
func TestUpJSONReportsAFailedDeferredStep(t *testing.T) {
	t.Parallel()
	ctx, _ := provisiontest.RunFixture(t, false)
	main := ctx.Repo.MainRoot
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n" +
		"defer:\n  - run: exit 3\n    paths: [v.txt]\n"
	if err := os.WriteFile(filepath.Join(main, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main, "commit", "-q", "-am", "a failing deferred step")
	testkit.GitIn(t, main, "fetch", "-q", "origin")
	r, _ := synccmdtest.UpJSON(t, ctx, "bump", synccmdtest.NoAgents())
	if r.Outcome != kit.OutcomePartial || len(r.Worktrees) != 1 {
		t.Fatalf("%+v", r)
	}
	if p := r.Worktrees[0]; p.Result != synccmd.ResultRebasedStepFailed || len(p.FailedSteps) == 0 || *p.Before == *p.After {
		t.Errorf("participant %+v", p)
	}
}

// An error before any participant is the run's own; the main checkout is one.
func TestUpJSONReportsAnErrorBeforeAnyParticipant(t *testing.T) {
	t.Parallel()
	ctx, _ := provisiontest.RunFixture(t, false)
	r, err := synccmdtest.UpJSON(t, ctx, "/", synccmdtest.NoAgents())
	if err == nil || r.Outcome != kit.OutcomeRefused || r.Error == nil || !strings.Contains(*r.Error, "main checkout") {
		t.Errorf("%v %+v", err, r)
	}
}

// A signal writes the one object: the participant in flight interrupted,
// the way back as recovery, and nothing written a second time.
func TestJournalWritesOneObjectWhenInterrupted(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	j := synccmd.NewRunJournal(&out)
	j.Trunk("main", kit.Strp("conventional"), "origin/main", strings.Repeat("a", 40), true)
	j.Join("one", "feat_wt/one", "/w/one", strings.Repeat("1", 40), &syncrun.OwnRemoteSync{State: "none"})
	j.Join("two", "feat_wt/two", "/w/two", strings.Repeat("2", 40), &syncrun.OwnRemoteSync{State: "none"})
	j.Set("feat_wt/one", func(p *synccmd.UpParticipant) { p.Result = synccmd.ResultRebased })
	j.Interrupted("two", "interrupted while rebasing two: git rebase --abort")
	j.Finish()
	testkit.ValidateJSON(t, "up", out.Bytes())
	dec := json.NewDecoder(&out)
	var r synccmd.UpResult
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if dec.More() {
		t.Error("a second object was written")
	}
	if r.Outcome != kit.OutcomeInterrupted || r.Worktrees[1].Result != synccmd.ResultInterrupted || r.Recovery == nil {
		t.Errorf("%+v", r)
	}
	if r.TrunkSource == nil || *r.TrunkSource != "conventional" {
		t.Errorf("trunkSource = %v, want the source recorded with trunk", r.TrunkSource)
	}
}

// The outcome rules, as docs/json.md states them.
func TestOutcomeRules(t *testing.T) {
	t.Parallel()
	ps := func(results ...string) []*synccmd.SyncParticipant {
		var out []*synccmd.SyncParticipant
		for _, r := range results {
			out = append(out, &synccmd.SyncParticipant{UpParticipant: synccmd.UpParticipant{Result: r}})
		}
		return out
	}
	for _, tc := range []struct {
		ps    []*synccmd.SyncParticipant
		early bool
		want  string
	}{
		{ps(synccmd.ResultRebased, synccmd.ResultSkipped), false, kit.OutcomeDone},
		{ps(synccmd.ResultSkipped), false, kit.OutcomeDone},
		{ps(synccmd.ResultRefused, synccmd.ResultNotRun), false, kit.OutcomeRefused},
		{ps(synccmd.ResultRestored), false, kit.OutcomeRefused},
		{nil, true, kit.OutcomeRefused},
		{ps(synccmd.ResultRebased, synccmd.ResultRefused), false, kit.OutcomePartial},
		{ps(synccmd.ResultRebasedStepFailed), false, kit.OutcomePartial},
		{ps(synccmd.ResultNeedsRecovery), false, kit.OutcomePartial},
		{ps(synccmd.ResultHandedOver, synccmd.ResultNotRun), false, kit.OutcomePartial},
		{ps(synccmd.ResultUndone, synccmd.ResultSkipped), false, kit.OutcomeDone},
		{ps(synccmd.ResultUndone), true, kit.OutcomePartial},
	} {
		if got := synccmd.OutcomeOf(tc.ps, tc.early); got != tc.want {
			t.Errorf("%v early=%v: got %s, want %s", tc.ps, tc.early, got, tc.want)
		}
	}
}
