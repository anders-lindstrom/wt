package commands

import (
	"slices"
	"testing"
)

// declareNoPathsStep adds a deferred step with no paths to trunk's
// declaration, ahead of whatever it declares already.
func declareNoPathsStep(t *testing.T, ctx *Context) {
	t.Helper()
	main := ctx.Repo.MainRoot
	writeFile(t, main, ".wt-sync.yaml", "defer:\n  - run: \"true\"\n  - run: cp v.txt gen.txt\n    paths: [v.txt]\n    commit: \"chore: regen\"\n"+
		"conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n")
	gitIn(t, main, "commit", "-q", "-am", "declare a step with no paths")
	gitIn(t, main, "fetch", "-q", "origin")
}

// The overview lists the deferred steps trunk declares on every worktree, in
// the declared order, each with its gate: also on one a run would skip.
func TestSyncJSONListsTheDeclaredDeferredStepsOnEveryWorktree(t *testing.T) {
	ctx, _ := runFixture(t, true)
	declareNoPathsStep(t, ctx)
	o := overviewJSON(t, ctx)
	if len(o.Worktrees) != 2 {
		t.Fatalf("worktrees = %+v", o.Worktrees)
	}
	for _, w := range o.Worktrees {
		d := w.DeferredDeclared
		if len(d) != 2 || d[0].Step != "true" || d[0].Paths == nil || len(d[0].Paths) != 0 ||
			d[1].Step != "cp v.txt gen.txt" || !slices.Equal(d[1].Paths, []string{"v.txt"}) {
			t.Errorf("%s (%s): deferredDeclared = %+v", w.Work, w.Verdict, d)
		}
	}
	if other := overviewRow(t, o, "other"); other.Verdict == VerdictProceed {
		t.Errorf("other = %+v, want a worktree a run would not rebase", other)
	}
}

// A trunk that declares no deferred step, and one with no declaration at
// all, list none: an empty array, which the schema holds it to.
func TestSyncJSONListsNoDeclaredStepsWhenTrunkDeclaresNone(t *testing.T) {
	ctx, _ := runFixture(t, false)
	for _, w := range overviewJSON(t, ctx).Worktrees {
		if w.DeferredDeclared == nil || len(w.DeferredDeclared) != 0 {
			t.Errorf("%s: deferredDeclared = %#v, want an empty array", w.Work, w.DeferredDeclared)
		}
	}
	gitIn(t, ctx.Repo.MainRoot, "rm", "-q", ".wt-sync.yaml")
	gitIn(t, ctx.Repo.MainRoot, "commit", "-q", "-m", "declare nothing")
	gitIn(t, ctx.Repo.MainRoot, "fetch", "-q", "origin")
	o := overviewJSON(t, ctx)
	if o.Declared || len(o.Worktrees) == 0 {
		t.Fatalf("overview = %+v", o)
	}
	for _, w := range o.Worktrees {
		if w.DeferredDeclared == nil || len(w.DeferredDeclared) != 0 {
			t.Errorf("%s, nothing declared: deferredDeclared = %#v, want an empty array", w.Work, w.DeferredDeclared)
		}
	}
}

// The plan wt up runs against lists them on each stack member, from trunk
// as last fetched, and none when trunk declares none.
func TestPlanListsTheDeclaredDeferredStepsOnEachStackMember(t *testing.T) {
	ctx, _ := runFixture(t, true)
	p := planOfWork(t, ctx, "bump")
	if len(p.Stack) != 1 {
		t.Fatalf("stack = %+v", p.Stack)
	}
	d := p.Stack[0].DeferredDeclared
	if len(d) != 1 || d[0].Step != "cp v.txt gen.txt" || !slices.Equal(d[0].Paths, []string{"v.txt"}) {
		t.Errorf("deferredDeclared = %+v", d)
	}
	// A declaration that has not been fetched is not the plan's yet.
	writeFile(t, ctx.Repo.MainRoot, ".wt-sync.yaml", "defer:\n  - run: \"true\"\n")
	gitIn(t, ctx.Repo.MainRoot, "commit", "-q", "-am", "declare another step")
	if d := planOfWork(t, ctx, "bump").Stack[0].DeferredDeclared; len(d) != 1 || d[0].Step != "cp v.txt gen.txt" {
		t.Errorf("before the fetch: deferredDeclared = %+v", d)
	}
	gitIn(t, ctx.Repo.MainRoot, "fetch", "-q", "origin")
	if d := planOfWork(t, ctx, "bump").Stack[0].DeferredDeclared; len(d) != 1 || d[0].Step != "true" || d[0].Paths == nil || len(d[0].Paths) != 0 {
		t.Errorf("after the fetch: deferredDeclared = %+v", d)
	}

	ctx, _ = runFixture(t, false)
	p = planOfWork(t, ctx, "bump")
	if len(p.Stack) != 1 || p.Stack[0].DeferredDeclared == nil || len(p.Stack[0].DeferredDeclared) != 0 {
		t.Errorf("no step declared: stack = %#v", p.Stack)
	}
}

// What the overview lists for a worktree is what the run then reports of
// it, step for step by the same string.
func TestTheDeclaredStepsPairWithTheRunsDeferred(t *testing.T) {
	ctx, _ := runFixture(t, true)
	declared := overviewRow(t, overviewJSON(t, ctx), "bump").DeferredDeclared
	r, err := syncRunJSON(t, ctx, []string{"bump"}, RunOptions{NoFetch: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Worktrees) != 1 || len(r.Worktrees[0].Deferred) != len(declared) || len(declared) != 1 {
		t.Fatalf("declared %+v, ran %+v", declared, r.Worktrees)
	}
	if ran := r.Worktrees[0].Deferred[0]; ran.Step != declared[0].Step || ran.Result != DeferredCommitted {
		t.Errorf("declared %+v, ran %+v", declared[0], ran)
	}
}
