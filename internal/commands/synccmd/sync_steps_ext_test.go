package synccmd_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/anlindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anlindstrom/wt/internal/commands/synccmd"
	"github.com/anlindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
	"github.com/anlindstrom/wt/internal/commands/testkit"
)

const dockerStep = "defer:\n  - run: cp v.txt gen.txt\n    paths: [v.txt]\n    commit: \"chore: regen\"\n    needs: [docker]\n"

func TestSyncRunRefusesBeforeAnythingMovesWhenAStepsNeedDoesNotAnswer(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	synccmdtest.DeclareSteps(t, ctx, dockerStep)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	probe := synccmdtest.DockerDown()
	opts := synccmdtest.NoAgents()
	opts.Probes = probe.Probes()
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out)
	if err == nil {
		t.Fatalf("the run went on without docker:\n%s", out.String())
	}
	for _, want := range []string{"cp v.txt gen.txt", "needs docker", "cannot connect to the Docker daemon", "--allow-owed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if probe.Calls() != 1 {
		t.Errorf("docker probed %d times, want once", probe.Calls())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("the branch moved")
	}
	if refs := testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

func TestSyncRunWithAllowOwedRebasesAndOwesTheStepThatNeedsWhatIsMissing(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	synccmdtest.DeclareSteps(t, ctx, dockerStep)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	opts := synccmdtest.NoAgents()
	opts.Probes = synccmdtest.DockerDown().Probes()
	opts.AllowOwed = true
	res, err := synccmdtest.SyncRunJSON(t, ctx, []string{"bump"}, opts)
	if err == nil || !strings.Contains(err.Error(), "owed") {
		t.Fatalf("err %v, want the step owed", err)
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") == old {
		t.Fatal("not rebased")
	}
	if testkit.GitOut(t, bump, "log", "-1", "--format=%s") == "chore: regen" {
		t.Fatal("the step that needs docker ran")
	}
	p := res.Worktrees[0]
	if p.Result != syncrun.ResultRebasedStepFailed || !slices.Contains(p.FailedSteps, "cp v.txt gen.txt") {
		t.Fatalf("participant %+v", p.UpParticipant)
	}
	if len(p.Deferred) != 1 || p.Deferred[0].Result != syncrun.DeferredOwed {
		t.Fatalf("deferred %+v", p.Deferred)
	}
}

func TestSyncRunDoesNotProbeANeedNoStepOfTheRunHas(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	synccmdtest.DeclareSteps(t, ctx, "defer:\n  - run: exit 1\n    paths: [nowhere/**]\n    needs: [docker]\n")
	probe := synccmdtest.DockerDown()
	opts := synccmdtest.NoAgents()
	opts.Probes = probe.Probes()
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if probe.Calls() != 0 {
		t.Fatalf("docker probed %d times for a step whose paths the rebase does not touch", probe.Calls())
	}
	_ = bump
}

// The parent of a stack whose check fails is put back, and its child is not
// rebased at all.
func TestSyncRunPutsBackAStackParentWhoseCheckFails(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	child := provisiontest.StackFixture(t, ctx)
	// After the rebase the parent's commit carries 1.0.6, which the check
	// refuses; the child's own commit touches no v.txt.
	synccmdtest.DeclareSteps(t, ctx, "each_commit:\n  - run: \"! grep -q 1.0.6 v.txt\"\n    paths: [v.txt]\n")
	oldBump, oldChild := testkit.GitOut(t, bump, "rev-parse", "HEAD"), testkit.GitOut(t, child, "rev-parse", "HEAD")
	res, err := synccmdtest.SyncRunJSON(t, ctx, []string{"child"}, synccmdtest.NoAgents())
	if err == nil {
		t.Fatal("the run succeeded over a failing check")
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != oldBump || testkit.GitOut(t, child, "rev-parse", "HEAD") != oldChild {
		t.Fatal("a stack member moved")
	}
	byWork := map[string]*syncrun.SyncParticipant{}
	for _, p := range res.Worktrees {
		byWork[p.Work] = p
	}
	parent := byWork["bump"]
	if parent == nil || parent.Result != syncrun.ResultRestored || parent.CheckFailed == nil || parent.CheckFailed.Step != "! grep -q 1.0.6 v.txt" {
		t.Fatalf("parent %+v", parent)
	}
	if c := byWork["child"]; c == nil || c.Result != syncrun.ResultNotRun {
		t.Fatalf("child %+v", c)
	}
}

func TestSyncRunReportsTheCommitsItChecked(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, _ := provisiontest.RunFixture(t, false)
	synccmdtest.DeclareSteps(t, ctx, "each_commit:\n  - run: \"true\"\n    paths: [v.txt]\n")
	res, err := synccmdtest.SyncRunJSON(t, ctx, []string{"bump"}, synccmdtest.NoAgents())
	if err != nil {
		t.Fatal(err)
	}
	p := res.Worktrees[0]
	if p.Result != syncrun.ResultRebased || p.Checked != 1 || p.CheckFailed != nil {
		t.Fatalf("participant %+v checked %d", p.UpParticipant, p.Checked)
	}
}

// The overview says which commits a run would check, runs nothing, and
// reports the probe of a need a step that would run has.
func TestSyncOverviewSaysWhatARunWouldCheckAndProbesItsNeeds(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	synccmdtest.DeclareSteps(t, ctx, "each_commit:\n  - run: \"touch ran && false\"\n    paths: [v.txt]\n    needs: [docker]\n")
	probe := synccmdtest.DockerDown()
	o := synccmdtest.OverviewJSONWith(t, ctx, synccmd.SyncOptions{NoFetch: true, Probes: probe.Probes()})
	if len(o.EachCommitDeclared) != 1 || !slices.Equal(o.EachCommitDeclared[0].Needs, []string{"docker"}) {
		t.Fatalf("each_commit declared %+v", o.EachCommitDeclared)
	}
	if len(o.Needs) != 1 || o.Needs[0].Need != "docker" || !o.Needs[0].Probed || o.Needs[0].OK || o.Needs[0].Error == nil {
		t.Fatalf("needs %+v", o.Needs)
	}
	row := synccmdtest.OverviewRow(t, o, "bump")
	if row.Checks != 1 || len(row.CheckCommits) != 1 || row.CheckCommits[0].Subject != "bump" {
		t.Fatalf("row checks %d %+v", row.Checks, row.CheckCommits)
	}
	if testkit.Exists(bump + "/ran") {
		t.Fatal("the simulation ran the check")
	}
	var human bytes.Buffer
	if err := synccmd.Sync(ctx, synccmd.SyncOptions{NoFetch: true, Probes: probe.Probes()}, &human); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "checks 1 commit") {
		t.Fatalf("overview does not say what a run checks:\n%s", human.String())
	}
}

func TestSyncOverviewProbesNothingWhenNoStepWouldRun(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, _ := provisiontest.RunFixture(t, false)
	synccmdtest.DeclareSteps(t, ctx, "defer:\n  - run: exit 1\n    paths: [nowhere/**]\n    needs: [docker]\n")
	probe := synccmdtest.DockerDown()
	o := synccmdtest.OverviewJSONWith(t, ctx, synccmd.SyncOptions{NoFetch: true, Probes: probe.Probes()})
	if probe.Calls() != 0 {
		t.Fatalf("probed %d times", probe.Calls())
	}
	if len(o.Needs) != 1 || o.Needs[0].Probed {
		t.Fatalf("needs %+v", o.Needs)
	}
	if len(o.Worktrees) == 0 || !slices.Equal(o.Worktrees[0].DeferredDeclared[0].Needs, []string{"docker"}) {
		t.Fatalf("deferred declared %+v", o.Worktrees)
	}
}

// declareOnOrigin has origin's trunk declare yaml as its .wt-sync.yaml.
func declareOnOrigin(t *testing.T, f *synccmdtest.TrunkFixture, yaml string) {
	t.Helper()
	testkit.GitIn(t, f.Other, "pull", "-q", "--ff-only")
	testkit.WriteIn(t, f.Other, ".wt-sync.yaml", yaml)
	testkit.GitIn(t, f.Other, "add", "-A")
	testkit.GitIn(t, f.Other, "commit", "-q", "-m", "declare")
	testkit.GitIn(t, f.Other, "push", "-q", "origin", "main")
}

// A branch behind its own remote is fast-forwarded before its rebase, so a
// commit only the remote has is replayed too: a step that needs something
// for that commit is probed, and refuses the run before the fast-forward.
func TestSyncRunProbesTheNeedsOfACommitOnlyItsRemoteHas(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	declareOnOrigin(t, f, "each_commit:\n  - run: \"true\"\n    paths: [t.txt]\n    needs: [docker]\n")
	f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.AdvanceOrigin(t, 1)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")

	o := synccmdtest.OverviewJSONWith(t, f.Ctx, synccmd.SyncOptions{Probes: synccmdtest.DockerDown().Probes()})
	row := synccmdtest.OverviewRow(t, o, "x")
	if row.Checks != 1 || len(o.Needs) != 1 || !o.Needs[0].Probed {
		t.Fatalf("overview checks %d needs %+v", row.Checks, o.Needs)
	}

	probe := synccmdtest.DockerDown()
	opts.Probes = probe.Probes()
	var out bytes.Buffer
	err := synccmd.SyncRebase(f.Ctx, []string{"x"}, opts, &out)
	if err == nil || !strings.Contains(out.String(), "needs docker") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if probe.Calls() != 1 {
		t.Fatalf("docker probed %d times", probe.Calls())
	}
	if testkit.GitOut(t, x, "rev-parse", "HEAD") != before {
		t.Fatal("the branch moved")
	}
	if refs := testkit.GitOut(t, f.Main, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}
