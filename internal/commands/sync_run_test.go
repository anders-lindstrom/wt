package commands

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// runFixture: a main checkout that is its own origin; trunk declares v.txt
// owned-line max-plus-patch and, when withDefer, a deferred step that copies
// v.txt to the tracked gen.txt and commits it; worktree bump (feat_wt/bump)
// one commit ahead on v.txt; worktree other (feat_wt/other) current; trunk
// moved on v.txt; origin fetched.
func runFixture(t *testing.T, withDefer bool) (ctx *Context, bump string) {
	t.Helper()
	main := committedRepo(t, minimalConf)
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(main, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n"
	if withDefer {
		yaml += "defer:\n  - run: cp v.txt gen.txt\n    paths: [v.txt]\n    commit: \"chore: regen\"\n"
	}
	write(".wt-sync.yaml", yaml)
	write("v.txt", "1.0.0\n")
	write("gen.txt", "stale\n")
	write("a.txt", "a\n")
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-q", "-m", "declare")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	bump, err = New(ctx, "feat/bump", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("1.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bump, "commit", "-q", "-am", "bump")
	write("v.txt", "1.0.5\n")
	gitIn(t, main, "commit", "-q", "-am", "trunk bump")
	if _, err := New(ctx, "feat/other", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	return ctx, bump
}

// declareScript hands v.txt to a script on trunk instead of the owned line.
// A script can only be asked whether it claims a path, never for the bytes,
// so the simulation truncates its replay at that stop and cannot see what
// comes after it: triage reports recipe? and the run proceeds. That is the
// one way left for a run to discover a later unclaimed stop for itself, so
// it is what the restore tests below are built on.
func declareScript(t *testing.T, ctx *Context) {
	t.Helper()
	main := ctx.Repo.MainRoot
	dir := filepath.Join(main, "bin", "conflict")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\ncase \"$1\" in\n--check) exit 0 ;;\n--resolve) git show \":3:$2\" > \"$2\" && git add -- \"$2\" ;;\n*) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "vbump"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: script\n    run: bin/conflict/vbump\n"
	if err := os.WriteFile(filepath.Join(main, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, main, "add", "-A")
	gitOut(t, main, "commit", "-q", "-m", "declare a script for v.txt")
	gitOut(t, main, "fetch", "-q", "origin")
}

// contestedFixture is runFixture's repository with one more file in play:
// the branch's single commit also touches a.txt, which the declaration does
// not claim, and trunk moves a.txt too. Triage sees the whole stop, so the
// class is contested and the run walks into it knowingly.
func contestedFixture(t *testing.T) (ctx *Context, bump string) {
	t.Helper()
	ctx, bump = runFixture(t, false)
	main := ctx.Repo.MainRoot
	if err := os.WriteFile(filepath.Join(bump, "a.txt"), []byte("branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, bump, "add", "-A")
	gitOut(t, bump, "commit", "-q", "--amend", "--no-edit")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, main, "add", "-A")
	gitOut(t, main, "commit", "-q", "-m", "a on trunk")
	gitOut(t, main, "fetch", "-q", "origin")
	return ctx, bump
}

// gitAncestor reports whether a is an ancestor of b; exit 1 is a plain no.
func gitAncestor(t *testing.T, dir, a, b string) bool {
	t.Helper()
	cmd := exec.Command("git", "merge-base", "--is-ancestor", a, b)
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		return true
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false
	}
	t.Fatalf("merge-base --is-ancestor %s %s: %v", a, b, err)
	return false
}

func noAgents() RunOptions {
	return RunOptions{NoFetch: true, Agents: []wtsync.Agent{}, Now: func() time.Time { return time.Unix(0, 99) }}
}

func TestSyncRunRebasesARecipeWorktreeAndRunsTheDeferredStep(t *testing.T) {
	ctx, bump := runFixture(t, true)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"wt sync run  onto origin/main", "stop 1/1", "✓ v.txt owned-line", "✓ rebased 1 commit", "committed 1 file", "as ", "↩ wt sync undo bump puts it back", "push: git -C"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if gitOut(t, bump, "log", "-1", "--format=%s") != "chore: regen" {
		t.Fatal("no regeneration commit")
	}
	if got, _ := os.ReadFile(filepath.Join(bump, "gen.txt")); string(got) != "1.0.6\n" {
		t.Fatalf("gen.txt %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(gitOut(t, bump, "rev-parse", "--absolute-git-dir"), wtsync.LockName+"*")); len(left) != 0 {
		t.Fatalf("lock left behind: %v", left)
	}
}

func TestSyncRunRefusesADirtyWorktreeAndTouchesNothing(t *testing.T) {
	ctx, bump := runFixture(t, false)
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	if err == nil || !strings.Contains(out.String(), "tracked changes") {
		t.Fatalf("err %v out %s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved")
	}
	if refs := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

func TestSyncRunRefusesAWorktreeWithAnAgent(t *testing.T) {
	ctx, bump := runFixture(t, false)
	resolved, _ := filepath.EvalSymlinks(bump)
	opts := noAgents()
	opts.Agents = []wtsync.Agent{{Name: "bump-1", Cwd: resolved}}
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(out.String(), "bump-1") {
		t.Fatalf("err %v out %s", err, out.String())
	}
}

func TestSyncRunRefusesWhenAgentsCannotBeListed(t *testing.T) {
	// Agents nil means "ask claude agents"; make that fail by putting a
	// claude on the PATH that exits non-zero. The real PATH stays in front
	// of nothing and behind the stub, so git is still reachable — emptying
	// the PATH outright would break every git call instead.
	ctx, bump := runFixture(t, false)
	stub := t.TempDir()
	if err := os.WriteFile(filepath.Join(stub, "claude"), []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	opts := noAgents()
	opts.Agents = nil
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "agent sessions") {
		t.Fatalf("err %v", err)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved")
	}
}

func TestSyncRunSkipsACurrentWorktreeWithoutASafetyRef(t *testing.T) {
	ctx, _ := runFixture(t, false)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"other"}, noAgents(), &out); err != nil {
		t.Fatalf("a skip is not an error: %v", err)
	}
	if !strings.Contains(out.String(), "skipped:") {
		t.Fatalf("out %s", out.String())
	}
	if refs := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

func TestSyncRunRefusesWhenTrunkDeclaresNothing(t *testing.T) {
	main := committedRepo(t, minimalConf)
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := New(ctx, "feat/bump", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "declares no") {
		t.Fatalf("err %v", err)
	}
}

// stackFixture adds worktree child (feat_wt/child) on top of feat_wt/bump.
func stackFixture(t *testing.T, ctx *Context) (child string) {
	t.Helper()
	var buf bytes.Buffer
	child, err := New(ctx, "feat/child", NewOptions{NoSetup: true, Base: "feat_wt/bump"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, child, "add", "-A")
	gitOut(t, child, "commit", "-q", "-m", "child")
	return child
}

func TestSyncRunRebasesAStackParentFirstAndChildOntoTheParentsFinalTip(t *testing.T) {
	ctx, bump := runFixture(t, true) // with the deferred commit, so the parent's tip moves after its rebase
	child := stackFixture(t, ctx)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"child"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "is a stack with") {
		t.Fatalf("out %s", out.String())
	}
	parentTip := gitOut(t, bump, "rev-parse", "HEAD")
	if gitOut(t, bump, "log", "-1", "--format=%s") != "chore: regen" {
		t.Fatal("parent has no regeneration commit")
	}
	// The child's own commit sits directly on the parent's final tip (the
	// child's deferred step does not fire: its rebase changed no v.txt of its own
	// relative to its parent, and gen.txt is already regenerated there).
	if gitOut(t, child, "rev-parse", "HEAD~1") != parentTip {
		t.Fatalf("child not on the parent's final tip:\n%s", out.String())
	}
	if !gitAncestor(t, ctx.Repo.MainRoot, "origin/main", "feat_wt/bump") {
		t.Fatal("parent not on trunk")
	}
	refs := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "--format=%(refname)", "refs/wt-sync/")
	if !strings.Contains(refs, "feat_wt/child/99") || !strings.Contains(refs, "feat_wt/bump/99") {
		t.Fatalf("safety refs %q", refs)
	}
}

func TestSyncRunARefusedStackMemberDefersTheWholeStack(t *testing.T) {
	ctx, bump := runFixture(t, false)
	child := stackFixture(t, ctx)
	if err := os.WriteFile(filepath.Join(child, "c.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldBump, oldChild := gitOut(t, bump, "rev-parse", "HEAD"), gitOut(t, child, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	if err == nil || !strings.Contains(out.String(), "child: tracked changes") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != oldBump || gitOut(t, child, "rev-parse", "HEAD") != oldChild {
		t.Fatal("a stack member moved")
	}
	if refs := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

// An empty worktree on an older trunk commit is in the history of every
// branch cut after it, yet nothing is built on it: naming bump syncs bump
// alone, and an agent in the empty worktree does not hold it back.
func TestSyncRunIgnoresAnEmptyWorktreeBehindTrunk(t *testing.T) {
	ctx, _ := runFixture(t, false)
	var buf bytes.Buffer
	stale, err := New(ctx, "feat/stale", NewOptions{NoSetup: true, Base: "main~1"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(stale)
	old := gitOut(t, stale, "rev-parse", "HEAD")
	opts := noAgents()
	opts.Agents = []wtsync.Agent{{Name: "stale-1", Cwd: resolved}}
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if s := out.String(); strings.Contains(s, "is a stack with") || strings.Contains(s, "stale-1") {
		t.Fatalf("stale was pulled in:\n%s", s)
	}
	if !gitAncestor(t, ctx.Repo.MainRoot, "origin/main", "feat_wt/bump") {
		t.Fatalf("bump not rebased:\n%s", out.String())
	}
	if gitOut(t, stale, "rev-parse", "HEAD") != old {
		t.Fatal("stale moved")
	}
}

func TestSyncRunAsksOnceForMoreThanOneWorktreeAndStopsOnNo(t *testing.T) {
	ctx, bump := runFixture(t, false)
	stackFixture(t, ctx)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var asked []string
	opts := noAgents()
	opts.Confirm = func(works []string) (bool, error) { asked = works; return false, nil }
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("a declined confirmation is not an error: %v", err)
	}
	if len(asked) != 2 || !strings.Contains(out.String(), "nothing rebased") {
		t.Fatalf("asked %v out %s", asked, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved after no")
	}
	// --yes never asks.
	opts.Yes = true
	asked = nil
	out.Reset()
	if err := SyncRun(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if asked != nil {
		t.Fatal("asked despite --yes")
	}
}

// A contested stop is no longer refused: the run rebases up to it, stages
// what the strategies resolved, leaves the rebase in place and writes the
// handover.
func TestSyncRunHandsAContestedStopOver(t *testing.T) {
	ctx, bump := contestedFixture(t)
	branch := "feat_wt/bump"
	old := gitOut(t, ctx.Repo.MainRoot, "rev-parse", branch)
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
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
	if got := gitOut(t, ctx.Repo.MainRoot, "rev-parse", branch); got != old {
		t.Fatal("the branch moved before the rebase finished")
	}
	// No result ref: the run did not finish for this branch.
	if _, ok, err := wtsync.ResultTip(ctx.Repo.MainRoot, branch, st.Epoch); err != nil || ok {
		t.Fatalf("ResultTip = %v, %v; a handed-over run pins no result", ok, err)
	}
}

// A second run refuses a worktree waiting on a person, and leaves both the
// rebase and the handover exactly as the first run left them.
func TestSyncRunRefusesAWorktreeWaitingOnAPerson(t *testing.T) {
	ctx, bump := contestedFixture(t)
	var first bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &first); err == nil {
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
	head := gitOut(t, bump, "rev-parse", "HEAD")

	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err == nil {
		t.Fatalf("a second run must not touch a worktree somebody is finishing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "refused:") || !strings.Contains(out.String(), "wt sync resume") {
		t.Fatalf("the refusal does not name wt sync resume:\n%s", out.String())
	}
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the second run disturbed the rebase", busy, err)
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != head {
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

// A handover that cannot be written leaves the rebase in the worktree with
// nothing to explain it. The run cannot put that right — a restore here
// would be the one thing the handover exists to avoid — but it must say
// where the worktree is and what puts it back.
func TestSyncRunSaysWhereAFailedHandoverLeftTheWorktree(t *testing.T) {
	ctx, bump := contestedFixture(t)
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	// A directory where writeAtomic wants its temp file: the brief cannot be
	// written, and nothing else in the run touches this name.
	if err := os.Mkdir(wtsync.PlanPath(gitDir)+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	old := gitOut(t, ctx.Repo.MainRoot, "rev-parse", "feat_wt/bump")
	var out bytes.Buffer
	err = SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	s := out.String()
	if err == nil || !strings.Contains(err.Error(), "bump (failed)") {
		t.Fatalf("err %v\n%s", err, s)
	}
	if !strings.Contains(s, "bump is left mid-rebase with no plan") || !strings.Contains(s, "rebase --abort") {
		t.Fatalf("the run did not say where it left the worktree:\n%s", s)
	}
	// Not wt sync undo: it refuses a mid-rebase worktree, and there is no
	// handover here for it to abort, so naming it would send the person to a
	// command that answers "nothing undone".
	if strings.Contains(s, "wt sync undo") {
		t.Fatalf("the run named a command that will refuse:\n%s", s)
	}
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the run put the rebase back", busy, berr)
	}
	if has, herr := wtsync.HasPlan(gitDir); herr != nil || has {
		t.Fatalf("HasPlan = %v, %v; a handover that failed leaves no marker", has, herr)
	}
	if _, err := os.Stat(wtsync.PlanPath(gitDir)); !os.IsNotExist(err) {
		t.Fatalf("a half-written brief survived: %v", err)
	}
	// The command the line names has to be the one that works. undo does not:
	// it refuses a mid-rebase worktree outright.
	if uerr := SyncUndo(ctx, "bump", UndoOptions{Agents: []wtsync.Agent{}}, io.Discard); uerr == nil {
		t.Fatal("undo accepted a mid-rebase worktree; the old wording would have been true")
	}
	gitOut(t, bump, "rebase", "--abort")
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v after the abort the line names", busy, berr)
	}
	if got := gitOut(t, ctx.Repo.MainRoot, "rev-parse", "feat_wt/bump"); got != old {
		t.Fatalf("the abort left %s at %s, not %s", "feat_wt/bump", got, old)
	}
}

// A stack parent may not be left waiting: its children would be stranded on
// a base that is about to be rewritten, so its stop is put back instead.
func TestSyncRunPutsAStackParentBackInsteadOfHandingItOver(t *testing.T) {
	ctx, bump := contestedFixture(t)
	child := stackFixture(t, ctx)
	oldBump, oldChild := gitOut(t, bump, "rev-parse", "HEAD"), gitOut(t, child, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
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
	if gitOut(t, bump, "rev-parse", "HEAD") != oldBump || gitOut(t, child, "rev-parse", "HEAD") != oldChild {
		t.Fatal("a stack member moved")
	}
}

func TestSyncRunHandsOverALaterUnclaimedStop(t *testing.T) {
	// The first stop is a script's (v.txt), which truncates the simulation
	// there, so triage says recipe? and the run starts; the branch's second
	// commit conflicts on a.txt, which nobody claims. Without the script the
	// simulation would replay to that second stop itself and triage would
	// call it contested - which is the point of doing it this way round:
	// this pins what happens when a run, not triage, is the one that finds
	// the unclaimed stop.
	ctx, bump := runFixture(t, false)
	declareScript(t, ctx)
	main := ctx.Repo.MainRoot
	if err := os.WriteFile(filepath.Join(bump, "a.txt"), []byte("branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, bump, "add", "-A")
	gitOut(t, bump, "commit", "-q", "-m", "a on branch")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, main, "add", "-A")
	gitOut(t, main, "commit", "-q", "-m", "a on trunk")
	gitOut(t, main, "fetch", "-q", "origin")
	old := gitOut(t, main, "rev-parse", "feat_wt/bump")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	if err == nil || !strings.Contains(out.String(), "needs you") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if gitOut(t, main, "rev-parse", "feat_wt/bump") != old {
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
	if !strings.Contains(gitOut(t, main, "for-each-ref", "--format=%(refname)", "refs/wt-sync/"), "feat_wt/bump/99") {
		t.Fatal("the safety ref, the undo target, is missing")
	}
}

// twoChildFixture puts two independent children on feat_wt/bump and moves
// a.txt on trunk, so the child that also touches a.txt hits an unclaimed
// conflict on its own commit while its sibling replays cleanly. The names
// put the conflicted one first in the run order, so a whole-stack poison
// would visibly reach the sibling.
func twoChildFixture(t *testing.T, ctx *Context, main string) (conflicted, clean string) {
	t.Helper()
	var buf bytes.Buffer
	child := func(spec, file, content string) string {
		t.Helper()
		path, err := New(ctx, spec, NewOptions{NoSetup: true, Base: "feat_wt/bump"}, &buf)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		gitOut(t, path, "add", "-A")
		gitOut(t, path, "commit", "-q", "-m", spec)
		return path
	}
	conflicted = child("feat/alpha", "a.txt", "alpha\n")
	clean = child("feat/zulu", "c.txt", "c\n")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, main, "add", "-A")
	gitOut(t, main, "commit", "-q", "-m", "a on trunk")
	gitOut(t, main, "fetch", "-q", "origin")
	return conflicted, clean
}

func TestSyncRunAHandedOverChildDoesNotStopItsSibling(t *testing.T) {
	ctx, bump := runFixture(t, false)
	// As above: the script truncates the simulation at v.txt, so triage
	// cannot foresee the child's unclaimed a.txt and the run reaches it. A
	// leaf has no descendants, so its stop is handed over, not put back.
	declareScript(t, ctx)
	conflicted, clean := twoChildFixture(t, ctx, ctx.Repo.MainRoot)
	main := ctx.Repo.MainRoot
	oldConflicted := gitOut(t, main, "rev-parse", "feat_wt/alpha")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	s := out.String()
	if err == nil {
		t.Fatalf("a handed-over branch is a failure:\n%s", s)
	}
	if !strings.Contains(s, "needs you") {
		t.Fatalf("out %s", s)
	}
	// The sibling rebased onto the parent's tip; the handed-over one did not
	// move: its rebase is still in flight.
	parentTip := gitOut(t, bump, "rev-parse", "HEAD")
	if gitOut(t, clean, "rev-parse", "HEAD~1") != parentTip {
		t.Fatalf("the sibling was not rebased onto the parent:\n%s", s)
	}
	if gitOut(t, main, "rev-parse", "feat_wt/alpha") != oldConflicted {
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
