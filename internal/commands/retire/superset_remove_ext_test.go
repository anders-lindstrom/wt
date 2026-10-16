package retire_test

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// wt sweep deletes the workspace of each worktree it removes, and its --json
// says so per item.
func TestSweepDeregistersEachRemovedWorktree(t *testing.T) {
	for _, quarantined := range []bool{false, true} {
		name := "delete"
		if quarantined {
			name = "quarantine"
		}
		t.Run(name, func(t *testing.T) {
			ctx, _, _ := testkit.SweepRepo(t)
			path := provisiontest.MergedWorktree(t, ctx, "fix/one")
			testkit.OptIn(ctx)
			log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
			opts := retire.SweepOptions{NoFetch: true, Agents: retire.NoClaude, PRs: map[string]github.PR{}}
			if quarantined {
				opts.Quarantine = testkit.TrashFor(t, ctx)
			}
			r, human, err := retiretest.SweepJSON(t, ctx, opts)
			if err != nil || r.Outcome != kit.OutcomeDone {
				t.Fatalf("%v %+v\n%s", err, r, human)
			}
			it := retiretest.ResultItem(t, r, "fix_wt/one")
			if it.Superset == nil || it.Superset.Result != provision.StepDeregistered || it.Superset.Reason != nil {
				t.Errorf("item superset = %+v", it.Superset)
			}
			if got := testkit.ArgvOf(t, log); len(got) != 2 || got[1] != "ws delete --local w1 --json" {
				t.Errorf("argv = %q", got)
			}
		})
	}
}

// A sweep item that removed no worktree has no Superset step.
func TestSweepItemWithoutAWorktreeHasNoSupersetStep(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 1)
	testkit.LandOnMain(t, main, "done-work")
	r, human, err := retiretest.SweepJSON(t, ctx, retire.SweepOptions{NoFetch: true, Agents: retire.NoClaude, PRs: map[string]github.PR{}})
	if err != nil {
		t.Fatalf("%v\n%s", err, human)
	}
	if it := retiretest.ResultItem(t, r, "done-work"); it.Superset != nil {
		t.Errorf("superset = %+v, want null", it.Superset)
	}
}

// The opt-out is part of the plan and of its token, and the result's
// superset step says optedOut.
func TestRemoveJSONKeepSupersetIsInThePlanAndTheToken(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/kept-ws-json")
	testkit.OptIn(ctx)
	log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
	plain, err := retire.RemovePlanOf(t, ctx, path, retire.RemoveOptions{Agents: retire.NoClaude})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := retire.RemovePlanOf(t, ctx, path, retire.RemoveOptions{Agents: retire.NoClaude, KeepSuperset: true})
	if err != nil {
		t.Fatal(err)
	}
	if plain.KeepSuperset || !kept.KeepSuperset {
		t.Errorf("keepSuperset = %v / %v", plain.KeepSuperset, kept.KeepSuperset)
	}
	if retire.Deref(plain.Token) == retire.Deref(kept.Token) {
		t.Error("the token must change with --keep-superset")
	}
	r, err := retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{Agents: retire.NoClaude, KeepSuperset: true, Expect: retire.Deref(kept.Token)})
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if s := retiretest.Step(t, r, kit.StepSuperset); s.Result != provision.StepOptedOut || s.Reason == nil {
		t.Errorf("superset step = %+v", s)
	}
	if got := testkit.ArgvOf(t, log); got != nil {
		t.Errorf("superset was run: %q", got)
	}
}

func TestSweepKeepSupersetAsksSupersetNothing(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	path := provisiontest.MergedWorktree(t, ctx, "fix/one")
	testkit.OptIn(ctx)
	log := provisiontest.SupersetAt(t, ctx, path, testkit.Deleted)
	opts := retire.SweepOptions{NoFetch: true, Agents: retire.NoClaude, PRs: map[string]github.PR{}}
	plain, err := retiretest.SweepPlanJSON(t, ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.KeepSuperset = true
	kept, err := retiretest.SweepPlanJSON(t, ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if plain.KeepSuperset || !kept.KeepSuperset {
		t.Errorf("keepSuperset = %v / %v", plain.KeepSuperset, kept.KeepSuperset)
	}
	if retire.Deref(plain.Token) == retire.Deref(kept.Token) {
		t.Error("the token must change with --keep-superset")
	}
	opts.Yes, opts.Expect = true, retire.Deref(kept.Token)
	r, human, err := retiretest.SweepJSON(t, ctx, opts)
	if err != nil || r.Outcome != kit.OutcomeDone {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	it := retiretest.ResultItem(t, r, "fix_wt/one")
	if it.Superset == nil || it.Superset.Result != provision.StepOptedOut {
		t.Errorf("item superset = %+v", it.Superset)
	}
	if got := testkit.ArgvOf(t, log); got != nil {
		t.Errorf("superset was run: %q", got)
	}
}
