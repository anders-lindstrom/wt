package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// 3. A branch this tooling does not own is never deleted or renamed.
func TestRemoveLeavesForeignBranchAlone(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	gitIn(t, main, "branch", "someones-work")
	dst := filepath.Join(ctx.Repo.Parent, "foreign")
	gitIn(t, main, "worktree", "add", "-q", dst, "someones-work")

	var buf bytes.Buffer
	if err := RemoveAt(ctx, dst, RemoveOptions{}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v", err)
	}
	if !ctx.Repo.BranchExists("someones-work") {
		t.Fatal("a branch this tooling did not create must survive removal")
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
	for _, want := range []string{path, "fix_wt/unmerged-plan", "not merged into main", "unmerged-plan"} {
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
	gitIn(t, main, "branch", "someones-work")
	dst := filepath.Join(ctx.Repo.Parent, "foreign")
	gitIn(t, main, "worktree", "add", "-q", dst, "someones-work")

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
	if !strings.Contains(buf.String(), "someones-work — not created by wt") {
		t.Errorf("rendered plan should name the branch and the reason, got:\n%s", buf.String())
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
