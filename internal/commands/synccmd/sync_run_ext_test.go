package synccmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// declareScript hands v.txt to a script on trunk instead of the owned line.
// A script can only be asked whether it claims a path, never for the bytes,
// so the simulation truncates its replay at that stop and cannot see what
// comes after it: triage reports recipe? and the run proceeds. That is the
// one way left for a run to discover a later unclaimed stop for itself, so
// it is what the restore tests below are built on.
func declareScript(t *testing.T, ctx *kit.Context) {
	t.Helper()
	main := ctx.Repo.MainRoot
	dir := filepath.Join(main, "bin", "conflict")
	body := "#!/bin/sh\ncase \"$1\" in\n--check) exit 0 ;;\n--resolve) git show \":3:$2\" > \"$2\" && git add -- \"$2\" ;;\n*) exit 1 ;;\nesac\n"
	gittest.WriteExecutable(t, filepath.Join(dir, "vbump"), body)
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: script\n    run: bin/conflict/vbump\n"
	if err := os.WriteFile(filepath.Join(main, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitOut(t, main, "add", "-A")
	testkit.GitOut(t, main, "commit", "-q", "-m", "declare a script for v.txt")
	testkit.GitOut(t, main, "fetch", "-q", "origin")
}

func TestSyncRunRebasesARecipeWorktreeAndRunsTheDeferredStep(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, true)
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"wt sync rebase  onto origin/main", "(as last fetched ", "stop 1/1", "✓ v.txt owned-line", "✓ rebased 1 commit", "committed 1 file", "as ", "↩ wt sync undo bump puts it back", "push: git -C"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if testkit.GitOut(t, bump, "log", "-1", "--format=%s") != "chore: regen" {
		t.Fatal("no regeneration commit")
	}
	if got, _ := os.ReadFile(filepath.Join(bump, "gen.txt")); string(got) != "1.0.6\n" {
		t.Fatalf("gen.txt %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(testkit.GitOut(t, bump, "rev-parse", "--absolute-git-dir"), wtsync.LockName+"*")); len(left) != 0 {
		t.Fatalf("lock left behind: %v", left)
	}
}

func TestSyncRunRefusesADirtyWorktreeAndTouchesNothing(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out)
	if err == nil || !strings.Contains(out.String(), "tracked changes") {
		t.Fatalf("err %v out %s", err, out.String())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved")
	}
	if refs := testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

func TestSyncRunRefusesAWorktreeWithAnAgent(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	resolved, _ := filepath.EvalSymlinks(bump)
	opts := synccmdtest.NoAgents()
	opts.Agents = []wtsync.Agent{{Name: "bump-1", Cwd: resolved}}
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(out.String(), "bump-1") {
		t.Fatalf("err %v out %s", err, out.String())
	}
}

func TestSyncRunRefusesWhenAgentsCannotBeListed(t *testing.T) {
	// Not parallel: puts a fake claude on the process-wide PATH.
	// Agents nil means "ask claude agents"; make that fail by putting a
	// claude on the PATH that exits non-zero. The real PATH stays in front
	// of nothing and behind the stub, so git is still reachable — emptying
	// the PATH outright would break every git call instead.
	ctx, bump := provisiontest.RunFixture(t, false)
	stub := t.TempDir()
	gittest.WriteExecutable(t, filepath.Join(stub, "claude"), "#!/bin/sh\necho boom >&2\nexit 1\n")
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	opts := synccmdtest.NoAgents()
	opts.Agents = nil
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "agent sessions") {
		t.Fatalf("err %v", err)
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved")
	}
}

func TestSyncRunSkipsACurrentWorktreeWithoutASafetyRef(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, _ := provisiontest.RunFixture(t, false)
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"other"}, synccmdtest.NoAgents(), &out); err != nil {
		t.Fatalf("a skip is not an error: %v", err)
	}
	if !strings.Contains(out.String(), "skipped:") {
		t.Fatalf("out %s", out.String())
	}
	if refs := testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

func TestSyncRunRefusesWhenTrunkDeclaresNothing(t *testing.T) {
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	testkit.GitIn(t, main, "remote", "add", "origin", main)
	testkit.GitIn(t, main, "fetch", "-q", "origin")
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := provision.New(ctx, "feat/bump", provision.NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "declares no") {
		t.Fatalf("err %v", err)
	}
}

func TestSyncRunRebasesAStackParentFirstAndChildOntoTheParentsFinalTip(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, true) // with the deferred commit, so the parent's tip moves after its rebase
	child := provisiontest.StackFixture(t, ctx)
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"child"}, synccmdtest.NoAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "is a stack with") {
		t.Fatalf("out %s", out.String())
	}
	parentTip := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	if testkit.GitOut(t, bump, "log", "-1", "--format=%s") != "chore: regen" {
		t.Fatal("parent has no regeneration commit")
	}
	// The child's own commit sits directly on the parent's final tip (the
	// child's deferred step does not fire: its rebase changed no v.txt of its own
	// relative to its parent, and gen.txt is already regenerated there).
	if testkit.GitOut(t, child, "rev-parse", "HEAD~1") != parentTip {
		t.Fatalf("child not on the parent's final tip:\n%s", out.String())
	}
	if !testkit.GitAncestor(t, ctx.Repo.MainRoot, "origin/main", "feat_wt/bump") {
		t.Fatal("parent not on trunk")
	}
	refs := testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "--format=%(refname)", "refs/wt-sync/")
	if !strings.Contains(refs, "feat_wt/child/99") || !strings.Contains(refs, "feat_wt/bump/99") {
		t.Fatalf("safety refs %q", refs)
	}
}

func TestSyncRunARefusedStackMemberDefersTheWholeStack(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	child := provisiontest.StackFixture(t, ctx)
	if err := os.WriteFile(filepath.Join(child, "c.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldBump, oldChild := testkit.GitOut(t, bump, "rev-parse", "HEAD"), testkit.GitOut(t, child, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out)
	if err == nil || !strings.Contains(out.String(), "child: tracked changes") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != oldBump || testkit.GitOut(t, child, "rev-parse", "HEAD") != oldChild {
		t.Fatal("a stack member moved")
	}
	if refs := testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

// An empty worktree on an older trunk commit is in the history of every
// branch cut after it, yet nothing is built on it: naming bump syncs bump
// alone, and an agent in the empty worktree does not hold it back.
func TestSyncRunIgnoresAnEmptyWorktreeBehindTrunk(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, _ := provisiontest.RunFixture(t, false)
	var buf bytes.Buffer
	stale, err := provision.New(ctx, "feat/stale", provision.NewOptions{NoSetup: true, Base: "main~1"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(stale)
	old := testkit.GitOut(t, stale, "rev-parse", "HEAD")
	opts := synccmdtest.NoAgents()
	opts.Agents = []wtsync.Agent{{Name: "stale-1", Cwd: resolved}}
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if s := out.String(); strings.Contains(s, "is a stack with") || strings.Contains(s, "stale-1") {
		t.Fatalf("stale was pulled in:\n%s", s)
	}
	if !testkit.GitAncestor(t, ctx.Repo.MainRoot, "origin/main", "feat_wt/bump") {
		t.Fatalf("bump not rebased:\n%s", out.String())
	}
	if testkit.GitOut(t, stale, "rev-parse", "HEAD") != old {
		t.Fatal("stale moved")
	}
}

func TestSyncRunAsksOnceForMoreThanOneWorktreeAndStopsOnNo(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	provisiontest.StackFixture(t, ctx)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	var asked []string
	opts := synccmdtest.NoAgents()
	opts.Confirm = func(works []string) (bool, error) { asked = works; return false, nil }
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("a declined confirmation is not an error: %v", err)
	}
	if len(asked) != 2 || !strings.Contains(out.String(), "nothing rebased") {
		t.Fatalf("asked %v out %s", asked, out.String())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved after no")
	}
	// --yes, and a run with no terminal, arrive here as a nil Confirm.
	opts.Confirm = nil
	asked = nil
	out.Reset()
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if asked != nil {
		t.Fatal("asked with nobody to ask")
	}
}

// With nothing named the run takes the ready group and nothing else, says
// what it leaves alone in the overview's words, and asks even for one
// worktree, since nobody named it.
func TestSyncRunWithNothingNamedTakesEveryReadyWorktreeAndAsksFirst(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	alpha := provisiontest.ContestedSibling(t, ctx)
	oldBump := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	oldAlpha := testkit.GitOut(t, alpha, "rev-parse", "HEAD")
	var asked []string
	opts := synccmdtest.NoAgents()
	opts.Confirm = func(works []string) (bool, error) { asked = works; return false, nil }
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, nil, opts, &out); err != nil {
		t.Fatalf("a declined confirmation is not an error: %v", err)
	}
	s := out.String()
	if len(asked) != 1 || asked[0] != "bump" {
		t.Fatalf("asked %v, want only the ready worktree\n%s", asked, s)
	}
	// other is behind trunk with nothing ahead: stale, and said so, as the
	// overview says it under skipped.
	for _, want := range []string{"every ready worktree: bump\n", "left as they are: alpha contested · other stale\n", "nothing rebased"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != oldBump {
		t.Fatal("HEAD moved after no")
	}
	opts.Confirm = nil
	out.Reset()
	if err := synccmd.SyncRebase(ctx, nil, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "rebased 1 commit") {
		t.Fatalf("bump was not rebased:\n%s", out.String())
	}
	if testkit.GitOut(t, alpha, "rev-parse", "HEAD") != oldAlpha {
		t.Fatal("the contested worktree moved")
	}
	if busy, err := wtsync.RebaseInProgress(alpha); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; the contested worktree was touched", busy, err)
	}
}

// recipe? is not ready: a script owns a path, so the replay could not be
// carried past it and a run may stop later. With nothing named the run
// leaves it alone rather than risk a handover nobody asked for.
func TestSyncRunWithNothingNamedLeavesRecipeQuestionAlone(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	declareScript(t, ctx)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, nil, synccmdtest.NoAgents(), &out); err != nil {
		t.Fatalf("nothing to do is not an error: %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"nothing is ready to rebase\n", "left as they are: bump recipe? · other stale\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved")
	}
}

// A ready worktree is only taken with its whole stack. A member held back
// would refuse every one of them at triage, after the run said it was left
// alone, so the stack is left alone up front and the reason names the member.
func TestSyncRunWithNothingNamedLeavesAStackWithAHeldMember(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	child := provisiontest.StackFixture(t, ctx)
	if err := os.WriteFile(filepath.Join(bump, "a.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldChild := testkit.GitOut(t, child, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, nil, synccmdtest.NoAgents(), &out); err != nil {
		t.Fatalf("nothing taken is not an error: %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"nothing is ready to rebase\n", "bump recipe, dirty · child ", ", stacked with bump\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "is a stack with") || strings.Contains(s, "refused") {
		t.Errorf("the stack was pulled in after being left alone:\n%s", s)
	}
	if testkit.GitOut(t, child, "rev-parse", "HEAD") != oldChild {
		t.Fatal("the child moved")
	}
}

// A ready worktree with a session idle in it is taken, and the session is
// named in the question, as when it is named by hand.
func TestSyncRunWithNothingNamedNamesAnIdleSessionAndAsks(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	opts := synccmdtest.NoAgents()
	opts.Agents = testkit.IdleIn(t, bump, "parked")
	var asked []string
	opts.Confirm = func(works []string) (bool, error) { asked = works; return false, nil }
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, nil, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	if len(asked) != 1 || asked[0] != "bump" || !strings.Contains(s, "every ready worktree: bump\n") || !strings.Contains(s, "parked") {
		t.Fatalf("asked %v\n%s", asked, s)
	}
}

// What holds a worktree back is named beside its class, as the overview
// names it.
func TestSyncRunWithNothingNamedNamesWhatHoldsAWorktree(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	if err := os.WriteFile(filepath.Join(bump, "a.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, nil, synccmdtest.NoAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	// Nothing moved on trunk after other was cut, so other is current and
	// not worth a line.
	if !strings.Contains(out.String(), "left as they are: bump recipe, dirty\n") {
		t.Fatalf("out:\n%s", out.String())
	}
}

// A contested stop is no longer refused: the run rebases up to it, stages
// what the strategies resolved, leaves the rebase in place and writes the
// handover.
func TestSyncRunHandsAContestedStopOver(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.ContestedFixture(t)
	branch := "feat_wt/bump"
	old := testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", branch)
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out)
	if err == nil {
		t.Fatalf("err = nil, want the run reported as not completed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "contested at 1/1: the run stops there and writes a plan") {
		t.Fatalf("output does not say what is coming:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "needs you") {
		t.Fatalf("output has no needs-you line:\n%s", out.String())
	}
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := os.ReadFile(wtsync.PlanPath(gitDir))
	if err != nil {
		t.Fatalf("no plan file: %v", err)
	}
	for _, want := range []string{"## already resolved", "## yours", "wt sync resume"} {
		if !strings.Contains(string(plan), want) {
			t.Fatalf("plan is missing %q:\n%s", want, plan)
		}
	}
	st, ok, err := wtsync.ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	if st.Epoch == 0 || st.Safety == "" || len(st.Resolved) == 0 || st.Lock.PID == 0 {
		t.Fatalf("state = %+v", st)
	}
	if st.Strategy["v.txt"] != "owned-line" || len(st.Left) != 1 || st.Left[0] != "a.txt" {
		t.Fatalf("state = %+v", st)
	}
	if _, ok, err := wtsync.ReadLock(gitDir); err != nil || !ok {
		t.Fatalf("ReadLock = %v, %v; want the lock kept", ok, err)
	}
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; want the rebase left in place", busy, err)
	}
	if got := testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", branch); got != old {
		t.Fatal("the branch moved before the rebase finished")
	}
	// No result ref: the run did not finish for this branch.
	if _, ok, err := wtsync.ResultTip(ctx.Repo.MainRoot, branch, st.Epoch); err != nil || ok {
		t.Fatalf("ResultTip = %v, %v; a handed-over run pins no result", ok, err)
	}
}

// The needs-you line has to say what to do: resolve what is yours, git add
// it, then resume, or undo; and name the plan. The handover test read the
// old line as naming the file and resume and nothing else, with a wt:
// prefix inside the indented line that the closing line also carries.
func TestSyncRunNeedsYouLineSaysResolveAddResumeOrUndo(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.ContestedFixture(t)
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out)
	if err == nil || err.Error() != "not completed: bump (needs you)" {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	gitDir, gerr := wtsync.GitDir(bump)
	if gerr != nil {
		t.Fatal(gerr)
	}
	s := out.String()
	want := "\n  ⚠ bump needs you. 1 left after resolvers: a.txt · " + wtsync.WayOut(wtsync.Way{Work: "bump", Plan: true, Rebasing: true, OwesAdd: true}) +
		"\n    plan " + wtsync.PlanPath(gitDir) + "\n"
	if !strings.Contains(s, want) {
		t.Fatalf("want %q in:\n%s", want, s)
	}
	if strings.Contains(s, "⚠ wt:") {
		t.Fatalf("the wt: prefix is inside the indented line:\n%s", s)
	}
}

// A second run refuses a worktree waiting on a person, and leaves both the
// rebase and the handover exactly as the first run left them.
func TestSyncRunRefusesAWorktreeWaitingOnAPerson(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.ContestedFixture(t)
	var first bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &first); err == nil {
		t.Fatalf("the first run should have handed over:\n%s", first.String())
	}
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(wtsync.PlanPath(gitDir))
	if err != nil {
		t.Fatal(err)
	}
	head := testkit.GitOut(t, bump, "rev-parse", "HEAD")

	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out); err == nil {
		t.Fatalf("a second run must not touch a worktree somebody is finishing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "refused:") || !strings.Contains(out.String(), "wt sync resume") {
		t.Fatalf("the refusal does not name wt sync resume:\n%s", out.String())
	}
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the second run disturbed the rebase", busy, err)
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != head {
		t.Fatal("the second run moved HEAD")
	}
	after, err := os.ReadFile(wtsync.PlanPath(gitDir))
	if err != nil || string(after) != string(before) {
		t.Fatalf("the plan file was rewritten or removed: %v", err)
	}
	if _, ok, err := wtsync.ReadState(gitDir); err != nil || !ok {
		t.Fatalf("ReadState = %v, %v; the sidecar is gone", ok, err)
	}
}

// A stack parent may not be left waiting: its children would be stranded on
// a base that is about to be rewritten, so its stop is put back instead.
func TestSyncRunPutsAStackParentBackInsteadOfHandingItOver(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.ContestedFixture(t)
	child := provisiontest.StackFixture(t, ctx)
	oldBump, oldChild := testkit.GitOut(t, bump, "rev-parse", "HEAD"), testkit.GitOut(t, child, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out)
	s := out.String()
	if err == nil {
		t.Fatalf("a restored branch is a failure:\n%s", s)
	}
	if !strings.Contains(s, "a stack parent cannot be left waiting") {
		t.Fatalf("output does not say why it will not hand over:\n%s", s)
	}
	if !strings.Contains(s, "restored: a.txt at 1/1") {
		t.Fatalf("out %s", s)
	}
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; the parent was left mid-rebase", busy, err)
	}
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	if has, err := wtsync.HasPlan(gitDir); err != nil || has {
		t.Fatalf("HasPlan = %v, %v; a restored run hands nothing over", has, err)
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != oldBump || testkit.GitOut(t, child, "rev-parse", "HEAD") != oldChild {
		t.Fatal("a stack member moved")
	}
}

func TestSyncRunHandsOverALaterUnclaimedStop(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	// The first stop is a script's (v.txt), which truncates the simulation
	// there, so triage says recipe? and the run starts; the branch's second
	// commit conflicts on a.txt, which nobody claims. Without the script the
	// simulation would replay to that second stop itself and triage would
	// call it contested - which is the point of doing it this way round:
	// this pins what happens when a run, not triage, is the one that finds
	// the unclaimed stop.
	ctx, bump := provisiontest.RunFixture(t, false)
	declareScript(t, ctx)
	main := ctx.Repo.MainRoot
	if err := os.WriteFile(filepath.Join(bump, "a.txt"), []byte("branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "-m", "a on branch")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitOut(t, main, "add", "-A")
	testkit.GitOut(t, main, "commit", "-q", "-m", "a on trunk")
	testkit.GitOut(t, main, "fetch", "-q", "origin")
	old := testkit.GitOut(t, main, "rev-parse", "feat_wt/bump")
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out)
	if err == nil || !strings.Contains(out.String(), "needs you") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if testkit.GitOut(t, main, "rev-parse", "feat_wt/bump") != old {
		t.Fatal("the branch moved before the rebase finished")
	}
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; want the rebase left in place", busy, err)
	}
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	st, ok, err := wtsync.ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v", ok, err)
	}
	if st.Stop != 2 || st.Total != 2 || len(st.Left) != 1 || st.Left[0] != "a.txt" {
		t.Fatalf("state = %+v", st)
	}
	if !strings.Contains(testkit.GitOut(t, main, "for-each-ref", "--format=%(refname)", "refs/wt-sync/"), "feat_wt/bump/99") {
		t.Fatal("the safety ref, the undo target, is missing")
	}
}

func TestSyncRunAHandedOverChildDoesNotStopItsSibling(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	// As above: the script truncates the simulation at v.txt, so triage
	// cannot foresee the child's unclaimed a.txt and the run reaches it. A
	// leaf has no descendants, so its stop is handed over, not put back.
	declareScript(t, ctx)
	conflicted, clean := provisiontest.TwoChildFixture(t, ctx, ctx.Repo.MainRoot)
	main := ctx.Repo.MainRoot
	oldConflicted := testkit.GitOut(t, main, "rev-parse", "feat_wt/alpha")
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out)
	s := out.String()
	if err == nil {
		t.Fatalf("a handed-over branch is a failure:\n%s", s)
	}
	if !strings.Contains(s, "needs you") {
		t.Fatalf("out %s", s)
	}
	// The sibling rebased onto the parent's tip; the handed-over one did not
	// move: its rebase is still in flight.
	parentTip := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	if testkit.GitOut(t, clean, "rev-parse", "HEAD~1") != parentTip {
		t.Fatalf("the sibling was not rebased onto the parent:\n%s", s)
	}
	if testkit.GitOut(t, main, "rev-parse", "feat_wt/alpha") != oldConflicted {
		t.Fatal("the handed-over branch moved")
	}
	if busy, berr := wtsync.RebaseInProgress(conflicted); berr != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; want the rebase left in place", busy, berr)
	}
	if strings.Contains(s, "alpha is waiting for you") {
		t.Fatalf("the sibling was poisoned:\n%s", s)
	}
	// The closing error names the handed-over branch and nothing else.
	if !strings.Contains(err.Error(), "alpha (needs you)") || strings.Contains(err.Error(), "zulu") {
		t.Fatalf("err %v", err)
	}
}

func TestSyncRunStillRefusesASessionThatIsNotIdle(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	resolved, _ := filepath.EvalSymlinks(bump)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	for _, a := range []wtsync.Agent{
		{Name: "bump-1", Cwd: resolved, Kind: "interactive", Status: "busy"},
		{Name: "bump-1", Cwd: resolved, Kind: "background", State: "blocked", Status: "idle"},
	} {
		opts := synccmdtest.NoAgents()
		opts.Agents = []wtsync.Agent{a}
		var out bytes.Buffer
		if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err == nil || !strings.Contains(out.String(), "busy in it: bump-1") {
			t.Fatalf("%+v: err %v\n%s", a, err, out.String())
		}
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved under a busy session")
	}
}

func TestSyncRunNamesAnIdleSessionInTheQuestionAndStopsOnNo(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	opts := synccmdtest.NoAgents()
	opts.Agents = testkit.IdleIn(t, bump, "bump-1")
	var asked []string
	opts.Confirm = func(works []string) (bool, error) { asked = works; return false, nil }
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if len(asked) != 1 || asked[0] != "bump" {
		t.Fatalf("asked %v; one worktree under an idle session is asked about", asked)
	}
	if s := out.String(); !strings.Contains(s, "⚠ bump: session bump-1 (idle) is in it") || !strings.Contains(s, "nothing rebased") {
		t.Fatalf("out:\n%s", s)
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved after no")
	}
}

func TestSyncRunUnderAnIdleSessionWithNoTerminalSaysSoAndEndsWithALineToRelay(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	opts := synccmdtest.NoAgents()
	opts.Agents = testkit.IdleIn(t, bump, "bump-1")
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	notice := strings.Index(s, "session bump-1 (idle) is in it")
	rebased := strings.Index(s, "✓ rebased")
	if notice < 0 || rebased < 0 || notice > rebased {
		t.Fatalf("the notice must come before anything moves:\n%s", s)
	}
	if !strings.Contains(s, "⚠ tell bump-1, idle in it:\n    wt: bump rebased on main (+1). yours to check: v.txt\n") {
		t.Fatalf("no relay line:\n%s", s)
	}
}

// Between triage and the lock a session can wake up, or a new one open. The
// lock-time re-check lists them again and refuses either.
func TestSyncRunRefusesASessionThatChangedSinceTriage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	woke := testkit.IdleIn(t, bump, "bump-1")
	woke[0].Status = "busy"
	for _, tc := range []struct {
		name   string
		before []wtsync.Agent
		then   []wtsync.Agent
		want   string
	}{
		{"became busy", testkit.IdleIn(t, bump, "bump-1"), woke, "busy in it now: bump-1"},
		{"arrived", testkit.IdleIn(t, bump, "bump-1"), append(testkit.IdleIn(t, bump, "bump-1"), testkit.IdleIn(t, bump, "bump-2")...), "arrived since it was checked: bump-2 (idle)"},
		{"arrived in an empty worktree", []wtsync.Agent{}, testkit.IdleIn(t, bump, "bump-3"), "arrived since it was checked: bump-3 (idle)"},
	} {
		opts := synccmdtest.NoAgents()
		opts.Agents = tc.before
		then := tc.then
		opts.Relist = func() ([]wtsync.Agent, error) { return then, nil }
		var out bytes.Buffer
		if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err == nil || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%s: err %v\n%s", tc.name, err, out.String())
		}
		if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
			t.Fatalf("%s: HEAD moved", tc.name)
		}
	}
}

// A run holds each worktree's lock for as long as it is working in it and
// no longer: a refused worktree's lock goes before the first rebase, and a
// finished worktree's goes before the next worktree is rebased, so a run
// over a fleet does not hold every worktree against another wt for its whole
// length. A deferred step that waits to be let go is where the test looks.
func TestSyncRunReleasesEachLockAsSoonAsItIsDoneWithTheWorktree(t *testing.T) {
	// Not parallel: waits 10s for the run to reach a step, which parallel load outlasted.
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	main := ctx.Repo.MainRoot
	var buf bytes.Buffer
	ahead := func(spec, base, file string) string {
		t.Helper()
		wt, err := provision.New(ctx, spec, provision.NewOptions{NoSetup: true, Base: base}, &buf)
		if err != nil {
			t.Fatal(err)
		}
		testkit.WriteFile(t, wt, file, file+"\n")
		testkit.GitOut(t, wt, "add", "-A")
		testkit.GitOut(t, wt, "commit", "-q", "-m", file)
		return wt
	}
	second := ahead("feat/second", "", "s.txt")
	leaf := ahead("feat/leaf", "feat_wt/second", "l.txt")
	fourth := ahead("feat/fourth", "", "f.txt")
	// The step marks that it started and waits for the test to let it go,
	// for every rebased worktree.
	marks, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n" +
		"defer:\n  - run: touch " + marks + "/started-$(basename \"$(pwd)\"); while [ ! -f " + marks + "/go-$(basename \"$(pwd)\") ]; do sleep 0.05; done\n"
	testkit.WriteFile(t, main, ".wt-sync.yaml", yaml)
	testkit.GitOut(t, main, "commit", "-q", "-am", "declare a step that waits")
	testkit.GitOut(t, main, "fetch", "-q", "origin")
	// Somebody else holds leaf: its stack, second and leaf, is refused at
	// the lock, after second's own lock was taken.
	leafGitDir, err := wtsync.GitDir(leaf)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := wtsync.Acquire(leafGitDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foreign.Release() })
	locked := func(wt string) bool {
		t.Helper()
		gitDir, err := wtsync.GitDir(wt)
		if err != nil {
			t.Fatal(err)
		}
		_, ok, err := wtsync.ReadLock(gitDir)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	waitFor := func(name string) {
		t.Helper()
		for i := 0; i < 200; i++ {
			if _, err := os.Stat(filepath.Join(marks, name)); err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s never appeared", name)
	}
	letGo := func(work string) {
		t.Helper()
		testkit.WriteFile(t, marks, "go-"+work, "")
	}

	done := make(chan error, 1)
	var out bytes.Buffer
	go func() {
		done <- synccmd.SyncRebase(ctx, []string{"bump", "second", "fourth"}, synccmdtest.NoAgents(), &out)
	}()

	// bump is in its finish: the refused stack's lock has gone already, and
	// bump's own is held.
	waitFor("started-bump")
	if locked(second) {
		t.Error("second was refused at the lock and still holds its lock while bump is rebased")
	}
	if !locked(bump) {
		t.Error("bump is in its finish and does not hold its lock")
	}
	letGo("bump")
	// fourth is in its finish: bump is done, and its lock has gone.
	waitFor("started-fourth")
	if locked(bump) {
		t.Error("bump is finished and still holds its lock while fourth is rebased")
	}
	if !locked(fourth) {
		t.Error("fourth is in its finish and does not hold its lock")
	}
	letGo("fourth")

	select {
	case err := <-done:
		if err == nil || !strings.Contains(out.String(), "refused: leaf: locked") {
			t.Fatalf("err %v\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not finish")
	}
	for _, wt := range []string{bump, second, fourth} {
		if locked(wt) {
			t.Errorf("%s still locked after the run", wt)
		}
	}
	if !locked(leaf) {
		t.Error("the run removed somebody else's lock")
	}
}

// A worktree whose state can no longer be read between triage and the lock
// is refused for that, in git's own words: the check under the lock reads
// through wtsync, whose error form appends the exit status, and the run's
// line never carried it.
func TestSyncRunSaysWhyARecheckCouldNotBeMadeInGitsOwnWords(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	opts := synccmdtest.NoAgents()
	// The second listing is the last thing before the lock: the index goes
	// unreadable there, after triage read it and before the recheck does.
	opts.Relist = func() ([]wtsync.Agent, error) {
		if err := os.WriteFile(filepath.Join(gitDir, "index"), []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
		return []wtsync.Agent{}, nil
	}
	var out bytes.Buffer
	err = synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out)
	s := out.String()
	if err == nil || !strings.Contains(s, "refused: bump: changed since triage: could not check: fatal: ") {
		t.Fatalf("err %v\n%s", err, s)
	}
	if strings.Contains(s, "exit status") {
		t.Fatalf("the refusal quotes the exit status:\n%s", s)
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved")
	}
}
