package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// sweepRepoWith is a main checkout on main with a bare origin beside it,
// fetched, origin/HEAD pointing at main, opened after all of that.
func sweepRepoWith(t *testing.T, conf string) (ctx *Context, main, origin string) {
	t.Helper()
	main = committedRepo(t, conf)
	origin = filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, main, "clone", "-q", "--bare", main, origin)
	gitIn(t, main, "remote", "add", "origin", origin)
	gitIn(t, main, "fetch", "-q", "origin")
	gitIn(t, main, "remote", "set-head", "origin", "main")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, main, origin
}

func sweepRepo(t *testing.T) (ctx *Context, main, origin string) {
	t.Helper()
	return sweepRepoWith(t, minimalConf)
}

// addWork puts n empty commits on branch through update-ref, so no checkout
// moves. Messages are unique: two empty commits with the same parent, message
// and second would otherwise be the same commit.
func addWork(t *testing.T, main, branch string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		tree := gitOut(t, main, "rev-parse", branch+"^{tree}")
		c := gitOut(t, main, "commit-tree", "-p", branch, "-m", fmt.Sprintf("%s work %d", branch, i), tree)
		gitIn(t, main, "update-ref", "refs/heads/"+branch, c)
	}
}

// branchWithWork cuts branch from main and gives it n commits of its own.
func branchWithWork(t *testing.T, main, branch string, n int) {
	t.Helper()
	gitIn(t, main, "branch", branch, "main")
	addWork(t, main, branch, n)
}

// landOnMain merges branch into main and pushes, which is what merging its
// pull request amounts to.
func landOnMain(t *testing.T, main, branch string) {
	t.Helper()
	gitIn(t, main, "merge", "-q", "--no-ff", "-m", "merge "+branch, branch)
	gitIn(t, main, "push", "-q", "origin", "main")
	gitIn(t, main, "fetch", "-q", "origin")
}

// goneUpstream gives branch an upstream origin does not have, which is what a
// remote branch deleted on merge looks like after fetch --prune.
func goneUpstream(t *testing.T, main, branch string) {
	t.Helper()
	gitIn(t, main, "config", "branch."+branch+".remote", "origin")
	gitIn(t, main, "config", "branch."+branch+".merge", "refs/heads/"+branch)
}

// nobodyIn is the sessions a plan is checked against when the test names none.
var nobodyIn = SweepOptions{Agents: []wtsync.Agent{}}

func planOf(t *testing.T, ctx *Context) SweepPlan {
	t.Helper()
	return planWith(t, ctx, nobodyIn)
}

func planWith(t *testing.T, ctx *Context, opts SweepOptions) SweepPlan {
	t.Helper()
	bases, err := trunkBases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planSweep(ctx, bases, opts.listAgents)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func branchNames(bs []SweepBranch) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.Name)
	}
	return out
}

func worktreeNames(ws []SweepWorktree) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Name)
	}
	return out
}

func rendered(p SweepPlan) string {
	var buf bytes.Buffer
	p.Render(&buf, 0)
	return buf.String()
}

func inAnyGroup(p SweepPlan, name string) bool {
	return slices.Contains(branchNames(p.Delete), name) ||
		slices.Contains(worktreeNames(p.Remove), name) ||
		slices.Contains(branchNames(p.CheckedOut), name) ||
		slices.Contains(branchNames(p.Gone), name)
}

// mergedWorktree is a worktree wt new made on a branch cut from trunk and
// never committed to, which is merged: what is left behind once a pull
// request lands and nobody ran wt remove.
func mergedWorktree(t *testing.T, ctx *Context, work string) string {
	t.Helper()
	var buf bytes.Buffer
	path, err := New(ctx, work, NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestSweepPlanDeletesAMergedBranch(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")

	p := planOf(t, ctx)
	if !slices.Equal(branchNames(p.Delete), []string{"done-work"}) {
		t.Fatalf("delete = %v", branchNames(p.Delete))
	}
	if p.Delete[0].MergedInto != "origin/main" {
		t.Errorf("merged into %q, want origin/main first", p.Delete[0].MergedInto)
	}
	if out := rendered(p); !strings.Contains(out, "done-work") || !strings.Contains(out, "merged into origin/main") {
		t.Errorf("the plan must name the branch and why it goes:\n%s", out)
	}
}

func TestSweepPlanLeavesUnmergedWorkOut(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "wip", 2)

	if p := planOf(t, ctx); inAnyGroup(p, "wip") {
		t.Fatalf("unmerged work with no gone upstream belongs in no group: %+v", p)
	}
}

// Merges happen on the remote; the main checkout's trunk is often behind it.
func TestSweepPlanCountsAMergeOnlyOriginHas(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	branchWithWork(t, main, "landed", 1)
	// To the path: a push by remote name would move origin/main without a fetch.
	gitIn(t, main, "push", "-q", origin, "landed:main")
	gitIn(t, main, "fetch", "-q", "origin")

	p := planOf(t, ctx)
	if !slices.Contains(branchNames(p.Delete), "landed") {
		t.Fatalf("landed is in origin/main; want it deleted: %v", branchNames(p.Delete))
	}
}

// Some repositories commit straight to trunk, and origin can be behind.
func TestSweepPlanCountsAMergeOnlyLocalTrunkHas(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "local-only", 1)
	gitIn(t, main, "merge", "-q", "--no-ff", "-m", "merge local-only", "local-only")

	p := planOf(t, ctx)
	if !slices.Equal(branchNames(p.Delete), []string{"local-only"}) || p.Delete[0].MergedInto != "main" {
		t.Fatalf("delete = %+v", p.Delete)
	}
}

func TestSweepPlanRemovesACleanMergedWorktree(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")

	p := planOf(t, ctx)
	if slices.Contains(branchNames(p.Delete), "fix_wt/login-crash") {
		t.Fatal("a branch a worktree has checked out is not deleted on its own")
	}
	if slices.Contains(branchNames(p.CheckedOut), "fix_wt/login-crash") {
		t.Fatal("nothing is using the worktree; it is not kept")
	}
	if !slices.Equal(worktreeNames(p.Remove), []string{"fix_wt/login-crash"}) {
		t.Fatalf("want the worktree removed: %v", worktreeNames(p.Remove))
	}
	if p.Remove[0].Work != "login-crash" || p.Remove[0].Worktree != path || p.Remove[0].Plan.Outcome != BranchDeleted {
		t.Errorf("remove = %+v", p.Remove[0])
	}
	out := rendered(p)
	for _, want := range []string{"Will be removed with its branch, 1 worktree:", "login-crash", "fix_wt/login-crash", path, "merged into origin/main"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan must show every directory that will go, with its branch and why:\nwant %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "No merged branches") {
		t.Errorf("there is a worktree to sweep:\n%s", out)
	}
}

func TestSweepRemovesAMergedWorktreeAndDeletesItsBranch(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	branchWithWork(t, main, "done-work", 0)

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true, Agents: []wtsync.Agent{}}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if exists(path) {
		t.Error("the worktree should have been removed")
	}
	if ctx.Repo.BranchExists("fix_wt/login-crash") {
		t.Error("the branch should have gone with its worktree")
	}
	if ctx.Repo.BranchExists("done-work") {
		t.Error("the plain branch should still have been deleted")
	}
	if !strings.Contains(buf.String(), "✓ worktree removed; branch fix_wt/login-crash was merged into origin/main and has been deleted") {
		t.Errorf("each removal says what wt remove says:\n%s", buf.String())
	}
}

func TestSweepPlanKeepsADirtyMergedWorktree(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	mustWrite(t, filepath.Join(path, "scratch.txt"), "not yet\n")

	p := planOf(t, ctx)
	if len(p.Remove) != 0 {
		t.Fatalf("a dirty worktree must not be removed: %v", worktreeNames(p.Remove))
	}
	if !slices.Contains(branchNames(p.CheckedOut), "fix_wt/login-crash") {
		t.Fatalf("want it kept: %+v", p)
	}
	if out := rendered(p); !strings.Contains(out, "fix_wt/login-crash  dirty; commit or discard the changes, then sweep again") {
		t.Errorf("the row must say why it is kept:\n%s", out)
	}
}

func TestSweepPlanKeepsAMergedWorktreeWhoseLockIsHeld(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	gitIn(t, main, "worktree", "lock", "--reason", fmt.Sprintf("(pid %d start now)", os.Getpid()), path)

	p := planOf(t, ctx)
	if len(p.Remove) != 0 {
		t.Fatalf("a worktree whose lock is held must not be removed: %v", worktreeNames(p.Remove))
	}
	want := fmt.Sprintf("fix_wt/login-crash  lock held by pid %d; wt remove login-crash --force", os.Getpid())
	if out := rendered(p); !strings.Contains(out, want) {
		t.Errorf("the row must name the holder and say --force breaks it:\nwant %q in\n%s", want, out)
	}
}

// A stale lock is litter wt remove releases on its way; it does not keep the
// worktree.
func TestSweepRemovesAMergedWorktreeWithAStaleLock(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	gitIn(t, main, "worktree", "lock", "--reason", "(pid 2147483000 start long ago)", path)

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true, Agents: []wtsync.Agent{}}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if exists(path) {
		t.Error("the worktree should have been removed past its stale lock")
	}
	if !strings.Contains(buf.String(), "- released a stale lock") {
		t.Errorf("say the lock went first:\n%s", buf.String())
	}
}

func TestSweepPlanKeepsAMergedWorktreeWithAnIdleSession(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")

	p := planWith(t, ctx, SweepOptions{Agents: idleIn(t, path, "parked-1")})
	if len(p.Remove) != 0 {
		t.Fatalf("a worktree with a session in it, idle or not, must not be removed: %v", worktreeNames(p.Remove))
	}
	if out := rendered(p); !strings.Contains(out, "fix_wt/login-crash  session parked-1 (idle) in it; wt remove login-crash") {
		t.Errorf("the row must name the session the way wt sync does:\n%s", out)
	}
}

func TestSweepKeepsAWorktreeWhenTheSessionsCannotBeListed(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	mergedWorktree(t, ctx, "fix/login-crash")

	bases, err := trunkBases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planSweep(ctx, bases, func() ([]wtsync.Agent, error) { return nil, fmt.Errorf("claude is away") })
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Remove) != 0 {
		t.Fatalf("with nobody to ask, no worktree is known to be safe: %v", worktreeNames(p.Remove))
	}
	if out := rendered(p); !strings.Contains(out, "cannot list agent sessions (claude is away)") {
		t.Errorf("say why:\n%s", out)
	}
}

func TestSweepLeavesAnUnmergedWorktreeAlone(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "feat/api-tidy")
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "real work")

	p := planOf(t, ctx)
	if inAnyGroup(p, "feat_wt/api-tidy") {
		t.Fatalf("unmerged work belongs in no group: %+v", p)
	}
	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true, Agents: []wtsync.Agent{}}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if !exists(path) || !ctx.Repo.BranchExists("feat_wt/api-tidy") {
		t.Error("an unmerged worktree and its branch are never touched")
	}
	if !strings.Contains(buf.String(), "No merged branches or worktrees to sweep.") {
		t.Errorf("an empty plan says so in one sentence:\n%s", buf.String())
	}
}

// The prompt can stay open while somebody starts editing in a worktree the
// plan was going to remove. It is kept; the rest of the plan still runs.
func TestSweepKeepsAWorktreeThatBecameDirtyWhileThePromptWasOpen(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	other := mergedWorktree(t, ctx, "chore/tidy")
	branchWithWork(t, main, "done-work", 0)

	opts := SweepOptions{Agents: []wtsync.Agent{}, Confirm: func(SweepPlan) (bool, error) {
		mustWrite(t, filepath.Join(path, "scratch.txt"), "started\n")
		return true, nil
	}}
	var buf bytes.Buffer
	err := Sweep(ctx, opts, &buf)
	if err == nil {
		t.Fatal("a kept worktree must make the sweep fail")
	}
	if !strings.Contains(err.Error(), "1 of 1 branch and 2 worktrees kept") {
		t.Errorf("the closing error counts both kinds: %v", err)
	}
	if !exists(path) || !ctx.Repo.BranchExists("fix_wt/login-crash") {
		t.Fatal("a worktree that gained a change after the plan was removed")
	}
	if exists(other) || ctx.Repo.BranchExists("chore_wt/tidy") || ctx.Repo.BranchExists("done-work") {
		t.Error("the unchanged worktree and the plain branch should still have gone")
	}
	if !strings.Contains(buf.String(), "- kept login-crash: dirty") {
		t.Errorf("say why it was kept:\n%s", buf.String())
	}
}

func TestSweepKeepsAWorktreeASessionEnteredWhileThePromptWasOpen(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")

	opts := SweepOptions{Agents: []wtsync.Agent{},
		Relist:  func() ([]wtsync.Agent, error) { return idleIn(t, path, "late-1"), nil },
		Confirm: func(SweepPlan) (bool, error) { return true, nil }}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err == nil {
		t.Fatal("a kept worktree must make the sweep fail")
	}
	if !exists(path) {
		t.Fatal("a worktree a session entered after the plan was removed under it")
	}
	if !strings.Contains(buf.String(), "- kept login-crash: session late-1 (idle) in it") {
		t.Errorf("say who was found:\n%s", buf.String())
	}
}

// A directory deleted by hand leaves git's record of the worktree behind,
// and with it the branch counts as in use. wt remove finds nothing there.
func TestSweepPlanKeepsAWorktreeWhoseDirectoryIsGone(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	p := planOf(t, ctx)
	if len(p.Remove) != 0 {
		t.Fatalf("there is no directory to remove: %v", worktreeNames(p.Remove))
	}
	if out := rendered(p); !strings.Contains(out, "fix_wt/login-crash  its directory is gone; git worktree prune, then sweep again") {
		t.Errorf("say what happened and what clears it:\n%s", out)
	}
}

// The re-check before anything goes, case by case: what can change while
// the prompt is open, and what the kept line says about it.
func TestSweepKeepsAWorktreeDeletedByHandWhileThePromptWasOpen(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")

	opts := SweepOptions{Agents: []wtsync.Agent{}, Confirm: func(SweepPlan) (bool, error) {
		return true, os.RemoveAll(path)
	}}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err == nil {
		t.Fatal("a kept worktree must make the sweep fail")
	}
	if !ctx.Repo.BranchExists("fix_wt/login-crash") {
		t.Fatal("the branch of a worktree deleted by hand was deleted underneath its record")
	}
	if !strings.Contains(buf.String(), "- kept login-crash: its directory is gone") {
		t.Errorf("say why it was kept:\n%s", buf.String())
	}
}

func TestSweepKeepsAWorktreeLockedWhileThePromptWasOpen(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")

	opts := SweepOptions{Agents: []wtsync.Agent{}, Confirm: func(SweepPlan) (bool, error) {
		gitIn(t, main, "worktree", "lock", "--reason", fmt.Sprintf("(pid %d start now)", os.Getpid()), path)
		return true, nil
	}}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err == nil {
		t.Fatal("a kept worktree must make the sweep fail")
	}
	if !exists(path) {
		t.Fatal("a worktree locked after the plan was removed past the lock")
	}
	if !strings.Contains(buf.String(), fmt.Sprintf("- kept login-crash: lock held by pid %d", os.Getpid())) {
		t.Errorf("say who holds it:\n%s", buf.String())
	}
}

func TestSweepKeepsAWorktreeWhoseBranchMovedWhileThePromptWasOpen(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")

	opts := SweepOptions{Agents: []wtsync.Agent{}, Confirm: func(SweepPlan) (bool, error) {
		gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "landed in the window")
		return true, nil
	}}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err == nil {
		t.Fatal("a kept worktree must make the sweep fail")
	}
	if !exists(path) || !ctx.Repo.BranchExists("fix_wt/login-crash") {
		t.Fatal("a commit that landed after the plan was removed with its worktree")
	}
	if !strings.Contains(buf.String(), "- kept login-crash: it moved after the plan was made") {
		t.Errorf("say why it was kept:\n%s", buf.String())
	}
}

func TestSweepKeepsAWorktreeThatSwitchedBranchWhileThePromptWasOpen(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")

	opts := SweepOptions{Agents: []wtsync.Agent{}, Confirm: func(SweepPlan) (bool, error) {
		gitIn(t, path, "switch", "-q", "-c", "elsewhere")
		return true, nil
	}}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err == nil {
		t.Fatal("a kept worktree must make the sweep fail")
	}
	if !exists(path) || !ctx.Repo.BranchExists("fix_wt/login-crash") {
		t.Fatal("a worktree that moved off the branch was removed, or the branch deleted, on a stale plan")
	}
	if !strings.Contains(buf.String(), "- kept login-crash: its worktree is on elsewhere now; the branch goes next time") {
		t.Errorf("say what happened:\n%s", buf.String())
	}
}

func TestSweepKeepsAWorktreeWhenTheSessionsCannotBeListedAgain(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")

	opts := SweepOptions{Agents: []wtsync.Agent{},
		Relist:  func() ([]wtsync.Agent, error) { return nil, fmt.Errorf("claude is away") },
		Confirm: func(SweepPlan) (bool, error) { return true, nil }}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err == nil {
		t.Fatal("a kept worktree must make the sweep fail")
	}
	if !exists(path) {
		t.Fatal("with nobody to ask, no worktree is known to be safe")
	}
	if !strings.Contains(buf.String(), "- kept login-crash: cannot list agent sessions (claude is away)") {
		t.Errorf("say why:\n%s", buf.String())
	}
}

// A removal git refuses keeps that worktree and its branch; the sweep goes on
// to the plain branches all the same.
func TestSweepGoesOnToTheBranchesWhenARemovalFails(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	branchWithWork(t, main, "done-work", 0)

	opts := SweepOptions{Agents: []wtsync.Agent{}, Confirm: func(SweepPlan) (bool, error) {
		// git cannot unlink files in a directory it may not write to.
		if err := os.Chmod(path, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
		return true, nil
	}}
	var buf bytes.Buffer
	err := Sweep(ctx, opts, &buf)
	if err == nil {
		t.Fatal("a kept worktree must make the sweep fail")
	}
	if !strings.Contains(err.Error(), "1 of 1 branch and 1 worktree kept") {
		t.Errorf("the closing error counts what was kept: %v", err)
	}
	if !ctx.Repo.BranchExists("fix_wt/login-crash") {
		t.Fatal("the branch of a worktree that could not be removed was deleted underneath it")
	}
	if ctx.Repo.BranchExists("done-work") {
		t.Error("the plain branch should still have been deleted")
	}
	if !strings.Contains(buf.String(), "- kept login-crash: git refused to remove "+path) {
		t.Errorf("say what git said:\n%s", buf.String())
	}
}

func TestSweepAsksOnceForBranchesAndWorktreesTogether(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	mergedWorktree(t, ctx, "fix/login-crash")
	branchWithWork(t, main, "done-work", 0)
	branchWithWork(t, main, "also-done", 0)

	asked := 0
	var seen SweepPlan
	opts := SweepOptions{Agents: []wtsync.Agent{}, Confirm: func(p SweepPlan) (bool, error) {
		asked++
		seen = p
		return false, nil
	}}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if asked != 1 {
		t.Errorf("asked %d times, want once", asked)
	}
	if seen.Question() != "Delete these 2 branches and remove this worktree?" {
		t.Errorf("question = %q", seen.Question())
	}
	if !strings.Contains(buf.String(), "Nothing was swept.") {
		t.Errorf("say so:\n%s", buf.String())
	}
}

func TestSweepWithoutATerminalRemovesNoWorktree(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Agents: []wtsync.Agent{}}, &buf); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !exists(path) {
		t.Error("with no terminal and no --yes, nothing may be removed")
	}
	if !strings.Contains(buf.String(), "Pass --yes to sweep 1 worktree.") {
		t.Errorf("say how to go ahead:\n%s", buf.String())
	}
}

// A rebase detaches HEAD, so for-each-ref's worktreepath is empty for the
// branch being rebased. The worktree list's head-name is what still names it.
func TestSweepPlanSeesABranchInTheMiddleOfARebase(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	dst := foreignWorktree(t, ctx, main, "rebasing", 0)
	mustWrite(t, filepath.Join(dst, "f.txt"), "branch\n")
	gitIn(t, dst, "add", "f.txt")
	gitIn(t, dst, "commit", "-q", "-m", "branch side")
	landOnMain(t, main, "rebasing")

	other := filepath.Join(ctx.Repo.Parent, "other")
	gitIn(t, main, "worktree", "add", "-q", "-b", "other", other, "main^1")
	mustWrite(t, filepath.Join(other, "f.txt"), "other\n")
	gitIn(t, other, "add", "f.txt")
	gitIn(t, other, "commit", "-q", "-m", "other side")

	rebase := exec.Command("git", "rebase", "other")
	rebase.Dir = dst
	_ = rebase.Run() // stops at the conflict on f.txt, which is the state under test
	if wp := gitOut(t, main, "for-each-ref", "--format=%(worktreepath)", "refs/heads/rebasing"); wp != "" {
		t.Fatalf("precondition: git reports rebasing as checked out at %q; the trap is gone", wp)
	}

	p := planOf(t, ctx)
	if slices.Contains(branchNames(p.Delete), "rebasing") {
		t.Fatal("a branch mid-rebase was offered for deletion")
	}
	if !slices.Contains(branchNames(p.CheckedOut), "rebasing") {
		t.Errorf("want rebasing in the checked-out group: %v", branchNames(p.CheckedOut))
	}
	// wt remove would throw the rebase away and leave the branch.
	if out := rendered(p); !strings.Contains(out, "held by the rebase in "+dst+"; finish or abort it there, then sweep again") {
		t.Errorf("the plan must send the reader to the rebase, not to wt remove:\n%s", out)
	}
}

// A bisect detaches HEAD; git still counts the branch it started from as in
// use, and only BISECT_START names it.
func TestSweepPlanSeesABranchABisectStartedFrom(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	addWork(t, main, "main", 4)
	dst := foreignWorktree(t, ctx, main, "bisecting", 0)
	gitIn(t, dst, "bisect", "start")
	gitIn(t, dst, "bisect", "bad")
	gitIn(t, dst, "bisect", "good", "HEAD~4")
	if err := exec.Command("git", "-C", dst, "symbolic-ref", "-q", "HEAD").Run(); err == nil {
		t.Fatal("precondition: the bisect left HEAD on the branch; the trap is not set")
	}

	p := planOf(t, ctx)
	if slices.Contains(branchNames(p.Delete), "bisecting") {
		t.Fatal("a branch a bisect started from was offered for deletion")
	}
	if !slices.Contains(branchNames(p.CheckedOut), "bisecting") {
		t.Errorf("want bisecting in the checked-out group: %v", branchNames(p.CheckedOut))
	}
	// No worktree has bisecting checked out, so wt remove bisecting finds nothing.
	if out := rendered(p); !strings.Contains(out, "held by the bisect in "+dst+"; finish or abort it there, then sweep again") {
		t.Errorf("the plan must send the reader to the bisect, not to wt remove:\n%s", out)
	}
}

// A stopped rebase --update-refs holds every branch it will move, not only
// the one being rebased; git lists them in rebase-merge/update-refs.
func TestSweepPlanSeesABranchARebaseWillUpdate(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	dst := foreignWorktree(t, ctx, main, "upper", 0)
	mustWrite(t, filepath.Join(dst, "f.txt"), "lower\n")
	gitIn(t, dst, "add", "f.txt")
	gitIn(t, dst, "commit", "-q", "-m", "lower side")
	gitIn(t, main, "branch", "lower", "upper")
	mustWrite(t, filepath.Join(dst, "f.txt"), "upper\n")
	gitIn(t, dst, "commit", "-q", "-am", "upper side")
	landOnMain(t, main, "lower")

	other := filepath.Join(ctx.Repo.Parent, "other")
	gitIn(t, main, "worktree", "add", "-q", "-b", "other", other, "main^1")
	mustWrite(t, filepath.Join(other, "f.txt"), "other\n")
	gitIn(t, other, "add", "f.txt")
	gitIn(t, other, "commit", "-q", "-m", "other side")

	rebase := exec.Command("git", "rebase", "--update-refs", "other")
	rebase.Dir = dst
	_ = rebase.Run() // stops at the conflict on f.txt, which is the state under test
	updateRefs := filepath.Join(gitOut(t, dst, "rev-parse", "--absolute-git-dir"), "rebase-merge", "update-refs")
	if err := exec.Command("grep", "-qxF", "refs/heads/lower", updateRefs).Run(); err != nil {
		t.Fatalf("precondition: the stopped rebase does not list lower in %s: %v", updateRefs, err)
	}

	p := planOf(t, ctx)
	if slices.Contains(branchNames(p.Delete), "lower") {
		t.Fatal("a branch a stopped rebase will update was offered for deletion")
	}
	if !slices.Contains(branchNames(p.CheckedOut), "lower") {
		t.Errorf("want lower in the checked-out group: %v", branchNames(p.CheckedOut))
	}
	if out := rendered(p); !strings.Contains(out, "lower  held by the rebase in "+dst+"; finish or abort it there, then sweep again") {
		t.Errorf("the plan must send the reader to the rebase, not to wt remove:\n%s", out)
	}
}

func TestSweepPlanNamesTheMainCheckoutWhenItIsOnAMergedBranch(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	gitIn(t, main, "switch", "-q", "done-work")

	p := planOf(t, ctx)
	if !slices.Contains(branchNames(p.CheckedOut), "done-work") {
		t.Fatalf("want done-work in the checked-out group: %+v", p)
	}
	if out := rendered(p); !strings.Contains(out, "main checkout") {
		t.Errorf("wt remove cannot remove the main checkout; say what to do instead:\n%s", out)
	}
}

func TestSweepPlanNeverOffersProtectedBranches(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	for _, name := range []string{"master", "develop", "development", "staging", "production", "release-2.1"} {
		gitIn(t, main, "branch", name)
	}

	p := planOf(t, ctx)
	if len(p.Delete)+len(p.CheckedOut)+len(p.Gone) != 0 {
		t.Fatalf("protected branches must not appear anywhere: %+v", p)
	}
}

// The configured trunk and origin's HEAD can disagree; both are trunks.
func TestSweepPlanProtectsTheConfiguredTrunkAndOriginHead(t *testing.T) {
	main := committedRepo(t, "MAIN_BRANCH=\"trunk\"\nBUILD_INIT_ENABLED=false\n")
	gitIn(t, main, "branch", "trunk")
	gitIn(t, main, "branch", "integration")
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, main, "clone", "-q", "--bare", main, origin)
	gitIn(t, main, "remote", "add", "origin", origin)
	gitIn(t, main, "fetch", "-q", "origin")
	gitIn(t, main, "remote", "set-head", "origin", "integration")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}

	p := planOf(t, ctx)
	for _, name := range []string{"trunk", "integration"} {
		if inAnyGroup(p, name) {
			t.Errorf("%s is a trunk and must never be offered: %+v", name, p)
		}
	}
}

// A HEAD whose target was pruned still names trunk.
func TestSweepPlanProtectsTheBranchADanglingOriginHeadNames(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	gitIn(t, main, "branch", "integration")
	gitIn(t, main, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/integration")

	if p := planOf(t, ctx); inAnyGroup(p, "integration") {
		t.Fatalf("integration is origin's HEAD, dangling or not: %+v", p)
	}
}

func TestSweepPlanReportsAGoneBranchThatStillHasWork(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "old-idea", 3)
	goneUpstream(t, main, "old-idea")
	branchWithWork(t, main, "gone-merged", 0)
	goneUpstream(t, main, "gone-merged")

	p := planOf(t, ctx)
	if !slices.Equal(branchNames(p.Gone), []string{"old-idea"}) || p.Gone[0].Ahead != 3 {
		t.Fatalf("gone = %+v", p.Gone)
	}
	if !slices.Contains(branchNames(p.Delete), "gone-merged") {
		t.Errorf("a gone upstream on a merged branch is still merged: %v", branchNames(p.Delete))
	}
	if out := rendered(p); !strings.Contains(out, "3 commits ahead of origin/main") {
		t.Errorf("the plan must say what is at stake:\n%s", out)
	}
}

func TestSweepPlanWithNoOriginComparesWithLocalTrunk(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	branchWithWork(t, main, "done-work", 0)

	p := planOf(t, ctx)
	if len(p.Bases) != 1 || p.Bases[0].Name != "main" {
		t.Fatalf("bases = %+v", p.Bases)
	}
	if !slices.Equal(branchNames(p.Delete), []string{"done-work"}) || p.Delete[0].MergedInto != "main" {
		t.Errorf("delete = %+v", p.Delete)
	}
}

// Commit subjects run long. On a terminal they are cut so each row fits;
// piped, they are printed whole.
func TestSweepPlanFitsSubjectsToTheTerminal(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	subject := "a commit subject long enough to push its row past the edge of a narrow terminal"
	tree := gitOut(t, main, "rev-parse", "main^{tree}")
	gitIn(t, main, "branch", "done-work", gitOut(t, main, "commit-tree", "-p", "main", "-m", subject, tree))
	gitIn(t, main, "merge", "-q", "--ff-only", "done-work")
	p := planOf(t, ctx)

	var buf bytes.Buffer
	p.Render(&buf, 80)
	for _, line := range strings.Split(buf.String(), "\n") {
		if n := len([]rune(line)); n > 80 {
			t.Errorf("a %d-column line on an 80-column terminal: %q", n, line)
		}
	}
	if !strings.Contains(buf.String(), "done-work") || !strings.Contains(buf.String(), "…") {
		t.Errorf("want the branch whole and its subject cut:\n%s", buf.String())
	}
	if out := rendered(p); !strings.Contains(out, subject) {
		t.Errorf("piped, the subject is printed whole:\n%s", out)
	}
}

func TestSweepPlanWithNothingMergedSaysSo(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	if out := rendered(planOf(t, ctx)); !strings.Contains(out, "No merged branches or worktrees to sweep.") {
		t.Errorf("an empty plan must say so:\n%s", out)
	}
}

func TestSweepRefusesOutsideTheMainCheckout(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	var buf bytes.Buffer
	path, err := New(ctx, "fix/login-crash", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	inWorktree, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	err = Sweep(inWorktree, SweepOptions{Yes: true}, &buf)
	if err == nil {
		t.Fatal("want a refusal from inside a worktree")
	}
	if !strings.Contains(err.Error(), "main checkout") || !strings.Contains(err.Error(), "wt cd /") {
		t.Errorf("the refusal must say where to run it: %v", err)
	}
	if !ctx.Repo.BranchExists("done-work") {
		t.Error("nothing may be deleted by a refused sweep")
	}
}

func TestSweepRunsFromASubdirectoryOfTheMainCheckout(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	mustWrite(t, filepath.Join(main, "sub", "notes.txt"), "scratch")
	inSub, err := Open(filepath.Join(main, "sub"))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Sweep(inSub, SweepOptions{NoFetch: true, Yes: true}, &buf); err != nil {
		t.Fatalf("a subdirectory of the main checkout is the main checkout: %v", err)
	}
	if ctx.Repo.BranchExists("done-work") {
		t.Error("done-work should have been deleted")
	}
}

func TestSweepRefusesABareRepository(t *testing.T) {
	src := committedRepo(t, minimalConf)
	bare := filepath.Join(t.TempDir(), "demo.git")
	gitIn(t, src, "clone", "-q", "--bare", src, bare)
	r, err := repo.Discover(bare)
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.FromRaw(map[string]config.Value{"MAIN_BRANCH": {Scalar: "main"}}, "main")
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err = Sweep(&Context{Repo: r, Config: c}, SweepOptions{NoFetch: true, Yes: true}, &buf)
	if err == nil || !strings.Contains(err.Error(), "bare") {
		t.Fatalf("want a refusal naming the bare repository, got %v", err)
	}
}

// Without MAIN_BRANCH or origin's HEAD, trunk is whatever is checked out.
func TestSweepRefusesWhenTrunkIsOnlyAGuess(t *testing.T) {
	main := committedRepo(t, "BUILD_INIT_ENABLED=false\n")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	branchWithWork(t, main, "done-work", 0)

	var buf bytes.Buffer
	err = Sweep(ctx, SweepOptions{Yes: true}, &buf)
	if err == nil || !strings.Contains(err.Error(), "MAIN_BRANCH") {
		t.Fatalf("want a refusal saying how to name trunk, got %v", err)
	}
	if !ctx.Repo.BranchExists("done-work") {
		t.Error("nothing may be deleted by a refused sweep")
	}
}

func TestSweepTakesOriginHeadAsTrunkWhenTheConfigurationNamesNone(t *testing.T) {
	ctx, main, _ := sweepRepoWith(t, "BUILD_INIT_ENABLED=false\n")
	branchWithWork(t, main, "done-work", 0)

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true}, &buf); err != nil {
		t.Fatalf("origin's HEAD anchors trunk: %v\n%s", err, buf.String())
	}
	if ctx.Repo.BranchExists("done-work") {
		t.Error("done-work should have been deleted")
	}
}

func TestSweepYesDeletesAndSaysHowToBringItBack(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")
	tip := gitOut(t, main, "rev-parse", "done-work")

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if ctx.Repo.BranchExists("done-work") {
		t.Error("done-work is merged; --yes should have deleted it")
	}
	if !strings.Contains(buf.String(), "git branch done-work "+tip[:12]) {
		t.Errorf("each deletion must say how to undo it:\n%s", buf.String())
	}
}

// A bulk delete does not happen because stdin was closed.
func TestSweepWithoutATerminalDeletesNothing(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{}, &buf); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !ctx.Repo.BranchExists("done-work") {
		t.Error("with no terminal and no --yes, nothing may be deleted")
	}
	if !strings.Contains(buf.String(), "--yes") {
		t.Errorf("say how to go ahead:\n%s", buf.String())
	}
}

func TestSweepDeclinedDeletesNothingAndTheQuestionSeesThePlan(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)

	var seen SweepPlan
	opts := SweepOptions{Confirm: func(p SweepPlan) (bool, error) { seen = p; return false, nil }}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !ctx.Repo.BranchExists("done-work") {
		t.Error("declining must delete nothing")
	}
	if !slices.Equal(branchNames(seen.Delete), []string{"done-work"}) {
		t.Errorf("the question must see the plan it asks about: %v", branchNames(seen.Delete))
	}
	if !strings.Contains(buf.String(), "Nothing was swept.") {
		t.Errorf("say so:\n%s", buf.String())
	}
}

func TestSweepWithNothingToDeleteAsksNothing(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "wip", 1)

	asked := false
	opts := SweepOptions{Confirm: func(SweepPlan) (bool, error) { asked = true; return true, nil }}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if asked {
		t.Error("there was nothing to confirm")
	}
}

// The prompt can stay open while another session commits to a branch the plan
// called merged. That branch is kept; the others still go.
func TestSweepKeepsABranchThatMovedWhileThePromptWasOpen(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	branchWithWork(t, main, "also-done", 0)

	opts := SweepOptions{Confirm: func(SweepPlan) (bool, error) {
		addWork(t, main, "done-work", 1)
		return true, nil
	}}
	var buf bytes.Buffer
	err := Sweep(ctx, opts, &buf)
	if err == nil {
		t.Fatal("a kept branch must make the sweep fail")
	}
	if !ctx.Repo.BranchExists("done-work") {
		t.Fatal("a commit that landed after the plan was deleted with its branch")
	}
	if ctx.Repo.BranchExists("also-done") {
		t.Error("an unchanged branch should still have been deleted")
	}
	if !strings.Contains(buf.String(), "- kept done-work: it moved after the plan was made") {
		t.Errorf("say why it was kept:\n%s", buf.String())
	}
}

func TestSweepKeepsABranchCheckedOutWhileThePromptWasOpen(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)

	opts := SweepOptions{Agents: []wtsync.Agent{}, Confirm: func(SweepPlan) (bool, error) {
		gitIn(t, main, "worktree", "add", "-q", filepath.Join(ctx.Repo.Parent, "late"), "done-work")
		return true, nil
	}}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err == nil {
		t.Fatal("a kept branch must make the sweep fail")
	}
	if !ctx.Repo.BranchExists("done-work") {
		t.Fatal("a branch checked out after the plan was deleted")
	}
	if !strings.Contains(buf.String(), "- kept done-work: it was checked out in ") {
		t.Errorf("say why it was kept:\n%s", buf.String())
	}
}

// update-ref -d does not refuse a checked-out branch, so apply asks the
// worktrees once more right before each delete. A reference-transaction hook
// checks the next branch out the moment the previous delete commits, which is
// the only window that check covers.
func TestSweepKeepsABranchCheckedOutBetweenTwoDeletes(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "also-done", 0)
	branchWithWork(t, main, "done-work", 0)
	late := filepath.Join(ctx.Repo.Parent, "late")
	hooks := filepath.Join(t.TempDir(), "hooks")
	hook := filepath.Join(hooks, "reference-transaction")
	mustWrite(t, hook, "#!/bin/sh\n"+
		"[ \"$1\" = committed ] || exit 0\n"+
		"grep -q ' refs/heads/also-done$' || exit 0\n"+
		"unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE\n"+
		"git -C '"+main+"' worktree add -q '"+late+"' done-work\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}

	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, Confirm: func(SweepPlan) (bool, error) {
		gitIn(t, main, "config", "core.hooksPath", hooks)
		return true, nil
	}}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err == nil {
		t.Fatal("a kept branch must make the sweep fail")
	}
	if ctx.Repo.BranchExists("also-done") {
		t.Error("also-done was unchanged and should have been deleted")
	}
	if !ctx.Repo.BranchExists("done-work") {
		t.Fatal("a branch checked out between two deletes was deleted")
	}
	if !strings.Contains(buf.String(), "checked out after the plan was made") {
		t.Errorf("the per-delete check must be the one that kept it:\n%s", buf.String())
	}
}

// The re-check reads trunk again: a trunk reset while the prompt was open
// takes the merge with it.
func TestSweepKeepsABranchWhoseMergeWasUndoneWhileThePromptWasOpen(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "local-only", 1)
	gitIn(t, main, "merge", "-q", "--no-ff", "-m", "merge local-only", "local-only")

	opts := SweepOptions{NoFetch: true, Confirm: func(SweepPlan) (bool, error) {
		gitIn(t, main, "reset", "-q", "--hard", "main^1")
		return true, nil
	}}
	var buf bytes.Buffer
	if err := Sweep(ctx, opts, &buf); err == nil {
		t.Fatal("a kept branch must make the sweep fail")
	}
	if !ctx.Repo.BranchExists("local-only") {
		t.Fatal("a branch whose only merge was reset away was deleted")
	}
	if !strings.Contains(buf.String(), "- kept local-only: trunk no longer contains it") {
		t.Errorf("say why it was kept:\n%s", buf.String())
	}
}

func TestSweepFetchesAndPrunesFirst(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	branchWithWork(t, main, "landed", 1)
	// To the path: a push by remote name would update origin/main without a fetch.
	gitIn(t, main, "push", "-q", origin, "landed:main")
	branchWithWork(t, main, "old-idea", 2)
	gitIn(t, main, "push", "-q", "-u", "origin", "old-idea")
	gitIn(t, origin, "branch", "-D", "old-idea") // deleted on the remote, as a merge would

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if ctx.Repo.BranchExists("landed") {
		t.Error("landed is on origin/main once fetched; it should be gone")
	}
	out := buf.String()
	if !strings.Contains(out, "old-idea") || !strings.Contains(out, "2 commits ahead of origin/main") {
		t.Errorf("the prune must reveal old-idea's deleted upstream:\n%s", out)
	}
	if !ctx.Repo.BranchExists("old-idea") {
		t.Error("unmerged work is never deleted")
	}
}

// A remote.origin.fetch mapping into refs/heads would let a plain fetch
// --prune delete local branches before anything was asked.
func TestSweepFetchCannotPruneLocalBranchesOrTags(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	gitIn(t, main, "config", "--add", "remote.origin.fetch", "+refs/heads/release/*:refs/heads/release/*")
	gitIn(t, main, "config", "fetch.pruneTags", "true")
	gitIn(t, main, "branch", "release/keep")
	gitIn(t, main, "tag", "local-tag")
	gitIn(t, origin, "tag", "remote-tag", "main")

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if !ctx.Repo.BranchExists("release/keep") {
		t.Error("the fetch pruned a local branch")
	}
	if _, ok := ctx.Repo.ResolveRef("refs/tags/local-tag"); !ok {
		t.Error("the fetch pruned a local tag")
	}
	if _, ok := ctx.Repo.ResolveRef("refs/tags/remote-tag"); ok {
		t.Error("the fetch wrote a tag")
	}
}

func TestSweepWithNoOriginComparesWithLocalBranchesOnly(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	branchWithWork(t, main, "done-work", 0)

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if ctx.Repo.BranchExists("done-work") {
		t.Error("done-work is merged into main; it should be gone")
	}
	if !strings.Contains(buf.String(), "no origin remote") {
		t.Errorf("say there is no origin to fetch:\n%s", buf.String())
	}
}

func TestSweepStopsWhenTheFetchFails(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	branchWithWork(t, main, "done-work", 0)
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := Sweep(ctx, SweepOptions{Yes: true}, &buf)
	if err == nil || !strings.Contains(err.Error(), "--no-fetch") {
		t.Fatalf("want a fetch error that names --no-fetch, got %v", err)
	}
	if !ctx.Repo.BranchExists("done-work") {
		t.Error("nothing may be deleted after a failed fetch")
	}
}

func TestSweepNoFetchComparesWithOriginAsLastFetched(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	branchWithWork(t, main, "landed", 1)
	// To the path: a push by remote name would update origin/main without a fetch.
	gitIn(t, main, "push", "-q", origin, "landed:main")

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{NoFetch: true, Yes: true}, &buf); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !ctx.Repo.BranchExists("landed") {
		t.Error("--no-fetch must not see what was never fetched")
	}
	if !strings.Contains(buf.String(), "not fetched") {
		t.Errorf("say it did not fetch:\n%s", buf.String())
	}
}
