package retire

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// A quarantine moves the checkout and its admin dir into the folder, deletes
// the merged branch after recording it, and says how to put it back.
func TestQuarantineMovesTheWorktreeInsteadOfDeletingIt(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/trashed")
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch.fix_wt/trashed.description", "mine")
	testkit.MustWrite(t, filepath.Join(ctx.Repo.MainRoot, ".git", "info", "exclude"), "*.log\n")
	testkit.MustWrite(t, filepath.Join(path, "ignored.log"), "")
	admin := testkit.GitDirFor(t, path)
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	dir := testkit.TrashFor(t, ctx)

	var buf bytes.Buffer
	var res RemoveResult
	if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir, Result: &res}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if testkit.Exists(path) || testkit.Exists(admin) {
		t.Error("the checkout and its admin dir must be gone from where they were")
	}
	if !testkit.Exists(filepath.Join(dir, "checkout", "ignored.log")) || !testkit.Exists(filepath.Join(dir, "admin", "HEAD")) {
		t.Error("both must be in the quarantine, ignored files included")
	}
	if list, _ := ctx.Repo.Worktrees(); func() bool { _, ok := list.ByPath(path); return ok }() {
		t.Error("git must no longer list the worktree")
	}
	if ctx.Repo.BranchExists("fix_wt/trashed") {
		t.Error("the merged branch should be deleted")
	}
	lock, _ := os.ReadFile(filepath.Join(dir, "admin", "locked"))
	if strings.TrimSpace(string(lock)) != quarantine.LockReason(dir) {
		t.Errorf("the admin dir must carry the quarantine's lock, has %q", lock)
	}
	r := testkit.LoadRecord(t, dir)
	for _, s := range r.Steps {
		if s.State != quarantine.Done {
			t.Errorf("step %s is %s", s.Name, s.State)
		}
	}
	if r.Branch == nil || r.Branch.Tip != tip || Deref(r.Branch.Result) != quarantine.BranchDeleted ||
		len(r.Branch.Config) != 1 || r.Branch.Config[0].Value != "mine" {
		t.Errorf("branch record %+v", r.Branch)
	}
	if res.Outcome != RemoveRemoved || res.Worktree != WorktreeQuarantined || !res.CheckoutMoved || !res.AdminMoved {
		t.Errorf("result %+v", res)
	}
	if !strings.Contains(buf.String(), "wt restore "+dir) {
		t.Errorf("it must say how to put it back:\n%s", buf.String())
	}
}

// What an editor or a build writes after the last check is not deleted: it
// moves with the checkout.
func TestQuarantineKeepsAFileWrittenAfterTheLastCheck(t *testing.T) {
	// Not parallel: swaps afterFinalCheck.
	ctx, path := provisiontest.SafetyWorktree(t, "fix/late-write")
	dir := testkit.TrashFor(t, ctx)
	t.Cleanup(func() { afterFinalCheck = nil })
	afterFinalCheck = func() { testkit.MustWrite(t, filepath.Join(path, "late.txt"), "written after the check") }

	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if b, err := os.ReadFile(filepath.Join(dir, "checkout", "late.txt")); err != nil || string(b) != "written after the check" {
		t.Errorf("the late file must be in the quarantine: %v", err)
	}
}

// A linked worktree keeps its submodules' repositories in its admin dir, so
// they go with it, under admin/modules/.
func TestQuarantineTakesTheSubmoduleRepositoriesAlong(t *testing.T) {
	t.Parallel()
	ctx, _, path := provisiontest.SubmoduleWorktree(t, "fix/sub-trash")
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if !testkit.Exists(filepath.Join(dir, "admin", "modules", "sm", "HEAD")) {
		t.Error("the submodule's repository must be under admin/modules/")
	}
	if !testkit.Exists(filepath.Join(dir, "checkout", "sm", ".git")) {
		t.Error("the submodule's checkout must be in the quarantine")
	}
}

// Nothing changes before a refusal: no folder, no lock, no move.
func TestQuarantineRefusesAFolderThatExistsBeforeAnyChange(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/exists")
	dir := testkit.TrashFor(t, ctx)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var res RemoveResult
	refused(t, ctx, path, RemoveOptions{Quarantine: dir, Result: &res}, "is there already")
	assertNotQuarantined(t, ctx, path, dir)
	if res.Outcome != RemoveRefused {
		t.Errorf("outcome %s", res.Outcome)
	}
}

func TestQuarantineRefusesAnotherVolumeBeforeAnyChange(t *testing.T) {
	// Not parallel: swaps quarantine.DeviceOf.
	ctx, path := provisiontest.SafetyWorktree(t, "fix/volume")
	dir := testkit.TrashFor(t, ctx)
	orig := quarantine.DeviceOf
	t.Cleanup(func() { quarantine.DeviceOf = orig })
	quarantine.DeviceOf = func(p string) (uint64, error) {
		if repo := filepath.Dir(dir); p == repo {
			return 4242, nil
		}
		return orig(p)
	}
	refused(t, ctx, path, RemoveOptions{Quarantine: dir, Force: kit.ForceAll}, "another volume")
	assertNotQuarantined(t, ctx, path, dir)
}

func assertNotQuarantined(t *testing.T, ctx *kit.Context, path, dir string) {
	t.Helper()
	if entries, _ := os.ReadDir(dir); len(entries) > 0 {
		t.Errorf("nothing may be written into %s", dir)
	}
	list, _ := ctx.Repo.Worktrees()
	wt, ok := list.ByPath(path)
	if !ok || wt.Locked {
		t.Errorf("the worktree must be registered and unlocked: %+v", wt)
	}
	if !ctx.Repo.BranchExists("fix_wt/" + filepath.Base(path)) {
		t.Error("the branch must be untouched")
	}
}

// A branch step that fails once the checkout is gone is a partial removal,
// not a removal with an error on the side.
func TestRemoveReportsABranchFailureAfterTheFolderIsGoneAsPartial(t *testing.T) {
	t.Parallel()
	for _, q := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete", true: "quarantine"}[q], func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/kept-partial")
			testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
			testkit.GitIn(t, path, "branch", "kept-partial")
			opts := RemoveOptions{}
			if q {
				opts.Quarantine = testkit.TrashFor(t, ctx)
			}
			var res RemoveResult
			opts.Result = &res
			var buf bytes.Buffer
			err := RemoveAt(ctx, path, opts, &buf)
			if err == nil {
				t.Fatalf("want an error\n%s", buf.String())
			}
			if res.Outcome != RemovePartial || res.Branch != quarantine.BranchFailed {
				t.Errorf("result %+v", res)
			}
			if strings.Contains(buf.String(), "✓") {
				t.Errorf("a partial removal is not a tick:\n%s", buf.String())
			}
			if !strings.Contains(buf.String(), "partly done") {
				t.Errorf("it must say it is partly done:\n%s", buf.String())
			}
			if q && Deref(testkit.LoadRecord(t, opts.Quarantine).Branch.Result) != quarantine.BranchFailed {
				t.Error("recovery.json must say the branch step failed")
			}
		})
	}
}

// A quarantine inside a checkout or the git dir would move a worktree into
// something another removal can delete, or into git's own files.
func TestQuarantineRefusesAFolderInsideTheRepository(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/inside-repo")
	other := filepath.Join(ctx.Repo.Parent, "neighbour")
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "--detach", other)
	for _, dir := range []string{
		filepath.Join(ctx.Repo.MainRoot, "trash-q"),
		filepath.Join(ctx.Repo.MainRoot, ".git", "trash-q"),
		filepath.Join(other, "trash-q"),
		filepath.Join(path, "trash-q"),
	} {
		refused(t, ctx, path, RemoveOptions{Quarantine: dir}, "inside")
		if testkit.Exists(dir) {
			t.Errorf("%s must not be made", dir)
		}
	}
}
