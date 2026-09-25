package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func planOfWork(t *testing.T, ctx *Context, work string) UpPlan {
	t.Helper()
	var buf bytes.Buffer
	if err := UpPlanJSON(ctx, work, &buf); err != nil {
		t.Fatal(err)
	}
	validateJSON(t, "status", buf.Bytes())
	var p UpPlan
	if err := json.Unmarshal(buf.Bytes(), &p); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return p
}

func upJSON(t *testing.T, ctx *Context, work string, opts RunOptions) (UpResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = NewRunJournal(&out)
	err := Up(ctx, work, opts, &human)
	validateJSON(t, "up", out.Bytes())
	var r UpResult
	if jerr := json.Unmarshal(out.Bytes(), &r); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	return r, err
}

// repoState is everything a read-only command must leave as it was: every
// ref, the loose objects, and each worktree's index.
func repoState(t *testing.T, main string) string {
	t.Helper()
	refs := gitOut(t, main, "for-each-ref", "--format=%(refname) %(objectname)")
	objects := gitOut(t, main, "count-objects", "-v")
	return refs + "\n" + objects
}

// The plan names the stack wt up would move, parents first, with a token, and
// writes nothing at all.
func TestPlanNamesTheStackAndWritesNothing(t *testing.T) {
	ctx, bump := runFixture(t, false)
	child := stackFixture(t, ctx)
	before := repoState(t, ctx.Repo.MainRoot)
	p := planOfWork(t, ctx, "child")
	if after := repoState(t, ctx.Repo.MainRoot); after != before {
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
	ctx, bump := runFixture(t, false)
	for work, code := range map[string]string{"/": IneligibleMainCheckout, "nope": IneligibleNotAWorktree} {
		if p := planOfWork(t, ctx, work); p.UpEligible || p.UpIneligibleCode == nil || *p.UpIneligibleCode != code || p.Token != nil {
			t.Errorf("%s: %+v", work, p)
		}
	}
	gitIn(t, bump, "checkout", "-q", "--detach")
	if p := planOfWork(t, ctx, bump); p.UpEligible || *p.UpIneligibleCode != IneligibleDetached {
		t.Errorf("detached: %+v", p)
	}

	main := committedRepo(t, minimalConf)
	if err := os.Remove(filepath.Join(main, "bin", "worktree", "worktree.conf")); err != nil {
		t.Fatal(err)
	}
	loose := OpenLenient(main, &bytes.Buffer{})
	if p := planOfWork(t, loose, "/"); p.UpEligible || p.Configured {
		t.Errorf("no configuration: %+v", p)
	}
}

// The token from the plan is what wt up checks: the same plan runs, and a
// stack that grew since is refused with nothing touched.
func TestUpExpectHoldsTheRunToThePlan(t *testing.T) {
	ctx, bump := runFixture(t, false)
	token := *planOfWork(t, ctx, "bump").Token

	stackFixture(t, ctx)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	opts := noAgents()
	opts.Expect = token
	r, err := upJSON(t, ctx, "bump", opts)
	if err == nil || r.Outcome != OutcomeRefused || r.Error == nil || len(r.Worktrees) != 0 {
		t.Fatalf("a grown stack is refused before any participant: %v %+v", err, r)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("the worktree moved")
	}

	opts.Expect = *planOfWork(t, ctx, "bump").Token
	if r, err = upJSON(t, ctx, "bump", opts); err != nil || r.Outcome != OutcomeDone {
		t.Fatalf("the plan as read runs: %v %+v", err, r)
	}
}

// A clean run: done, every participant rebased, with before, after and the
// push as an argv.
func TestUpJSONReportsARebasedStack(t *testing.T) {
	ctx, _ := runFixture(t, false)
	stackFixture(t, ctx)
	r, err := upJSON(t, ctx, "bump", noAgents())
	if err != nil || r.Outcome != OutcomeDone || r.Onto == nil || len(r.Worktrees) != 2 {
		t.Fatalf("%v %+v", err, r)
	}
	for _, p := range r.Worktrees {
		if p.Result != ResultRebased || p.Before == nil || p.After == nil || *p.Before == *p.After {
			t.Errorf("participant %+v", p)
		}
		if len(p.PushCommand) < 4 || p.PushCommand[0] != "git" || p.PushCommand[1] != "-C" || p.PushCommand[2] != p.Path {
			t.Errorf("push command %q", p.PushCommand)
		}
	}
}

// A refused parent: refused itself, the child not run, nothing changed.
func TestUpJSONReportsARefusedParentAndTheChildNotRun(t *testing.T) {
	ctx, bump := runFixture(t, false)
	stackFixture(t, ctx)
	if err := os.WriteFile(filepath.Join(bump, "a.txt"), []byte("dirt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := upJSON(t, ctx, "bump", noAgents())
	if err == nil || r.Outcome != OutcomeRefused || r.Error != nil || len(r.Worktrees) != 2 {
		t.Fatalf("%v %+v", err, r)
	}
	if r.Worktrees[0].Result != ResultRefused || r.Worktrees[1].Result != ResultNotRun || r.Worktrees[1].Reason == nil {
		t.Errorf("results %+v %+v", r.Worktrees[0], r.Worktrees[1])
	}
}

// A deferred step that fails after the rebase: the branch moved, the step is
// named, and the run is partial.
func TestUpJSONReportsAFailedDeferredStep(t *testing.T) {
	ctx, _ := runFixture(t, false)
	main := ctx.Repo.MainRoot
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n" +
		"defer:\n  - run: exit 3\n    paths: [v.txt]\n"
	if err := os.WriteFile(filepath.Join(main, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "commit", "-q", "-am", "a failing deferred step")
	gitIn(t, main, "fetch", "-q", "origin")
	r, _ := upJSON(t, ctx, "bump", noAgents())
	if r.Outcome != OutcomePartial || len(r.Worktrees) != 1 {
		t.Fatalf("%+v", r)
	}
	if p := r.Worktrees[0]; p.Result != ResultRebasedStepFailed || len(p.FailedSteps) == 0 || *p.Before == *p.After {
		t.Errorf("participant %+v", p)
	}
}

// An error before any participant is the run's own; the main checkout is one.
func TestUpJSONReportsAnErrorBeforeAnyParticipant(t *testing.T) {
	ctx, _ := runFixture(t, false)
	r, err := upJSON(t, ctx, "/", noAgents())
	if err == nil || r.Outcome != OutcomeRefused || r.Error == nil || !strings.Contains(*r.Error, "main checkout") {
		t.Errorf("%v %+v", err, r)
	}
}

// A signal writes the one object: the participant in flight interrupted,
// the way back as recovery, and nothing written a second time.
func TestJournalWritesOneObjectWhenInterrupted(t *testing.T) {
	var out bytes.Buffer
	j := NewRunJournal(&out)
	j.trunk("main", "origin/main", strings.Repeat("a", 40), true)
	j.join("one", "feat_wt/one", "/w/one", strings.Repeat("1", 40))
	j.join("two", "feat_wt/two", "/w/two", strings.Repeat("2", 40))
	j.set("feat_wt/one", func(p *UpParticipant) { p.Result = ResultRebased })
	j.interrupted("two", "interrupted while rebasing two: git rebase --abort")
	j.Finish()
	validateJSON(t, "up", out.Bytes())
	dec := json.NewDecoder(&out)
	var r UpResult
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if dec.More() {
		t.Error("a second object was written")
	}
	if r.Outcome != OutcomeInterrupted || r.Worktrees[1].Result != ResultInterrupted || r.Recovery == nil {
		t.Errorf("%+v", r)
	}
}

// The outcome rules, as docs/json.md states them.
func TestOutcomeRules(t *testing.T) {
	ps := func(results ...string) []*UpParticipant {
		var out []*UpParticipant
		for _, r := range results {
			out = append(out, &UpParticipant{Result: r})
		}
		return out
	}
	for _, tc := range []struct {
		ps    []*UpParticipant
		early bool
		want  string
	}{
		{ps(ResultRebased, ResultSkipped), false, OutcomeDone},
		{ps(ResultSkipped), false, OutcomeDone},
		{ps(ResultRefused, ResultNotRun), false, OutcomeRefused},
		{ps(ResultRestored), false, OutcomeRefused},
		{nil, true, OutcomeRefused},
		{ps(ResultRebased, ResultRefused), false, OutcomePartial},
		{ps(ResultRebasedStepFailed), false, OutcomePartial},
		{ps(ResultNeedsRecovery), false, OutcomePartial},
		{ps(ResultHandedOver, ResultNotRun), false, OutcomePartial},
	} {
		if got := outcomeOf(tc.ps, tc.early); got != tc.want {
			t.Errorf("%v early=%v: got %s, want %s", tc.ps, tc.early, got, tc.want)
		}
	}
}
