package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// removePlanJSON is wt remove <path> --dry-run --json, checked against its
// schema.
func removePlanJSON(t *testing.T, ctx *Context, path string, opts RemoveOptions) (RemovePlanOutput, error) {
	t.Helper()
	var out, progress bytes.Buffer
	err := RemovePlanJSON(ctx, RemoveTarget{Path: path}, opts, &out, &progress)
	validateJSON(t, "remove-plan", out.Bytes())
	var p RemovePlanOutput
	if jerr := json.Unmarshal(out.Bytes(), &p); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out.String())
	}
	return p, err
}

// removeJSON is wt remove <path> --yes --json: exactly one object, checked
// against its schema.
func removeJSON(t *testing.T, ctx *Context, path string, opts RemoveOptions) (RemoveOutput, error) {
	t.Helper()
	var out, progress bytes.Buffer
	err := RemoveJSON(ctx, RemoveTarget{Path: path}, opts, &out, &progress)
	return decodeRemoveOutput(t, out.Bytes()), err
}

func decodeRemoveOutput(t *testing.T, out []byte) RemoveOutput {
	t.Helper()
	validateJSON(t, "remove", out)
	dec := json.NewDecoder(bytes.NewReader(out))
	var r RemoveOutput
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if dec.More() {
		t.Errorf("more than one object:\n%s", out)
	}
	return r
}

func step(t *testing.T, r RemoveOutput, name string) RemoveStep {
	t.Helper()
	for _, s := range r.Steps {
		if s.Step == name {
			return s
		}
	}
	t.Fatalf("no step %s in %+v", name, r.Steps)
	return RemoveStep{}
}

func problemCodes(ps []RemoveProblem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

// The plan names the worktree, its commit, where the branch stands and what
// will become of it, and a token; it changes nothing.
func TestRemovePlanJSONDescribesTheRemoval(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/planned")
	tip := strings.TrimSpace(gitOut(t, path, "rev-parse", "HEAD"))

	p, err := removePlanJSON(t, ctx, path, RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Token == nil || p.Error != nil || len(p.Problems) != 0 {
		t.Fatalf("a removable worktree has a token and no problems: %+v", p)
	}
	if deref(p.Path) != path || deref(p.Head) != tip || p.Detached || deref(p.AdminDir) != gitDirFor(t, path) {
		t.Errorf("worktree facts %+v", p)
	}
	b := p.Branch
	if b == nil || b.Name != "fix_wt/planned" || deref(b.Tip) != tip || b.Merge != "merged" ||
		b.Outcome != quarantine.BranchPlanDelete || deref(b.Base) != "main" {
		t.Errorf("branch %+v", b)
	}
	if len(p.Bases) != 1 || p.Bases[0].Name != "main" {
		t.Errorf("bases %+v", p.Bases)
	}
	if !exists(path) || !ctx.Repo.BranchExists("fix_wt/planned") {
		t.Error("a plan changes nothing")
	}
}

// A plan the removal would refuse has every reason as a code sweep's --json
// knows, an error, and no token.
func TestRemovePlanJSONRefusalHasCodesAndNoToken(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/dirty-plan")
	mustWrite(t, filepath.Join(path, "wip.txt"), "x")
	gitIn(t, path, "worktree", "lock", "--reason", "held", path)

	p, err := removePlanJSON(t, ctx, path, RemoveOptions{Agents: []wtsync.Agent{{ID: "s1", Name: "one", Cwd: path, Status: "idle"}}})
	if err == nil {
		t.Fatal("a refused plan exits non-zero")
	}
	if p.Token != nil || p.Error == nil {
		t.Errorf("token %v error %v", p.Token, p.Error)
	}
	codes := problemCodes(p.Problems)
	for _, want := range []string{KeptDirty, KeptSession, KeptLockHeld} {
		if !slices.Contains(codes, want) {
			t.Errorf("want %s in %v", want, codes)
		}
	}
	for _, pr := range p.Problems {
		if pr.Force != (pr.Code == KeptSession || pr.Code == KeptLockHeld) {
			t.Errorf("force on %+v", pr)
		}
	}
	if len(p.Sessions) != 1 || p.Sessions[0].ID != "s1" || p.Sessions[0].State != "idle" {
		t.Errorf("sessions %+v", p.Sessions)
	}
	if p.Lock == nil || !p.Lock.Held || deref(p.Lock.Reason) != "held" {
		t.Errorf("lock %+v", p.Lock)
	}
}

// A detached worktree has no branch, and a quarantine folder that cannot be
// used is a problem with its own code.
func TestRemovePlanJSONDetachedAndQuarantineProblem(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/detached-plan")
	gitIn(t, path, "checkout", "-q", "--detach")
	dir := trashFor(t, ctx)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := removePlanJSON(t, ctx, path, RemoveOptions{Quarantine: dir})
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !p.Detached || p.Branch != nil || deref(p.Quarantine) != dir {
		t.Errorf("%+v", p)
	}
	if !slices.Contains(problemCodes(p.Problems), ProblemQuarantineUnusable) {
		t.Errorf("problems %+v", p.Problems)
	}
}

// A branch the removal deletes whose commits nothing else holds is listed,
// with the command that brings them back.
func TestRemovePlanJSONListsTheCommitsADeletedBranchTakes(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/squashed")
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	tip := strings.TrimSpace(gitOut(t, path, "rev-parse", "HEAD"))
	landed := func(string, string) int { return 12 }

	p, err := removePlanJSON(t, ctx, path, RemoveOptions{Landed: landed})
	if err != nil {
		t.Fatal(err)
	}
	if b := p.Branch; b == nil || b.Outcome != quarantine.BranchPlanDelete || b.Merge != "unmerged" ||
		b.PullRequest == nil || *b.PullRequest != 12 || b.Ahead != 1 {
		t.Fatalf("branch %+v", p.Branch)
	}
	if len(p.Unreachable) != 1 {
		t.Fatalf("unreachable %+v", p.Unreachable)
	}
	l := p.Unreachable[0]
	want := []string{"git", "-C", ctx.Repo.MainRoot, "branch", "fix_wt/squashed", tip}
	if l.Kind != LostBranch || l.OID != tip || l.Count != 1 || !slices.Equal(l.RestoreCommand, want) {
		t.Errorf("lost %+v", l)
	}
}

// With the plan's token, the removal goes ahead and reports each effect.
func TestRemoveJSONRemovesWithTheToken(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/json-gone")
	tip := strings.TrimSpace(gitOut(t, path, "rev-parse", "HEAD"))
	p, err := removePlanJSON(t, ctx, path, RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := removeJSON(t, ctx, path, RemoveOptions{Expect: *p.Token})
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if r.Outcome != RemoveRemoved || deref(r.Token) != *p.Token || r.Error != nil {
		t.Errorf("%+v", r)
	}
	if s := step(t, r, StepWorktree); s.Result != WorktreeRemoved {
		t.Errorf("worktree %+v", s)
	}
	if s := step(t, r, StepBranch); s.Result != quarantine.BranchDeleted || deref(s.Commit) != tip {
		t.Errorf("branch %+v", s)
	}
	if s := step(t, r, StepSuperset); s.Result == StepNotRun {
		t.Errorf("superset %+v", s)
	}
	// The branch is moved into a pin of its own run, which restore puts back.
	if r.RunID == nil {
		t.Fatalf("a removal that deletes its branch names the run it pinned it in: %+v", r)
	}
	pin := pinOf(*r.RunID, RefBranch, "fix_wt/json-gone")
	if s := step(t, r, StepBranch); deref(s.Pin) != pin || gitOut(t, ctx.Repo.MainRoot, "rev-parse", pin) != tip {
		t.Errorf("branch pin %v, want %s at %s", s.Pin, pin, tip)
	}
	want := []string{"wt", "refs", "restore", *r.RunID, "--only", "refs/heads/fix_wt/json-gone", "--yes"}
	if !slices.Equal(r.RestoreCommand, want) || r.Quarantine != nil {
		t.Errorf("restore %v quarantine %+v", r.RestoreCommand, r.Quarantine)
	}
	if exists(path) || ctx.Repo.BranchExists("fix_wt/json-gone") {
		t.Error("the worktree and its branch must be gone")
	}
}

// A quarantine reports both moves, the journal's steps, and wt restore.
func TestRemoveJSONQuarantine(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/json-trash")
	dir := trashFor(t, ctx)
	r, err := removeJSON(t, ctx, path, RemoveOptions{Quarantine: dir})
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if r.Outcome != RemoveRemoved || step(t, r, StepWorktree).Result != WorktreeQuarantined {
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
	ctx, path := safetyWorktree(t, "fix/json-dirty")
	mustWrite(t, filepath.Join(path, "wip.txt"), "x")
	r, err := removeJSON(t, ctx, path, RemoveOptions{})
	if err == nil || r.Outcome != RemoveRefused || r.Error == nil {
		t.Fatalf("%v %+v", err, r)
	}
	if !slices.Contains(problemCodes(r.Problems), KeptDirty) {
		t.Errorf("problems %+v", r.Problems)
	}
	for _, s := range r.Steps {
		if s.Result != StepNotRun {
			t.Errorf("step %+v", s)
		}
	}
	if !exists(path) {
		t.Error("the worktree must be there")
	}
}

// Each thing the plan stands on, changed after the plan was read, makes
// --expect refuse, touching nothing.
func TestRemoveExpectRefusesEveryChangeTouchingNothing(t *testing.T) {
	landed := func(string, string) int { return 5 }
	cases := []struct {
		name   string
		setup  func(t *testing.T, ctx *Context, path string, plan, run *RemoveOptions)
		change func(t *testing.T, ctx *Context, path string, run *RemoveOptions)
	}{
		{name: "branch tip and HEAD", change: func(t *testing.T, _ *Context, path string, _ *RemoveOptions) {
			gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "late")
		}},
		{name: "HEAD of a detached checkout",
			setup: func(t *testing.T, _ *Context, path string, _, _ *RemoveOptions) {
				gitIn(t, path, "checkout", "-q", "--detach")
			},
			change: func(t *testing.T, _ *Context, path string, _ *RemoveOptions) {
				gitIn(t, path, "checkout", "-q", "--detach", "HEAD~1")
			}},
		{name: "base commit", change: func(t *testing.T, ctx *Context, _ string, _ *RemoveOptions) {
			gitIn(t, ctx.Repo.MainRoot, "commit", "-q", "--allow-empty", "-m", "trunk moved")
		}},
		{name: "dirty", change: func(t *testing.T, _ *Context, path string, _ *RemoveOptions) {
			mustWrite(t, filepath.Join(path, "wip.txt"), "x")
		}},
		{name: "lock", change: func(t *testing.T, _ *Context, path string, _ *RemoveOptions) {
			gitIn(t, path, "worktree", "lock", "--reason", "pid 999999 gone", path)
		}},
		{name: "sessions",
			setup: func(_ *testing.T, _ *Context, _ string, plan, run *RemoveOptions) {
				plan.Force, run.Force = ForceAll, ForceAll
			},
			change: func(_ *testing.T, _ *Context, path string, run *RemoveOptions) {
				run.Agents = []wtsync.Agent{{ID: "late", Cwd: path, Status: "idle"}}
			}},
		{name: "operation", change: func(t *testing.T, _ *Context, path string, _ *RemoveOptions) {
			head := strings.TrimSpace(gitOut(t, path, "rev-parse", "HEAD"))
			mustWrite(t, filepath.Join(gitDirFor(t, path), "MERGE_HEAD"), head+"\n")
		}},
		{name: "quarantine folder",
			setup: func(t *testing.T, ctx *Context, _ string, plan, run *RemoveOptions) {
				plan.Quarantine = trashFor(t, ctx)
				run.Quarantine = plan.Quarantine
			},
			change: func(_ *testing.T, _ *Context, _ string, run *RemoveOptions) {
				run.Quarantine += "-elsewhere"
			}},
		{name: "kept name taken",
			setup: func(t *testing.T, _ *Context, path string, _, _ *RemoveOptions) {
				gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "unmerged")
			},
			change: func(t *testing.T, ctx *Context, _ string, _ *RemoveOptions) {
				gitIn(t, ctx.Repo.MainRoot, "branch", "expect")
			}},
		{name: "superset opt-in", change: func(_ *testing.T, ctx *Context, _ string, _ *RemoveOptions) {
			optIn(ctx)
		}},
		{name: "planned outcome",
			setup: func(t *testing.T, _ *Context, path string, plan, run *RemoveOptions) {
				gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "squashed on GitHub")
				plan.Landed, run.Landed = landed, landed
			},
			change: func(_ *testing.T, _ *Context, _ string, run *RemoveOptions) {
				run.Landed = func(string, string) int { return 0 }
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, path := safetyWorktree(t, "fix/expect")
			gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "on the branch")
			gitIn(t, ctx.Repo.MainRoot, "merge", "-q", "--ff-only", "fix_wt/expect")
			var plan, run RemoveOptions
			if c.setup != nil {
				c.setup(t, ctx, path, &plan, &run)
			}
			p, err := removePlanJSON(t, ctx, path, plan)
			if err != nil || p.Token == nil {
				t.Fatalf("the plan must be removable: %v %+v", err, p)
			}
			c.change(t, ctx, path, &run)
			tip, _ := ctx.Repo.ResolveRef("refs/heads/fix_wt/expect")
			run.Expect = *p.Token

			r, err := removeJSON(t, ctx, path, run)
			if err == nil || r.Outcome != RemoveRefused {
				t.Fatalf("want a refusal: %v %+v", err, r)
			}
			if !slices.Contains(problemCodes(r.Problems), ProblemPlanChanged) {
				t.Errorf("problems %+v", r.Problems)
			}
			if !exists(path) {
				t.Error("the worktree must be there")
			}
			if now, ok := ctx.Repo.ResolveRef("refs/heads/fix_wt/expect"); !ok || now != tip {
				t.Error("the branch must be untouched")
			}
			if run.Quarantine != "" && exists(run.Quarantine) {
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
	ctx, path := safetyWorktree(t, "fix/json-signal")
	dir := trashFor(t, ctx)
	t.Cleanup(func() { quarantine.AfterSave = nil })
	quarantine.AfterSave = func(_ *quarantine.Record, at string) error {
		if at == quarantine.StepMoveCheckout+":"+quarantine.Done {
			// What watchSignals does, but for the exit.
			currentInterruptJournal().interrupted("", "interrupted")
		}
		return nil
	}
	var out, progress bytes.Buffer
	_ = RemoveJSON(ctx, RemoveTarget{Path: path}, RemoveOptions{Quarantine: dir}, &out, &progress)
	r := decodeRemoveOutput(t, out.Bytes())
	if r.Outcome != OutcomeInterrupted || deref(r.Recovery) != "interrupted" {
		t.Errorf("%+v", r)
	}
	if s := step(t, r, StepWorktree); s.Result != WorktreePartlyMoved {
		t.Errorf("worktree %+v", s)
	}
	if s := step(t, r, StepBranch); s.Result != StepNotRun {
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
	ctx, path := safetyWorktree(t, "fix/json-partial")
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	gitIn(t, path, "branch", "json-partial")
	dir := trashFor(t, ctx)
	r, err := removeJSON(t, ctx, path, RemoveOptions{Quarantine: dir})
	if err == nil || r.Outcome != RemovePartial || r.Error == nil {
		t.Fatalf("%v %+v", err, r)
	}
	if s := step(t, r, StepWorktree); s.Result != WorktreeQuarantined {
		t.Errorf("worktree %+v", s)
	}
	if s := step(t, r, StepBranch); s.Result != quarantine.BranchFailed || s.Reason == nil {
		t.Errorf("branch %+v", s)
	}
	if q := r.Quarantine; q == nil || !q.CheckoutMoved || !q.AdminMoved {
		t.Errorf("quarantine %+v", q)
	}
}

// Outside a repository, both are still one object.
func TestRemoveJSONFailedBeforeTheRepository(t *testing.T) {
	var out bytes.Buffer
	RemoveFailedJSON(&out, true, ErrNotInRepo)
	validateJSON(t, "remove-plan", out.Bytes())
	out.Reset()
	RemoveFailedJSON(&out, false, errors.New("no repo"))
	if r := decodeRemoveOutput(t, out.Bytes()); r.Outcome != RemoveRefused || r.Error == nil {
		t.Errorf("%+v", r)
	}
}

// A sequencer left without its HEAD marker is an operation too, under a name
// the schema knows.
func TestRemovePlanJSONNamesALeftoverSequencer(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/sequencer")
	mustMkdir(t, filepath.Join(gitDirFor(t, path), "sequencer"))
	p, err := removePlanJSON(t, ctx, path, RemoveOptions{})
	if err == nil || deref(p.Operation) != "sequencer" || !slices.Contains(problemCodes(p.Problems), KeptOperation) {
		t.Errorf("%v %+v", err, p)
	}
}

// A quarantine that stopped after it locked the worktree has changed
// something: it is partial, and wt restore undoes it.
func TestRemoveJSONQuarantineStoppedAfterTheLockIsPartial(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/json-pin")
	dir := trashFor(t, ctx)
	crashAt(t, quarantine.StepPin+":"+quarantine.Running)
	r, err := removeJSON(t, ctx, path, RemoveOptions{Quarantine: dir})
	if err == nil || r.Outcome != RemovePartial {
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
	ctx, path := safetyWorktree(t, "fix/trunk-source")
	p, err := removePlanJSON(t, ctx, path, RemoveOptions{Agents: []wtsync.Agent{}})
	if err != nil || p.TrunkSource == nil || *p.TrunkSource != string(ctx.Config.TrunkSource) || *p.TrunkSource == "" {
		t.Fatalf("plan trunkSource = %v (%v)", p.TrunkSource, err)
	}
	r, err := removeJSON(t, ctx, path, RemoveOptions{Agents: []wtsync.Agent{}, Expect: deref(p.Token)})
	if err != nil || r.TrunkSource == nil || *r.TrunkSource != *p.TrunkSource {
		t.Fatalf("result trunkSource = %v (%v)", r.TrunkSource, err)
	}
}
