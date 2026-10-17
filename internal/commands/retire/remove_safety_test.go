package retire

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// refused runs a removal that must not happen and checks nothing went.
func refused(t *testing.T, ctx *kit.Context, path string, opts RemoveOptions, why string) string {
	t.Helper()
	var buf bytes.Buffer
	err := RemoveAt(ctx, path, opts, &buf)
	if err == nil {
		t.Fatalf("want a refusal (%s):\n%s", why, buf.String())
	}
	if _, serr := os.Stat(path); serr != nil {
		t.Fatalf("the worktree must still be there (%s)", why)
	}
	if !strings.Contains(buf.String()+err.Error(), why) {
		t.Errorf("want %q in what it said:\n%s\n%v", why, buf.String(), err)
	}
	return buf.String()
}

func TestRemoveRefusesADirtyWorktreeBeforeAsking(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/dirty")
	testkit.MustWrite(t, filepath.Join(path, "scratch.txt"), "mine")
	asked := false
	opts := RemoveOptions{Confirm: func(Plan) (bool, error) { asked = true; return true, nil }}
	refused(t, ctx, path, opts, "uncommitted changes")
	if asked {
		t.Error("a removal that will refuse must not ask first")
	}
}

// --force breaks a lock and walks past a session; it never throws work away.
func TestRemoveForceDoesNotDeleteUncommittedWork(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/dirty-forced")
	testkit.MustWrite(t, filepath.Join(path, "scratch.txt"), "mine")
	refused(t, ctx, path, RemoveOptions{Force: kit.ForceAll}, "uncommitted changes")
}

func TestRemoveRefusesWhenTheStatusCannotBeRead(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/unreadable")
	testkit.MustWrite(t, filepath.Join(testkit.GitDirFor(t, path), "index"), "garbage")
	refused(t, ctx, path, RemoveOptions{Force: kit.ForceAll}, "cannot read its status")
}

func TestRemoveRefusesDuringEachOperation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, name string }{
		{"rebase-merge/head-name", "rebase"},
		{"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
		{"BISECT_LOG", "bisect"},
		{"rebase-apply/head-name", "rebase"},
		{"sequencer/todo", "cherry-pick or revert"},
	} {
		t.Run(tc.name+" "+tc.file, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/op")
			testkit.MustWrite(t, filepath.Join(testkit.GitDirFor(t, path), tc.file), "x\n")
			refused(t, ctx, path, RemoveOptions{Force: kit.ForceAll}, "a "+tc.name+" is in progress")
		})
	}
}

func TestRemoveRefusesADetachedHeadWithCommitsNothingElseHas(t *testing.T) {
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main)
	dst := filepath.Join(ctx.Repo.Parent, "detached")
	testkit.GitIn(t, main, "worktree", "add", "-q", "--detach", dst)
	testkit.GitIn(t, dst, "commit", "-q", "--allow-empty", "-m", "only here")
	refused(t, ctx, dst, RemoveOptions{Force: kit.ForceAll}, "1 commit on its HEAD")
}

// A branch GitHub squash-merged is deleted, and its commits then hang on
// nothing: the plan lists the tip, so it can be put back.
func TestRemoveListsTheTipADeletedBranchLeavesUnreachable(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/squashed")
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	opts := RemoveOptions{Landed: func(string, string) int { return 7 }}

	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, opts, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "git branch fix_wt/squashed "+tip[:12]) {
		t.Errorf("the plan must say how to get the lost commit back:\n%s", buf.String())
	}
}

// The same tip held by another branch is not lost, so nothing is listed.
func TestRemoveListsNothingWhenAnotherRefHoldsTheTip(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/squashed-kept")
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	testkit.GitIn(t, path, "branch", "backup")
	opts := RemoveOptions{Landed: func(string, string) int { return 7 }}

	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, opts, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if strings.Contains(buf.String(), "on no branch") || strings.Contains(buf.String(), "restores its commits") {
		t.Errorf("backup still holds the commit:\n%s", buf.String())
	}
}

func TestRemoveRefusesASessionInTheWorktree(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/visited")
	resolved, _ := filepath.EvalSymlinks(path)
	opts := RemoveOptions{Agents: []wtsync.Agent{{Name: "parked-1", Cwd: resolved, Status: "idle"}}}
	refused(t, ctx, path, opts, "parked-1")
}

func TestRemoveForceGoesPastASession(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/visited-forced")
	resolved, _ := filepath.EvalSymlinks(path)
	opts := RemoveOptions{Force: kit.ForceAll, Agents: []wtsync.Agent{{Name: "parked-1", Cwd: resolved}}}
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, opts, &buf); err != nil {
		t.Fatalf("RemoveAt --force: %v\n%s", err, buf.String())
	}
}

func TestRemoveRefusesWhenTheSessionsCannotBeListed(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/unknown")
	opts := RemoveOptions{AgentsErr: errors.New("claude agents --json failed: boom")}
	refused(t, ctx, path, opts, "cannot list agent sessions")

	opts.Force = kit.ForceAll
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, opts, &buf); err != nil {
		t.Fatalf("--force goes past a listing that failed: %v\n%s", err, buf.String())
	}
}

func TestRemoveRefusesFilesStatusIsToldNotToLookAt(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/hidden")
	testkit.MustWrite(t, filepath.Join(path, "a.txt"), "a")
	testkit.GitIn(t, path, "add", "a.txt")
	testkit.GitIn(t, path, "commit", "-qm", "a")
	testkit.GitIn(t, path, "update-index", "--assume-unchanged", "a.txt")
	refused(t, ctx, path, RemoveOptions{}, "a.txt")

	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Force: kit.ForceAll}, &buf); err != nil {
		t.Fatalf("--force goes past an unedited assume-unchanged file: %v\n%s", err, buf.String())
	}
}

// An edit status is told not to look at is still an edit, and --force does
// not delete it.
func TestRemoveForceDoesNotDeleteAnEditedHiddenFile(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/hidden-edited")
	testkit.MustWrite(t, filepath.Join(path, "a.txt"), "a")
	testkit.GitIn(t, path, "add", "a.txt")
	testkit.GitIn(t, path, "commit", "-qm", "a")
	testkit.GitIn(t, path, "update-index", "--skip-worktree", "a.txt")
	testkit.MustWrite(t, filepath.Join(path, "a.txt"), "edited")
	refused(t, ctx, path, RemoveOptions{Force: kit.ForceAll}, "uncommitted changes")
}

// Claude Code puts its own worktrees under .claude/worktrees/ inside the
// checkout, usually ignored: deleting the outer one would take the inner
// one's work with it.
func TestRemoveRefusesAWorktreeWithAnotherInsideIt(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/outer")
	testkit.MustWrite(t, filepath.Join(path, ".gitignore"), ".claude/\n")
	testkit.GitIn(t, path, "add", ".gitignore")
	testkit.GitIn(t, path, "commit", "-qm", "ignore .claude")
	inner := filepath.Join(path, ".claude", "worktrees", "inner")
	testkit.GitIn(t, path, "worktree", "add", "-q", "-b", "inner", inner)
	testkit.MustWrite(t, filepath.Join(inner, "work.txt"), "uncommitted, in the inner one")
	refused(t, ctx, path, RemoveOptions{Force: kit.ForceAll}, inner)
}

// A commit inside a submodule lives only in this worktree's modules/, so it
// is lost work --force does not go past.
func TestRemoveRefusesASubmoduleCommitOnlyThisWorktreeHas(t *testing.T) {
	t.Parallel()
	ctx, _, path := provisiontest.SubmoduleWorktree(t, "fix/sub-commit")
	sm := filepath.Join(path, "sm")
	testkit.GitIn(t, sm, "checkout", "-q", "--detach")
	testkit.GitIn(t, sm, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "only here")
	testkit.GitIn(t, path, "add", "sm")
	testkit.GitIn(t, path, "commit", "-qm", "bump sm")
	refused(t, ctx, path, RemoveOptions{Force: kit.ForceAll}, "submodule sm")
}

func TestRemoveNamesASubmoduleAtAnotherCommit(t *testing.T) {
	t.Parallel()
	ctx, _, path := provisiontest.SubmoduleWorktree(t, "fix/sub-moved")
	sm := filepath.Join(path, "sm")
	testkit.GitIn(t, sm, "checkout", "-q", "--detach")
	testkit.GitIn(t, sm, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "moved")
	testkit.GitIn(t, sm, "branch", "keep-it")
	refused(t, ctx, path, RemoveOptions{}, "git submodule update")
}

// A worktree with an initialised submodule goes whole — through git, with
// nothing pruned — once a strict read found nothing in it.
func TestRemoveTakesASubmoduleWorktreeAndPrunesNothingElse(t *testing.T) {
	t.Parallel()
	ctx, main, path := provisiontest.SubmoduleWorktree(t, "fix/subs")
	gone := filepath.Join(ctx.Repo.Parent, "gone")
	testkit.GitIn(t, main, "worktree", "add", "-q", "--detach", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the worktree should be gone")
	}
	list, _ := ctx.Repo.Worktrees()
	if _, ok := list.ByPath(gone); !ok {
		t.Error("another worktree's registration was pruned")
	}
}

// The submodule's own config hides the file from the parent's status, so
// only reading inside the submodule finds it.
func TestRemoveRefusesASubmoduleWorktreeWithWorkInTheSubmodule(t *testing.T) {
	t.Parallel()
	ctx, _, path := provisiontest.SubmoduleWorktree(t, "fix/subs-dirty")
	testkit.GitIn(t, filepath.Join(path, "sm"), "config", "status.showUntrackedFiles", "no")
	testkit.MustWrite(t, filepath.Join(path, "sm", "scratch"), "x")
	if out := testkit.GitOut(t, path, "status", "--porcelain", "--ignore-submodules=none"); strings.TrimSpace(out) != "" {
		t.Fatalf("the parent's status must not see it for this test to mean anything: %q", out)
	}
	refused(t, ctx, path, RemoveOptions{Force: kit.ForceAll}, "uncommitted changes")
}

// A branch kept under its bare name, when that name is taken, is not a
// removal that went to plan.
func TestRemoveSaysSoWhenTheBranchCannotBeKept(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/kept")
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	testkit.GitIn(t, path, "branch", "kept")

	var buf bytes.Buffer
	err := RemoveAt(ctx, path, RemoveOptions{}, &buf)
	if err == nil {
		t.Fatalf("want an error: the branch could not be renamed\n%s", buf.String())
	}
	if !ctx.Repo.BranchExists("fix_wt/kept") {
		t.Error("the branch must still be there under its old name")
	}
}

// What was read when the plan was made is read again right before the
// delete: a file written in between keeps the worktree.
func TestApplyReadsTheWorktreeAgainBeforeDeleting(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/late")
	plan := planFor(ctx, worktreeRecord(ctx, path), RemoveOptions{})
	if why := plan.refusal(); why != "" {
		t.Fatalf("the plan refuses already: %s", why)
	}
	testkit.MustWrite(t, filepath.Join(path, "late.txt"), "written after the plan")

	var buf bytes.Buffer
	if err := plan.apply(ctx, &buf); err == nil {
		t.Fatalf("want a refusal\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(path, "late.txt")); err != nil {
		t.Error("the late file must still be there")
	}
}

// The submodule path forces git past its own check, so there the second
// read is all that stands between a late file and its deletion.
func TestApplyReadsASubmoduleWorktreeAgainBeforeDeleting(t *testing.T) {
	t.Parallel()
	ctx, _, path := provisiontest.SubmoduleWorktree(t, "fix/late-subs")
	plan := planFor(ctx, worktreeRecord(ctx, path), RemoveOptions{})
	if why := plan.refusal(); why != "" {
		t.Fatalf("the plan refuses already: %s", why)
	}
	testkit.MustWrite(t, filepath.Join(path, "sm", "late.txt"), "written after the plan")

	var buf bytes.Buffer
	if err := plan.apply(ctx, &buf); err == nil {
		t.Fatalf("want a refusal\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(path, "sm", "late.txt")); err != nil {
		t.Error("the late file must still be there")
	}
}

func TestApplyRefusesAHeadThatMovedAfterThePlan(t *testing.T) {
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main)
	dst := filepath.Join(ctx.Repo.Parent, "detached-late")
	testkit.GitIn(t, main, "worktree", "add", "-q", "--detach", dst)
	plan := planFor(ctx, worktreeRecord(ctx, dst), RemoveOptions{})
	testkit.GitIn(t, dst, "commit", "-q", "--allow-empty", "-m", "after the plan")

	var buf bytes.Buffer
	if err := plan.apply(ctx, &buf); err == nil {
		t.Fatalf("want a refusal: HEAD moved\n%s", buf.String())
	}
	if _, err := os.Stat(dst); err != nil {
		t.Error("the worktree must still be there")
	}
}
