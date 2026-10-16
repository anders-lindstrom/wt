package retire_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// With the plan's token, the removal goes ahead and reports each effect.
func TestRemoveJSONRemovesWithTheToken(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/json-gone")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	p, err := retire.RemovePlanOf(t, ctx, path, retire.RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{Expect: *p.Token})
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if r.Outcome != retire.RemoveRemoved || retire.Deref(r.Token) != *p.Token || r.Error != nil {
		t.Errorf("%+v", r)
	}
	if s := retiretest.Step(t, r, kit.StepWorktree); s.Result != retire.WorktreeRemoved {
		t.Errorf("worktree %+v", s)
	}
	if s := retiretest.Step(t, r, kit.StepBranch); s.Result != quarantine.BranchDeleted || retire.Deref(s.Commit) != tip {
		t.Errorf("branch %+v", s)
	}
	if s := retiretest.Step(t, r, kit.StepSuperset); s.Result == kit.StepNotRun {
		t.Errorf("superset %+v", s)
	}
	// The branch is moved into a pin of its own run, which restore puts back.
	if r.RunID == nil {
		t.Fatalf("a removal that deletes its branch names the run it pinned it in: %+v", r)
	}
	pin := refpins.PinOf(*r.RunID, refpins.RefBranch, "fix_wt/json-gone")
	if s := retiretest.Step(t, r, kit.StepBranch); retire.Deref(s.Pin) != pin || testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", pin) != tip {
		t.Errorf("branch pin %v, want %s at %s", s.Pin, pin, tip)
	}
	want := []string{"wt", "refs", "restore", *r.RunID, "--only", "refs/heads/fix_wt/json-gone", "--yes"}
	if !slices.Equal(r.RestoreCommand, want) || r.Quarantine != nil {
		t.Errorf("restore %v quarantine %+v", r.RestoreCommand, r.Quarantine)
	}
	if testkit.Exists(path) || ctx.Repo.BranchExists("fix_wt/json-gone") {
		t.Error("the worktree and its branch must be gone")
	}
}

// A quarantine reports both moves, the journal's steps, and wt restore.
func TestRemoveJSONQuarantine(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/json-trash")
	dir := testkit.TrashFor(t, ctx)
	r, err := retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{Quarantine: dir})
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if r.Outcome != retire.RemoveRemoved || retiretest.Step(t, r, kit.StepWorktree).Result != retire.WorktreeQuarantined {
		t.Errorf("%+v", r)
	}
	q := r.Quarantine
	if q == nil || q.Dir != dir || !q.CheckoutMoved || !q.AdminMoved || q.RecoveryFile != filepath.Join(dir, quarantine.FileName) {
		t.Fatalf("quarantine %+v", q)
	}
	for _, s := range q.Steps {
		if s.State != quarantine.Done {
			t.Errorf("step %s is %s", s.Name, s.State)
		}
	}
	if !slices.Equal(r.RestoreCommand, []string{"wt", "restore", dir}) {
		t.Errorf("restore %v", r.RestoreCommand)
	}
}

// A plan the removal refuses is one object too: refused, the codes, nothing
// run.
func TestRemoveJSONRefusedTouchesNothing(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/json-dirty")
	testkit.MustWrite(t, filepath.Join(path, "wip.txt"), "x")
	r, err := retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{})
	if err == nil || r.Outcome != retire.RemoveRefused || r.Error == nil {
		t.Fatalf("%v %+v", err, r)
	}
	if !slices.Contains(retire.ProblemCodes(r.Problems), retire.KeptDirty) {
		t.Errorf("problems %+v", r.Problems)
	}
	for _, s := range r.Steps {
		if s.Result != kit.StepNotRun {
			t.Errorf("step %+v", s)
		}
	}
	if !testkit.Exists(path) {
		t.Error("the worktree must be there")
	}
}

// Each thing the plan stands on, changed after the plan was read, makes
// --expect refuse, touching nothing.
func TestRemoveExpectRefusesEveryChangeTouchingNothing(t *testing.T) {
	t.Parallel()
	landed := func(string, string) int { return 5 }
	cases := []struct {
		name   string
		setup  func(t *testing.T, ctx *kit.Context, path string, plan, run *retire.RemoveOptions)
		change func(t *testing.T, ctx *kit.Context, path string, run *retire.RemoveOptions)
	}{
		{name: "branch tip and HEAD", change: func(t *testing.T, _ *kit.Context, path string, _ *retire.RemoveOptions) {
			testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "late")
		}},
		{name: "HEAD of a detached checkout",
			setup: func(t *testing.T, _ *kit.Context, path string, _, _ *retire.RemoveOptions) {
				testkit.GitIn(t, path, "checkout", "-q", "--detach")
			},
			change: func(t *testing.T, _ *kit.Context, path string, _ *retire.RemoveOptions) {
				testkit.GitIn(t, path, "checkout", "-q", "--detach", "HEAD~1")
			}},
		{name: "base commit", change: func(t *testing.T, ctx *kit.Context, _ string, _ *retire.RemoveOptions) {
			testkit.GitIn(t, ctx.Repo.MainRoot, "commit", "-q", "--allow-empty", "-m", "trunk moved")
		}},
		{name: "dirty", change: func(t *testing.T, _ *kit.Context, path string, _ *retire.RemoveOptions) {
			testkit.MustWrite(t, filepath.Join(path, "wip.txt"), "x")
		}},
		{name: "lock", change: func(t *testing.T, _ *kit.Context, path string, _ *retire.RemoveOptions) {
			testkit.GitIn(t, path, "worktree", "lock", "--reason", "pid 999999 gone", path)
		}},
		{name: "sessions",
			setup: func(_ *testing.T, _ *kit.Context, _ string, plan, run *retire.RemoveOptions) {
				plan.Force, run.Force = kit.ForceAll, kit.ForceAll
			},
			change: func(_ *testing.T, _ *kit.Context, path string, run *retire.RemoveOptions) {
				run.Agents = []wtsync.Agent{{ID: "late", Cwd: path, Status: "idle"}}
			}},
		{name: "operation", change: func(t *testing.T, _ *kit.Context, path string, _ *retire.RemoveOptions) {
			head := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
			testkit.MustWrite(t, filepath.Join(testkit.GitDirFor(t, path), "MERGE_HEAD"), head+"\n")
		}},
		{name: "quarantine folder",
			setup: func(t *testing.T, ctx *kit.Context, _ string, plan, run *retire.RemoveOptions) {
				plan.Quarantine = testkit.TrashFor(t, ctx)
				run.Quarantine = plan.Quarantine
			},
			change: func(_ *testing.T, _ *kit.Context, _ string, run *retire.RemoveOptions) {
				run.Quarantine += "-elsewhere"
			}},
		{name: "kept name taken",
			setup: func(t *testing.T, _ *kit.Context, path string, _, _ *retire.RemoveOptions) {
				testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "unmerged")
			},
			change: func(t *testing.T, ctx *kit.Context, _ string, _ *retire.RemoveOptions) {
				testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "expect")
			}},
		{name: "superset opt-in", change: func(_ *testing.T, ctx *kit.Context, _ string, _ *retire.RemoveOptions) {
			testkit.OptIn(ctx)
		}},
		{name: "planned outcome",
			setup: func(t *testing.T, _ *kit.Context, path string, plan, run *retire.RemoveOptions) {
				testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "squashed on GitHub")
				plan.Landed, run.Landed = landed, landed
			},
			change: func(_ *testing.T, _ *kit.Context, _ string, run *retire.RemoveOptions) {
				run.Landed = func(string, string) int { return 0 }
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/expect")
			testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "on the branch")
			testkit.GitIn(t, ctx.Repo.MainRoot, "merge", "-q", "--ff-only", "fix_wt/expect")
			var plan, run retire.RemoveOptions
			if c.setup != nil {
				c.setup(t, ctx, path, &plan, &run)
			}
			p, err := retire.RemovePlanOf(t, ctx, path, plan)
			if err != nil || p.Token == nil {
				t.Fatalf("the plan must be removable: %v %+v", err, p)
			}
			c.change(t, ctx, path, &run)
			tip, _ := ctx.Repo.ResolveRef("refs/heads/fix_wt/expect")
			run.Expect = *p.Token

			r, err := retiretest.RemoveJSON(t, ctx, path, run)
			if err == nil || r.Outcome != retire.RemoveRefused {
				t.Fatalf("want a refusal: %v %+v", err, r)
			}
			if !slices.Contains(retire.ProblemCodes(r.Problems), kit.ProblemPlanChanged) {
				t.Errorf("problems %+v", r.Problems)
			}
			if !testkit.Exists(path) {
				t.Error("the worktree must be there")
			}
			if now, ok := ctx.Repo.ResolveRef("refs/heads/fix_wt/expect"); !ok || now != tip {
				t.Error("the branch must be untouched")
			}
			if run.Quarantine != "" && testkit.Exists(run.Quarantine) {
				t.Error("no quarantine folder may be made")
			}
			if list, _ := ctx.Repo.Worktrees(); func() bool { wt, ok := list.ByPath(path); return ok && wt.Locked && c.name != "lock" }() {
				t.Error("the worktree must not be locked")
			}
		})
	}
}

// A signal between the two moves writes the one object, from what is true
// then: the checkout moved, the admin dir not, the branch step not run.
func TestRemoveJSONInterruptedBetweenTheMovesWritesOneObject(t *testing.T) {
	// Not parallel: marks the process-wide interrupt journal interrupted.
	ctx, path := provisiontest.SafetyWorktree(t, "fix/json-signal")
	dir := testkit.TrashFor(t, ctx)
	h := &quarantine.Hooks{AfterSave: func(_ *quarantine.Record, at string) error {
		if at == quarantine.StepMoveCheckout+":"+quarantine.Done {
			// What kit.WatchSignals does, but for the exit.
			kit.CurrentInterruptJournal().Interrupted("", "interrupted")
		}
		return nil
	}}
	var out, progress bytes.Buffer
	_ = retire.RemoveJSON(ctx, retire.RemoveTarget{Path: path}, retire.RemoveOptions{Quarantine: dir, QuarantineHooks: h}, &out, &progress)
	r := retiretest.DecodeRemoveOutput(t, out.Bytes())
	if r.Outcome != kit.OutcomeInterrupted || retire.Deref(r.Recovery) != "interrupted" {
		t.Errorf("%+v", r)
	}
	if s := retiretest.Step(t, r, kit.StepWorktree); s.Result != retire.WorktreePartlyMoved {
		t.Errorf("worktree %+v", s)
	}
	if s := retiretest.Step(t, r, kit.StepBranch); s.Result != kit.StepNotRun {
		t.Errorf("branch %+v", s)
	}
	if q := r.Quarantine; q == nil || !q.CheckoutMoved || q.AdminMoved {
		t.Errorf("quarantine %+v", q)
	}
	if !slices.Equal(r.RestoreCommand, []string{"wt", "restore", dir}) {
		t.Errorf("restore %v", r.RestoreCommand)
	}
}

// A branch step that fails once both moves are done is partial, and says
// which effect failed.
func TestRemoveJSONBranchFailureAfterTheMovesIsPartial(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/json-partial")
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	testkit.GitIn(t, path, "branch", "json-partial")
	dir := testkit.TrashFor(t, ctx)
	r, err := retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{Quarantine: dir})
	if err == nil || r.Outcome != retire.RemovePartial || r.Error == nil {
		t.Fatalf("%v %+v", err, r)
	}
	if s := retiretest.Step(t, r, kit.StepWorktree); s.Result != retire.WorktreeQuarantined {
		t.Errorf("worktree %+v", s)
	}
	if s := retiretest.Step(t, r, kit.StepBranch); s.Result != quarantine.BranchFailed || s.Reason == nil {
		t.Errorf("branch %+v", s)
	}
	if q := r.Quarantine; q == nil || !q.CheckoutMoved || !q.AdminMoved {
		t.Errorf("quarantine %+v", q)
	}
}

// Outside a repository, both are still one object.
func TestRemoveJSONFailedBeforeTheRepository(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	retire.RemoveFailedJSON(&out, true, kit.ErrNotInRepo)
	testkit.ValidateJSON(t, "remove-plan", out.Bytes())
	out.Reset()
	retire.RemoveFailedJSON(&out, false, errors.New("no repo"))
	if r := retiretest.DecodeRemoveOutput(t, out.Bytes()); r.Outcome != retire.RemoveRefused || r.Error == nil {
		t.Errorf("%+v", r)
	}
}

// A quarantine that stopped after it locked the worktree has changed
// something: it is partial, and wt restore undoes it.
func TestRemoveJSONQuarantineStoppedAfterTheLockIsPartial(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/json-pin")
	dir := testkit.TrashFor(t, ctx)
	r, err := retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{Quarantine: dir,
		QuarantineHooks: testkit.CrashAt(quarantine.StepPin + ":" + quarantine.Running)})
	if err == nil || r.Outcome != retire.RemovePartial {
		t.Fatalf("%v %+v", err, r)
	}
	if q := r.Quarantine; q == nil || q.CheckoutMoved || q.AdminMoved {
		t.Errorf("quarantine %+v", q)
	}
	if !slices.Equal(r.RestoreCommand, []string{"wt", "restore", dir}) {
		t.Errorf("restore %v", r.RestoreCommand)
	}
}

// The plan and the result say how trunk was found, as every other plan
// does, and the token covers it.
func TestRemoveJSONSaysHowTrunkWasFound(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/trunk-source")
	p, err := retire.RemovePlanOf(t, ctx, path, retire.RemoveOptions{Agents: []wtsync.Agent{}})
	if err != nil || p.TrunkSource == nil || *p.TrunkSource != string(ctx.Config.TrunkSource) || *p.TrunkSource == "" {
		t.Fatalf("plan trunkSource = %v (%v)", p.TrunkSource, err)
	}
	r, err := retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{Agents: []wtsync.Agent{}, Expect: retire.Deref(p.Token)})
	if err != nil || r.TrunkSource == nil || *r.TrunkSource != *p.TrunkSource {
		t.Fatalf("result trunkSource = %v (%v)", r.TrunkSource, err)
	}
}
