package commands

import (
	"bytes"
	"errors"
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
	for _, want := range []string{"safety refs/wt-sync/feat_wt/bump/99", "stop 1/1", "v.txt✓ owned-line", "rebased 1 commit", "committed 1 file", "as ", "push: git -C"} {
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

func TestSyncRunRestoresAndReportsALaterUnclaimedStop(t *testing.T) {
	// The first stop is a script's (v.txt), which truncates the simulation
	// there, so triage says recipe? and the run starts; the branch's second
	// commit conflicts on a.txt, which nobody claims. Without the script the
	// simulation would replay to that second stop itself and the run would
	// refuse before touching anything - which is the point of doing it this
	// way round: this pins what happens when a run, not triage, is the one
	// that finds the unclaimed stop.
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
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	if err == nil || !strings.Contains(out.String(), "restored: a.txt at 2/2") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD moved")
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

func TestSyncRunARestoredChildDoesNotStopItsSibling(t *testing.T) {
	ctx, bump := runFixture(t, false)
	// As above: the script truncates the simulation at v.txt, so triage
	// cannot foresee the child's unclaimed a.txt and the run reaches it.
	declareScript(t, ctx)
	conflicted, clean := twoChildFixture(t, ctx, ctx.Repo.MainRoot)
	oldConflicted := gitOut(t, conflicted, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncRun(ctx, []string{"bump"}, noAgents(), &out)
	s := out.String()
	if err == nil {
		t.Fatalf("a restored branch is a failure:\n%s", s)
	}
	if !strings.Contains(s, "restored: a.txt") {
		t.Fatalf("out %s", s)
	}
	// The sibling rebased onto the parent's tip; the restored one did not move.
	parentTip := gitOut(t, bump, "rev-parse", "HEAD")
	if gitOut(t, clean, "rev-parse", "HEAD~1") != parentTip {
		t.Fatalf("the sibling was not rebased onto the parent:\n%s", s)
	}
	if gitOut(t, conflicted, "rev-parse", "HEAD") != oldConflicted {
		t.Fatal("the restored branch moved")
	}
	if strings.Contains(s, "alpha was restored") {
		t.Fatalf("the sibling was poisoned:\n%s", s)
	}
	// The closing error names the restored branch and nothing else.
	if !strings.Contains(err.Error(), "alpha (restored)") || strings.Contains(err.Error(), "zulu") {
		t.Fatalf("err %v", err)
	}
}
