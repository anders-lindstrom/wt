package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// 5. A merged branch is deleted.
func TestRemoveDeletesMergedBranch(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if _, err := New(ctx, "fix/merged", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, "fix/merged", RemoveOptions{}, &buf); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ctx.Repo.BranchExists("fix_wt/merged") {
		t.Error("merged branch should have been deleted")
	}
}

// 6. An unmerged branch is renamed to strip the prefix, never deleted.
func TestRemoveRenamesUnmergedBranch(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/unmerged", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")

	if err := Remove(ctx, "fix/unmerged", RemoveOptions{}, &buf); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ctx.Repo.BranchExists("fix_wt/unmerged") {
		t.Error("prefixed branch should be gone")
	}
	if !ctx.Repo.BranchExists("unmerged") {
		t.Error("unmerged work must be kept under the stripped name")
	}
}

// 2. A detached worktree has no branch to clean up; touch none.
func TestRemoveDetachedTouchesNoBranch(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	dst := filepath.Join(ctx.Repo.Parent, "detached")
	gitIn(t, main, "worktree", "add", "-q", "--detach", dst)

	var buf bytes.Buffer
	if err := RemoveAt(ctx, dst, RemoveOptions{}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("worktree should be gone")
	}
	if !strings.Contains(buf.String(), "no branch") {
		t.Errorf("want a note about having no branch:\n%s", buf.String())
	}
}

// 3. A branch this tooling does not own, carrying work of its own, is never
// deleted or renamed.
func TestRemoveLeavesAnUnmergedForeignBranchAlone(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	dst := foreignWorktree(t, ctx, main, "someones-work", 3)

	var buf bytes.Buffer
	if err := RemoveAt(ctx, dst, RemoveOptions{}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if !ctx.Repo.BranchExists("someones-work") {
		t.Fatal("a branch this tooling did not create must survive removal")
	}
	if !strings.Contains(buf.String(), "3 commits ahead of main") {
		t.Errorf("the plan must say how much work is on it:\n%s", buf.String())
	}
}

// foreignWorktree makes a worktree on a branch outside the convention, with
// ahead commits of its own on top of the main branch.
func foreignWorktree(t *testing.T, ctx *Context, main, branch string, ahead int) string {
	t.Helper()
	gitIn(t, main, "branch", branch)
	dst := filepath.Join(ctx.Repo.Parent, branch)
	gitIn(t, main, "worktree", "add", "-q", dst, branch)
	for i := 0; i < ahead; i++ {
		gitIn(t, dst, "commit", "-q", "--allow-empty", "-m", "theirs")
	}
	return dst
}

// Merged means nothing is lost, whoever made the branch — and `git branch -d`
// refuses anything else anyway. Leaving a merged branch behind because wt did
// not create it just leaves litter nobody will ever clean up.
func TestRemoveDeletesAMergedBranchWhoeverMadeIt(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	dst := foreignWorktree(t, ctx, main, "someones-work", 0)

	var buf bytes.Buffer
	if err := RemoveAt(ctx, dst, RemoveOptions{}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if ctx.Repo.BranchExists("someones-work") {
		t.Error("a merged branch should have been deleted")
	}
	if !strings.Contains(buf.String(), "merged into main") {
		t.Errorf("the plan must say why it may be deleted:\n%s", buf.String())
	}
}

// The merge state is the fact a removal turns on, so it is stated for every
// branch — not only for the ones wt made.
func TestRemovePlanStatesTheMergeStateOfAForeignBranch(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	dst := foreignWorktree(t, ctx, main, "someones-work", 1)

	var buf bytes.Buffer
	opts := RemoveOptions{Confirm: func(Plan) (bool, error) { return false, nil }}
	if err := RemoveAt(ctx, dst, opts, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "not merged: 1 commit ahead of main") {
		t.Errorf("want the merge state and the count, singular:\n%s", out)
	}
	if strings.Contains(out, "1 commits") {
		t.Errorf("one commit is not 1 commits:\n%s", out)
	}
}

// 1. The branch is read from the worktree, not rebuilt from the work name.
func TestRemoveReadsBranchFromWorktree(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/renamed", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	// Rename the branch by hand: name and branch now disagree.
	gitIn(t, path, "branch", "-m", "fix_wt/renamed", "spike_wt/actually")

	if err := RemoveAt(ctx, path, RemoveOptions{}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if ctx.Repo.BranchExists("spike_wt/actually") {
		t.Error("the branch actually checked out should have been handled")
	}
	if ctx.Repo.BranchExists("fix_wt/renamed") {
		t.Error("the rebuilt-from-name branch must never be resurrected")
	}
}

// 4. A worktree with submodules cannot go through `git worktree remove`.
func TestRemoveHandlesSubmodulesWorktree(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/subs", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(path, ".gitmodules"), "")

	if err := RemoveAt(ctx, path, RemoveOptions{}, &buf); err != nil {
		t.Fatalf("RemoveAt with submodules: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("worktree should be gone")
	}
}

func TestRemoveUnknownWorkIsAnError(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if err := Remove(ctx, "fix/nope", RemoveOptions{}, &buf); err == nil {
		t.Error("want an error removing work that does not exist")
	}
}

// The regression this whole change exists for: the WORK column of `wt list` is
// a valid argument whatever type the worktree is. The old resolution rebuilt a
// branch from the default type and reported a path that never existed.
func TestRemoveAcceptsWorkNameOfAnyType(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if _, err := New(ctx, "chore/wt-migration", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}

	if err := Remove(ctx, "wt-migration", RemoveOptions{}, &buf); err != nil {
		t.Fatalf("Remove by bare work name: %v", err)
	}
	if ctx.Repo.BranchExists("chore_wt/wt-migration") {
		t.Error("the chore worktree's branch should have been handled")
	}
}

// Removal is destructive and its branch outcome is the surprising part, so the
// plan is printed before anything happens — with or without a confirmation.
func TestRemovePlanNamesUnmergedBranchOutcome(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/unmerged-plan", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	buf.Reset()

	if err := Remove(ctx, "fix/unmerged-plan", RemoveOptions{}, &buf); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	out := buf.String()
	for _, want := range []string{path, "fix_wt/unmerged-plan",
		"not merged: 1 commit ahead of main", "unmerged-plan"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan should mention %q:\n%s", want, out)
		}
	}
}

func TestRemovePlanNamesMergedBranchOutcome(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	if _, err := New(ctx, "fix/merged-plan", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	buf.Reset()

	if err := Remove(ctx, "fix/merged-plan", RemoveOptions{}, &buf); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"merged into main", "deleted"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan should mention %q:\n%s", want, out)
		}
	}
}

// git itself refuses to remove a dirty checkout, so the plan's job is to say so
// before the attempt rather than leaving the user to read it out of a failure.
func TestRemovePlanReportsUncommittedChanges(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/dirty", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(path, "scratch.txt"), "unsaved")
	buf.Reset()

	if err := Remove(ctx, "fix/dirty", RemoveOptions{}, &buf); err == nil {
		t.Fatal("git refuses to remove a dirty worktree; want that error")
	}
	if !strings.Contains(buf.String(), "uncommitted") {
		t.Errorf("plan should report uncommitted changes:\n%s", buf.String())
	}
}

// Declining leaves the checkout and the branch exactly as they were.
func TestRemoveDeclinedChangesNothing(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/kept", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}

	opts := RemoveOptions{Confirm: func(Plan) (bool, error) { return false, nil }}
	if err := Remove(ctx, "fix/kept", opts, &buf); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("worktree should still be there after declining")
	}
	if !ctx.Repo.BranchExists("fix_wt/kept") {
		t.Error("branch should still be there after declining")
	}
}

// The confirmation sees the same facts the plan prints, so a caller can render
// its own prompt without recomputing them.
func TestRemoveConfirmReceivesThePlan(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/inspected", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}

	var got Plan
	opts := RemoveOptions{Confirm: func(p Plan) (bool, error) { got = p; return false, nil }}
	if err := Remove(ctx, "fix/inspected", opts, &buf); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got.Path != path {
		t.Errorf("plan path = %q, want %q", got.Path, path)
	}
	if got.Branch != "fix_wt/inspected" {
		t.Errorf("plan branch = %q, want fix_wt/inspected", got.Branch)
	}
}

// A branch this tooling did not create is still named in the plan, so the
// confirmation identifies what is being removed; only the outcome says it is
// left alone.
func TestRemovePlanNamesForeignBranch(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	dst := foreignWorktree(t, ctx, main, "someones-work", 2)

	var got Plan
	var buf bytes.Buffer
	opts := RemoveOptions{Confirm: func(p Plan) (bool, error) { got = p; return false, nil }}
	if err := RemoveAt(ctx, dst, opts, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if got.Branch != "someones-work" {
		t.Errorf("plan branch = %q, want someones-work", got.Branch)
	}
	if got.Outcome != BranchUntouched {
		t.Errorf("plan outcome = %v, want BranchUntouched", got.Outcome)
	}
	if !strings.Contains(buf.String(), "not created by wt and not merged") {
		t.Errorf("rendered plan should name the branch and the reason, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "someones-work — not merged: 2 commits ahead of main") {
		t.Errorf("rendered plan should state the merge state, got:\n%s", buf.String())
	}
}

// Facts can change while the prompt is open: another session can land commits
// on the branch. The plan the user confirmed is the plan that runs, or nothing
// runs.
func TestRemoveRefusesWhenFactsChangedDuringConfirmation(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/racing", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}

	opts := RemoveOptions{Confirm: func(p Plan) (bool, error) {
		if p.Outcome != BranchDeleted {
			t.Fatalf("precondition: expected a merged branch, got outcome %v", p.Outcome)
		}
		gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "landed while the prompt was open")
		return true, nil
	}}
	if err := Remove(ctx, "fix/racing", opts, &buf); err == nil {
		t.Fatal("expected an error when the plan changed under the prompt")
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("worktree should still be there")
	}
	if !ctx.Repo.BranchExists("fix_wt/racing") {
		t.Error("branch should still be there")
	}
	if !strings.Contains(buf.String(), "Nothing was removed.") {
		t.Errorf("should say nothing was removed, got:\n%s", buf.String())
	}
}

// `git branch -d` measures "merged" against whatever the main checkout is
// standing on, which in this layout is rarely the main branch — so it refuses
// branches that are merged into the branch that matters. wt has already asked
// the right question by the time it deletes, so the plan is carried out rather
// than second-guessed by a comparison against an unrelated branch.
func TestRemoveDeletesAMergedBranchWithTheMainCheckoutElsewhere(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	gitIn(t, main, "branch", "sidetrack")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "on main")
	dst := foreignWorktree(t, ctx, main, "someones-work", 0)
	// The main checkout moves off main, behind the branch being removed, which
	// is what makes `git branch -d` refuse.
	gitIn(t, main, "switch", "-q", "sidetrack")

	var buf bytes.Buffer
	if err := RemoveAt(ctx, dst, RemoveOptions{}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if ctx.Repo.BranchExists("someones-work") {
		t.Error("the branch was merged into main; the plan said it would go")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("the worktree should be gone")
	}
}

// An unmerged branch is never on this path: the plan reaches BranchDeleted
// only after wt has compared it against the main branch itself.
func TestRemoveStillKeepsUnmergedWorkWhenTheMainCheckoutIsElsewhere(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	gitIn(t, main, "branch", "sidetrack")
	dst := foreignWorktree(t, ctx, main, "someones-work", 2)
	gitIn(t, main, "switch", "-q", "sidetrack")

	var buf bytes.Buffer
	if err := RemoveAt(ctx, dst, RemoveOptions{}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if !ctx.Repo.BranchExists("someones-work") {
		t.Fatal("two commits of somebody else's work were deleted")
	}
}

// A confirmed removal re-reads the whole plan before acting; --yes and a hook
// go straight from the plan to the delete, and the delete no longer asks git
// for a second opinion. So the merged answer is checked once more, in the
// window where it can go stale, before a branch is destroyed.
func TestRemoveDoesNotDeleteABranchThatGainedWorkAfterThePlan(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	dst := foreignWorktree(t, ctx, main, "someones-work", 0)

	plan := planFor(ctx, dst, RemoveOptions{Agents: []wtsync.Agent{}})
	if plan.Outcome != BranchDeleted {
		t.Fatalf("precondition: want a merged branch, got outcome %v", plan.Outcome)
	}
	// Another session lands a commit in the window the plan cannot see.
	gitIn(t, dst, "commit", "-q", "--allow-empty", "-m", "landed in the window")

	var buf bytes.Buffer
	err := plan.apply(ctx, &buf)
	if err == nil {
		t.Fatal("want a refusal once the branch is no longer merged")
	}
	if !ctx.Repo.BranchExists("someones-work") {
		t.Fatal("a commit that landed after the plan was deleted with the branch")
	}
	if !strings.Contains(err.Error(), "1 commit ahead of main") {
		t.Errorf("the message must say what it found instead: %v", err)
	}
	if !strings.Contains(buf.String(), "worktree removed") {
		t.Errorf("the worktree did go by then; say so:\n%s", buf.String())
	}
}

// The other half of the guard: the main branch itself can move out from under
// a run, and a comparison that cannot be made is not a merge.
func TestRemoveDoesNotDeleteWhenTheMainBranchDisappears(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	dst := foreignWorktree(t, ctx, main, "someones-work", 0)
	plan := planFor(ctx, dst, RemoveOptions{Agents: []wtsync.Agent{}})

	gitIn(t, main, "branch", "-m", "main", "renamed-trunk")

	var buf bytes.Buffer
	err := plan.apply(ctx, &buf)
	if err == nil {
		t.Fatal("want a refusal when the comparison cannot be made")
	}
	if !ctx.Repo.BranchExists("someones-work") {
		t.Fatal("the branch was deleted on an answer nobody could check")
	}
}

// deadPid returns a pid that has certainly exited: a process run to
// completion. Nothing else can be assumed dead, and inventing a number risks
// naming somebody else's process.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("running true: %v", err)
	}
	return cmd.Process.Pid
}

func lockedWorktree(t *testing.T, ctx *Context, main, work, reason string) string {
	t.Helper()
	var buf bytes.Buffer
	path, err := New(ctx, work, NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "worktree", "lock", "--reason", reason, path)
	return path
}

// Claude Code locks the worktree its session lives in and names the session
// and its pid in the reason. Once that session is over the lock is litter,
// and nobody should have to learn `git worktree unlock` to get past it.
func TestRemoveReleasesAStaleLockAndRemoves(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	pid := deadPid(t)
	reason := fmt.Sprintf("claude session gone (pid %d start Thu Sep 10 04:58:38 2026)", pid)
	path := lockedWorktree(t, ctx, main, "fix/gone", reason)

	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Agents: []wtsync.Agent{}}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the worktree should be gone")
	}
	out := buf.String()
	if !strings.Contains(out, "stale") || !strings.Contains(out, reason) {
		t.Errorf("the plan must show the lock and call it stale:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("pid %d is gone", pid)) {
		t.Errorf("the plan must say how it knows:\n%s", out)
	}
}

// A lock whose holder is still running is the one case where the answer is
// no — before any question is asked, because the answer does not depend on it.
func TestRemoveRefusesALockHeldByARunningProcess(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	reason := fmt.Sprintf("claude session live (pid %d start Thu Sep 10 04:58:38 2026)", os.Getpid())
	path := lockedWorktree(t, ctx, main, "fix/live", reason)

	asked := false
	opts := RemoveOptions{
		Agents:  []wtsync.Agent{},
		Confirm: func(Plan) (bool, error) { asked = true; return true, nil },
	}
	var buf bytes.Buffer
	err := RemoveAt(ctx, path, opts, &buf)
	if err == nil {
		t.Fatal("want a refusal while the holder is running")
	}
	if asked {
		t.Error("nothing should be asked when the answer is already no")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("the refusal must name the way past it: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("the worktree must still be there")
	}
	if !strings.Contains(buf.String(), reason) {
		t.Errorf("the plan must show whose lock it is:\n%s", buf.String())
	}
}

func TestRemoveForceBreaksALockHeldByARunningProcess(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	reason := fmt.Sprintf("claude session live (pid %d)", os.Getpid())
	path := lockedWorktree(t, ctx, main, "fix/live", reason)

	var buf bytes.Buffer
	opts := RemoveOptions{Force: true, Agents: []wtsync.Agent{}}
	if err := RemoveAt(ctx, path, opts, &buf); err != nil {
		t.Fatalf("RemoveAt --force: %v\n%s", err, buf.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("--force should have removed it")
	}
	if !strings.Contains(buf.String(), "broke the lock") {
		t.Errorf("--force must say what it overrode:\n%s", buf.String())
	}
}

// A lock with no pid in it says nothing about who holds it, so the sessions
// wt can see are the second opinion.
func TestRemoveTreatsALockWithAnAgentInTheWorktreeAsHeld(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	path := lockedWorktree(t, ctx, main, "fix/agented", "held by something")
	resolved, _ := filepath.EvalSymlinks(path)

	var buf bytes.Buffer
	opts := RemoveOptions{Agents: []wtsync.Agent{{Name: "agented-7", Cwd: resolved}}}
	err := RemoveAt(ctx, path, opts, &buf)
	if err == nil {
		t.Fatal("want a refusal: a session is living in it")
	}
	if !strings.Contains(buf.String(), "agented-7") {
		t.Errorf("the plan must name the session it found:\n%s", buf.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("the worktree must still be there")
	}
}

// A lock nobody claims and nothing explains is still a lock somebody took on
// purpose: assume it is held.
func TestRemoveRefusesALockNothingExplains(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	path := lockedWorktree(t, ctx, main, "fix/mystery", "reasons")

	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Agents: []wtsync.Agent{}}, &buf); err == nil {
		t.Fatal("want a refusal")
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("the worktree must still be there")
	}
}

// git's own answer for a locked worktree tells the reader to run
// `remove -f -f`, which is not a wt command and not what they should type.
func TestRemoveNeverRepeatsGitsAdviceAboutMinusFMinusF(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	reason := fmt.Sprintf("claude session live (pid %d)", os.Getpid())
	path := lockedWorktree(t, ctx, main, "fix/live", reason)

	var buf bytes.Buffer
	err := RemoveAt(ctx, path, RemoveOptions{Agents: []wtsync.Agent{}}, &buf)
	said := buf.String()
	if err != nil {
		said += err.Error()
	}
	if strings.Contains(said, "-f -f") {
		t.Errorf("git's advice must not reach the user:\n%s", said)
	}
}
