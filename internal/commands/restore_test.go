package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// quarantined is a wt worktree for work, moved into a quarantine; it
// returns the context, the worktree's path, its branch, its tip and the
// folder. commit gives the branch a commit of its own, so it is kept under
// its bare name rather than deleted.
func quarantined(t *testing.T, work string, commit bool) (*kit.Context, string, string, string, string) {
	t.Helper()
	ctx, path := provisiontest.SafetyWorktree(t, work)
	branch := strings.TrimSpace(testkit.GitOut(t, path, "symbolic-ref", "--short", "HEAD"))
	if commit {
		testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	}
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch."+branch+".description", "mine")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	return ctx, path, branch, tip, dir
}

func restore(t *testing.T, ctx *kit.Context, dir string) quarantine.RestoreResult {
	t.Helper()
	var res quarantine.RestoreResult
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{Result: &res}, &buf); err != nil {
		t.Fatalf("Restore: %v\n%s", err, buf.String())
	}
	return res
}

func branchTip(t *testing.T, ctx *kit.Context, name string) string {
	t.Helper()
	tip, _ := ctx.Repo.ResolveRef("refs/heads/" + name)
	return tip
}

// §8 step 3: deleted and the name free — made again at the recorded tip,
// its config with it.
func TestRestoreRecreatesADeletedBranchWithItsConfig(t *testing.T) {
	ctx, path, branch, tip, dir := quarantined(t, "fix/back", false)
	if ctx.Repo.BranchExists(branch) {
		t.Fatal("the merged branch should have been deleted")
	}
	res := restore(t, ctx, dir)
	if res.Outcome != quarantine.OutcomeRestored || res.Action != quarantine.ActionRecreate {
		t.Errorf("result %+v", res)
	}
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
	if branchTip(t, ctx, branch) != tip {
		t.Error("the branch must be back at its tip")
	}
	if got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "branch."+branch+".description")); got != "mine" {
		t.Errorf("its config must be back: %q", got)
	}
	r := testkit.LoadRecord(t, dir)
	for _, s := range r.Restore.Steps {
		if s.State != quarantine.Done {
			t.Errorf("restore step %s is %s", s.Name, s.State)
		}
	}
}

// Renamed out of its prefix by the removal: renamed back, config included.
func TestRestoreRenamesAKeptBranchBack(t *testing.T) {
	ctx, path, branch, tip, dir := quarantined(t, "fix/kept-back", true)
	if !ctx.Repo.BranchExists("kept-back") {
		t.Fatal("the unmerged branch should have been kept as kept-back")
	}
	res := restore(t, ctx, dir)
	if res.Action != quarantine.ActionRenameBack {
		t.Errorf("action %s", res.Action)
	}
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
	if branchTip(t, ctx, branch) != tip || ctx.Repo.BranchExists("kept-back") {
		t.Error("the branch must have its name back")
	}
	if got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "branch."+branch+".description")); got != "mine" {
		t.Errorf("its config must move back with it: %q", got)
	}
}

// A branch the removal did not touch is still there at its tip: nothing to
// do to it.
func TestRestoreLeavesAnUntouchedBranch(t *testing.T) {
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main)
	path := filepath.Join(ctx.Repo.Parent, "theirs")
	testkit.GitIn(t, main, "worktree", "add", "-q", "-b", "theirs", path)
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "theirs")
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	res := restore(t, ctx, dir)
	if res.Action != quarantine.ActionNone {
		t.Errorf("action %s", res.Action)
	}
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/theirs")
}

// §8 step 2: a branch another worktree has — as HEAD, as the branch a
// rebase returns to, or as the branch a bisect started from — is not
// touched, and the worktree comes back detached at its commit.
func TestRestoreDetachesWhenAnotherWorktreeHasTheBranch(t *testing.T) {
	for _, how := range []string{"HEAD", "rebase", "bisect"} {
		t.Run(how, func(t *testing.T) {
			ctx, path, branch, tip, dir := quarantined(t, "fix/taken-"+strings.ToLower(how), false)
			other := filepath.Join(ctx.Repo.Parent, "other-"+how)
			switch how {
			case "HEAD":
				testkit.GitIn(t, ctx.Repo.MainRoot, "branch", branch, tip)
				testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", other, branch)
			case "rebase":
				testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "--detach", other)
				testkit.MustWrite(t, filepath.Join(testkit.GitDirFor(t, other), "rebase-merge", "head-name"), "refs/heads/"+branch+"\n")
			case "bisect":
				testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "--detach", other)
				testkit.MustWrite(t, filepath.Join(testkit.GitDirFor(t, other), "BISECT_START"), branch+"\n")
			}
			res := restore(t, ctx, dir)
			if res.Action != quarantine.ActionDetach || !strings.Contains(res.OccupiedBy, "other-"+how) {
				t.Errorf("result %+v", res)
			}
			testkit.AssertBack(t, ctx, path, dir, tip)
		})
	}
}

// The kept name counts as the branch too.
func TestRestoreDetachesWhenAnotherWorktreeHasTheKeptName(t *testing.T) {
	ctx, path, _, tip, dir := quarantined(t, "fix/kept-taken", true)
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", filepath.Join(ctx.Repo.Parent, "holder"), "kept-taken")
	res := restore(t, ctx, dir)
	if res.Action != quarantine.ActionDetach {
		t.Errorf("action %s", res.Action)
	}
	testkit.AssertBack(t, ctx, path, dir, tip)
}

// The name taken by another tip, the kept name still at the recorded one:
// HEAD names the kept branch.
func TestRestoreAttachesToTheKeptNameWhenTheOriginalIsTaken(t *testing.T) {
	ctx, path, branch, _, dir := quarantined(t, "fix/attach", true)
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", branch, "main")
	res := restore(t, ctx, dir)
	if res.Action != quarantine.ActionAttach {
		t.Errorf("action %s", res.Action)
	}
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/attach")
}

// The kept branch moved on: HEAD is detached at the recorded commit, and
// neither branch is touched.
func TestRestoreDetachesWhenTheKeptBranchMovedOn(t *testing.T) {
	ctx, path, branch, tip, dir := quarantined(t, "fix/moved-on", true)
	testkit.GitIn(t, ctx.Repo.MainRoot, "update-ref", "refs/heads/moved-on", "main")
	res := restore(t, ctx, dir)
	if res.Action != quarantine.ActionDetach {
		t.Errorf("action %s", res.Action)
	}
	testkit.AssertBack(t, ctx, path, dir, tip)
	if ctx.Repo.BranchExists(branch) {
		t.Error("the original name must not be made")
	}
}

// While quarantined, nothing but the quarantine holds a squash-merged
// branch's commits: the branch is deleted with its reflog, and the admin
// dir that held HEAD is out of the repository. Refs pin them, so gc cannot
// take them, and they go when the restore is done.
func TestQuarantinePinsTheCommitsAgainstGC(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/squashed-away")
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "only on the branch")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	opts := RemoveOptions{Quarantine: dir, Landed: func(string, string) int { return 9 }}
	if err := RemoveAt(ctx, path, opts, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if ctx.Repo.BranchExists("fix_wt/squashed-away") {
		t.Fatal("the squash-merged branch should have been deleted")
	}
	testkit.GitIn(t, ctx.Repo.MainRoot, "reflog", "expire", "--expire=now", "--all")
	testkit.GitIn(t, ctx.Repo.MainRoot, "gc", "-q", "--prune=now")
	if _, err := gittest.Try(t, ctx.Repo.MainRoot, "cat-file", "-e", tip+"^{commit}"); err != nil {
		t.Fatal("gc took the quarantined branch's commit")
	}
	restore(t, ctx, dir)
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/fix_wt/squashed-away")
	if branchTip(t, ctx, "fix_wt/squashed-away") != tip {
		t.Error("the branch must be back at its tip")
	}
	if pins := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-quarantine/")); pins != "" {
		t.Errorf("the pins must go once it is restored:\n%s", pins)
	}
}

// git worktree prune and gc remove an empty .git/worktrees, and a checkout's
// parent folders can go once it is empty: restore makes the parents again,
// never the directories themselves.
func TestRestoreMakesTheParentsThatWentMeanwhile(t *testing.T) {
	ctx, path, branch, _, dir := quarantined(t, "fix/lonely", false)
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "prune")
	testkit.GitIn(t, ctx.Repo.MainRoot, "gc", "-q")
	if err := os.Remove(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if testkit.Exists(filepath.Join(ctx.Repo.MainRoot, ".git", "worktrees")) {
		t.Fatal("for this test to mean anything, .git/worktrees must be gone")
	}
	restore(t, ctx, dir)
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
}

// A removal stopped before anything was unregistered left a worktree that
// is still in use: restore only unlocks it and retires the record, whatever
// happened to its branch since.
func TestRestoreOnlyUnlocksAWorktreeThatNeverMoved(t *testing.T) {
	for _, how := range []string{"stopped after the lock", "the move failed"} {
		t.Run(how, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/stayed")
			dir := testkit.TrashFor(t, ctx)
			if how == "stopped after the lock" {
				testkit.CrashAt(t, "lock:done")
			} else {
				t.Cleanup(func() { quarantine.AfterSave = nil })
				quarantine.AfterSave = func(_ *quarantine.Record, at string) error {
					if at == "moveCheckout:running" {
						return os.Mkdir(filepath.Join(dir, "checkout"), 0o755)
					}
					return nil
				}
			}
			var buf bytes.Buffer
			if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err == nil {
				t.Fatalf("the removal must stop\n%s", buf.String())
			}
			quarantine.AfterSave = nil
			testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "kept working")
			res := restore(t, ctx, dir)
			if res.Action != quarantine.ActionNone {
				t.Errorf("action %s", res.Action)
			}
			got, _ := os.ReadFile(filepath.Join(testkit.GitDirFor(t, path), "HEAD"))
			if strings.TrimSpace(string(got)) != "ref: refs/heads/fix_wt/stayed" {
				t.Errorf("HEAD must stay on the branch: %q", got)
			}
			list, _ := ctx.Repo.Worktrees()
			if wt, _ := list.ByPath(path); wt.Locked {
				t.Error("it must be unlocked")
			}
		})
	}
}

// A branch somebody made again at the same tip, with config of its own, is
// left with that config: the old section is not stacked on it.
func TestRestoreDoesNotStackOldConfigOnNew(t *testing.T) {
	ctx, path, branch, tip, dir := quarantined(t, "fix/reconfigured", false)
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", branch, tip)
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch."+branch+".description", "theirs")
	restore(t, ctx, dir)
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
	if got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get-all", "branch."+branch+".description")); got != "theirs" {
		t.Errorf("config %q, want only theirs", got)
	}
}

// While quarantined, the checkout's .git names the quarantined admin dir,
// so a new worktree that takes the same id is never the one git in the
// quarantine works on; restore names the original again.
func TestQuarantinedCheckoutPointsAtItsOwnAdminDir(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/same-id")
	before := testkit.MustRead(t, filepath.Join(path, ".git"))
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if got := testkit.MustRead(t, filepath.Join(dir, "checkout", ".git")); got != "gitdir: "+filepath.Join(dir, "admin") {
		t.Errorf(".git in the quarantine says %q", got)
	}
	restore(t, ctx, dir)
	if got := testkit.MustRead(t, filepath.Join(path, ".git")); got != before {
		t.Errorf(".git after restore says %q, was %q", got, before)
	}
}

// §8 step 1: the original path taken by anything but this worktree refuses.
func TestRestoreRefusesWhenTheOriginalPathIsTaken(t *testing.T) {
	ctx, path, _, _, dir := quarantined(t, "fix/path-taken", false)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{}, &buf); err == nil || !strings.Contains(err.Error(), "taken") {
		t.Fatalf("want a refusal: %v\n%s", err, buf.String())
	}
	if !testkit.Exists(filepath.Join(dir, "checkout")) {
		t.Error("nothing may move on a refusal")
	}
}

func TestRestoreRefusesAnotherVolume(t *testing.T) {
	ctx, path, _, _, dir := quarantined(t, "fix/restore-volume", false)
	orig := quarantine.DeviceOf
	t.Cleanup(func() { quarantine.DeviceOf = orig })
	quarantine.DeviceOf = func(p string) (uint64, error) {
		if p == filepath.Dir(path) {
			return 4242, nil
		}
		return orig(p)
	}
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{}, &buf); err == nil || !strings.Contains(err.Error(), "volume") {
		t.Fatalf("want a refusal: %v\n%s", err, buf.String())
	}
}

// --dry-run says what it would do to the branch and changes nothing.
func TestRestoreDryRunChangesNothing(t *testing.T) {
	ctx, _, branch, _, dir := quarantined(t, "fix/dry", false)
	before, _ := os.ReadFile(filepath.Join(dir, quarantine.FileName))
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{DryRun: true}, &buf); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, quarantine.FileName))
	if !bytes.Equal(before, after) || ctx.Repo.BranchExists(branch) || !testkit.Exists(filepath.Join(dir, "checkout")) {
		t.Error("a dry run must change nothing")
	}
	if !strings.Contains(buf.String(), "recreate") && !strings.Contains(buf.String(), "made again") {
		t.Errorf("it must say what happens to the branch:\n%s", buf.String())
	}
}

// gittree's rule: a quarantine's lock with no recovery.json was stopped
// before anything moved, and restoring it is unlocking it.
func TestRestoreUnlocksAQuarantineLockWithNoRecord(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/orphan-lock")
	dir := testkit.TrashFor(t, ctx)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "lock", "--reason", quarantine.LockReason(dir), path)
	res := restore(t, ctx, dir)
	if res.Outcome != quarantine.OutcomeRestored {
		t.Errorf("result %+v", res)
	}
	list, _ := ctx.Repo.Worktrees()
	if wt, _ := list.ByPath(path); wt.Locked {
		t.Error("the lock must be gone")
	}
}

func TestRestoreRefusesAFolderWithNoRecordAndNoLock(t *testing.T) {
	ctx, _ := provisiontest.SafetyWorktree(t, "fix/nothing")
	dir := testkit.TrashFor(t, ctx)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{}, &buf); err == nil {
		t.Fatalf("want a refusal: nothing to restore\n%s", buf.String())
	}
}

// A removal that stops after any journal write — or after a step it ran but
// did not get to record — is put back by wt restore.
func TestRestorePutsBackARemovalStoppedAtEveryStep(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: stops the run at every step, over real repositories")
	}
	points := journalPoints(t, func() { quarantined(t, "fix/count", true) })
	if len(points) < 13 {
		t.Fatalf("points %v", points)
	}
	for _, point := range points {
		for _, lost := range []bool{false, true} {
			if lost && !strings.HasSuffix(point, ":done") {
				continue
			}
			name := point
			if lost {
				name += "-unrecorded"
			}
			t.Run(name, func(t *testing.T) {
				ctx, path := provisiontest.SafetyWorktree(t, "fix/crash")
				testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
				testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch.fix_wt/crash.description", "mine")
				tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
				dir := testkit.TrashFor(t, ctx)
				testkit.CrashAt(t, point)
				var buf bytes.Buffer
				if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err == nil && point != points[len(points)-1] {
					t.Fatalf("the run must stop at %s", point)
				}
				quarantine.AfterSave = nil
				if lost {
					// The step ran, and the write saying so never reached the disk.
					r := testkit.LoadRecord(t, dir)
					step, _, _ := strings.Cut(point, ":")
					r.Step(step).State = quarantine.Running
					if err := r.Save("test"); err != nil {
						t.Fatal(err)
					}
				}
				restore(t, ctx, dir)
				testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/fix_wt/crash")
				if branchTip(t, ctx, "fix_wt/crash") != tip || ctx.Repo.BranchExists("crash") {
					t.Error("the branch must be back under its name")
				}
				if got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get-all", "branch.fix_wt/crash.description")); got != "mine" {
					t.Errorf("its config must be back once: %q", got)
				}
			})
		}
	}
}

// A restore that stops after any journal write is finished by the next.
func TestRestoreFinishesARestoreStoppedAtEveryStep(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: stops the run at every step, over real repositories")
	}
	var points []string
	{
		ctx, _, _, _, dir := quarantined(t, "fix/count-restore", false)
		points = journalPoints(t, func() { restore(t, ctx, dir) })
	}
	if len(points) < 12 {
		t.Fatalf("points %v", points)
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			ctx, path, branch, tip, dir := quarantined(t, "fix/crash-restore", false)
			testkit.CrashAt(t, point)
			var buf bytes.Buffer
			_ = Restore(ctx, dir, RestoreOptions{}, &buf)
			quarantine.AfterSave = nil
			restore(t, ctx, dir)
			testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
			if branchTip(t, ctx, branch) != tip {
				t.Error("the branch must be back")
			}
			if got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get-all", "branch."+branch+".description")); got != "mine" {
				t.Errorf("its config must be back once: %q", got)
			}
		})
	}
}

func TestRestoreJSONMatchesItsSchemas(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/json", false)
	testkit.ValidateJSON(t, "recovery", []byte(testkit.MustRead(t, filepath.Join(dir, quarantine.FileName))))

	var plan bytes.Buffer
	if err := RestoreJSON(ctx, dir, RestoreOptions{DryRun: true}, &plan, &bytes.Buffer{}); err != nil {
		t.Fatalf("%v\n%s", err, plan.String())
	}
	testkit.ValidateJSON(t, "restore-plan", plan.Bytes())

	var out bytes.Buffer
	if err := RestoreJSON(ctx, dir, RestoreOptions{}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	testkit.ValidateJSON(t, "restore", out.Bytes())
	var res struct{ Outcome string }
	_ = json.Unmarshal(out.Bytes(), &res)
	if res.Outcome != quarantine.OutcomeRestored {
		t.Errorf("outcome %s", res.Outcome)
	}
	testkit.ValidateJSON(t, "recovery", []byte(testkit.MustRead(t, filepath.Join(dir, quarantine.FileName))))

	var refused bytes.Buffer
	if err := RestoreJSON(ctx, filepath.Join(dir, "nope"), RestoreOptions{}, &refused, &bytes.Buffer{}); err == nil {
		t.Error("want an error for a folder that is not there")
	}
	testkit.ValidateJSON(t, "restore", refused.Bytes())
}

// A restore that stopped after its branch step is not trusted when it
// resumes: the branch is read again before the worktree is registered, and
// a branch that vanished or moved since leaves HEAD detached, never on it.
func TestRestoreResumedReadsTheBranchAgain(t *testing.T) {
	for _, change := range []string{"deleted", "moved"} {
		t.Run(change, func(t *testing.T) {
			ctx, path, branch, tip, dir := quarantined(t, "fix/resume-"+change, false)
			testkit.CrashAt(t, "restore.branch:done")
			var buf bytes.Buffer
			_ = Restore(ctx, dir, RestoreOptions{}, &buf)
			quarantine.AfterSave = nil
			if branchTip(t, ctx, branch) != tip {
				t.Fatal("the first run should have made the branch again")
			}
			if change == "deleted" {
				testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "-D", branch)
			} else {
				testkit.GitIn(t, ctx.Repo.MainRoot, "commit", "-q", "--allow-empty", "-m", "elsewhere")
				testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "-f", branch, "main")
			}
			res := restore(t, ctx, dir)
			if res.Action != quarantine.ActionDetach {
				t.Errorf("action %s", res.Action)
			}
			testkit.AssertBack(t, ctx, path, dir, tip)
		})
	}
}

// Only the directories the removal moved go back: something put in their
// place in the quarantine is refused before anything is written.
func TestRestoreRefusesADirectoryPutInTheQuarantine(t *testing.T) {
	for _, which := range []string{"checkout", "admin"} {
		t.Run(which, func(t *testing.T) {
			ctx, _, _, _, dir := quarantined(t, "fix/swapped-"+which, false)
			moved := filepath.Join(dir, which)
			if err := os.Rename(moved, moved+".real"); err != nil {
				t.Fatal(err)
			}
			if which == "admin" {
				// A symlink to another worktree's admin dir.
				other := filepath.Join(ctx.Repo.Parent, "other")
				testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "--detach", other)
				if err := os.Symlink(testkit.GitDirFor(t, other), moved); err != nil {
					t.Fatal(err)
				}
				before := testkit.MustRead(t, filepath.Join(testkit.GitDirFor(t, other), "HEAD"))
				t.Cleanup(func() {
					if after := testkit.MustRead(t, filepath.Join(testkit.GitDirFor(t, other), "HEAD")); after != before {
						t.Errorf("another worktree's HEAD was written: %s", after)
					}
				})
			} else if err := os.Mkdir(moved, 0o755); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := Restore(ctx, dir, RestoreOptions{}, &buf); err == nil {
				t.Fatalf("want a refusal\n%s", buf.String())
			}
		})
	}
}

// Config is put back entry by entry, so a restore stopped between two of
// them adds the rest, and none twice.
func TestRestoreFinishesAConfigSectionPutBackInPart(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/two-keys")
	branch := "fix_wt/two-keys"
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch."+branch+".remote", "origin")
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	// What a restore stopped after its first config write leaves: the
	// branch made again, one entry back, the branch step running.
	testkit.GitIn(t, ctx.Repo.MainRoot, "update-ref", "refs/heads/"+branch, tip)
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "--add", "branch."+branch+".remote", "origin")
	r := testkit.LoadRecord(t, dir)
	recreate := quarantine.ActionRecreate
	r.Restore = &quarantine.Restore{Action: &recreate, Steps: []quarantine.Step{
		{Name: "branch", State: quarantine.Running}, {Name: "moveAdmin", State: quarantine.Pending},
		{Name: "relink", State: quarantine.Pending}, {Name: "moveCheckout", State: quarantine.Pending},
		{Name: "unlock", State: quarantine.Pending}, {Name: "unpin", State: quarantine.Pending}}}
	if err := r.Save("test"); err != nil {
		t.Fatal(err)
	}
	restore(t, ctx, dir)
	got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get-regexp", `^branch\.fix_wt/two-keys\.`))
	want := "branch." + branch + ".remote origin\nbranch." + branch + ".merge refs/heads/" + branch
	if got != want {
		t.Errorf("config:\n%s\nwant:\n%s", got, want)
	}
}

// A path taken between the plan and the move is a failure, never a move
// journalled as done.
func TestRestoreFailsWhenAPathIsTakenMidway(t *testing.T) {
	ctx, path, _, _, dir := quarantined(t, "fix/taken-midway", false)
	t.Cleanup(func() { quarantine.AfterSave = nil })
	quarantine.AfterSave = func(_ *quarantine.Record, at string) error {
		if at == "restore.moveCheckout:running" {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	var res quarantine.RestoreResult
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{Result: &res}, &buf); err == nil {
		t.Fatalf("want a failure\n%s", buf.String())
	}
	if res.Outcome == quarantine.OutcomeRestored {
		t.Errorf("result %+v", res)
	}
	if !testkit.Exists(filepath.Join(dir, "checkout")) {
		t.Error("the checkout must still be in the quarantine")
	}
	if s := testkit.LoadRecord(t, dir).RestoreStep(quarantine.StepMoveCheckout); s.State == quarantine.Done {
		t.Error("the move must not be journalled as done")
	}
}

// A rename that went through and a run that stopped before its fsync, and a
// delete that stopped between the ref and its config, are put back too.
func TestRestorePutsBackARemovalStoppedInsideAStep(t *testing.T) {
	for _, point := range []string{"checkout", "admin", "ref deleted"} {
		t.Run(point, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/inside")
			branch := "fix_wt/inside"
			testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch."+branch+".description", "mine")
			tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
			dir := testkit.TrashFor(t, ctx)
			switch point {
			case "ref deleted":
				t.Cleanup(func() { repo.AfterRefDeleted = nil })
				repo.AfterRefDeleted = func(string) error { return testkit.ErrCrash }
			default:
				t.Cleanup(func() { quarantine.AfterRename = nil })
				quarantine.AfterRename = func(to string) error {
					if to == filepath.Join(dir, point) {
						return testkit.ErrCrash
					}
					return nil
				}
			}
			var buf bytes.Buffer
			if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err == nil {
				t.Fatalf("the run must stop\n%s", buf.String())
			}
			quarantine.AfterRename, repo.AfterRefDeleted = nil, nil
			restore(t, ctx, dir)
			testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
			if branchTip(t, ctx, branch) != tip {
				t.Error("the branch must be back")
			}
			if got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get-all", "branch."+branch+".description")); got != "mine" {
				t.Errorf("its config must be there once: %q", got)
			}
		})
	}
}

// A restore that made a deleted branch again and then failed to put its
// config back owns that branch when it is run again: the entries still
// missing go back, none twice, and a value set since is left as it is.
func TestRestoreRetryFinishesTheConfigOfABranchItMade(t *testing.T) {
	for _, newer := range []bool{false, true} {
		name := "failed"
		if newer {
			name = "set-since"
		}
		t.Run(name, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/retry-"+name)
			branch := "fix_wt/retry-" + name
			section := "branch." + branch + "."
			testkit.GitIn(t, ctx.Repo.MainRoot, "config", section+"remote", "origin")
			testkit.GitIn(t, ctx.Repo.MainRoot, "config", section+"merge", "refs/heads/"+branch)
			testkit.GitIn(t, ctx.Repo.MainRoot, "config", section+"description", "mine")
			tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
			dir := testkit.TrashFor(t, ctx)
			var buf bytes.Buffer
			if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err != nil {
				t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
			}
			t.Cleanup(func() { repo.BeforeConfigAdd = nil })
			repo.BeforeConfigAdd = func(key, _ string) error {
				if strings.HasSuffix(key, ".merge") {
					return testkit.ErrCrash
				}
				return nil
			}
			var res quarantine.RestoreResult
			if err := Restore(ctx, dir, RestoreOptions{Result: &res}, &buf); err == nil {
				t.Fatalf("the config write must fail\n%s", buf.String())
			}
			repo.BeforeConfigAdd = nil
			if branchTip(t, ctx, branch) != tip {
				t.Fatal("the first run should have made the branch again")
			}
			wantMerge := "refs/heads/" + branch
			if newer {
				wantMerge = "refs/heads/elsewhere"
				testkit.GitIn(t, ctx.Repo.MainRoot, "config", section+"merge", wantMerge)
			}
			res = restore(t, ctx, dir)
			if res.Outcome != quarantine.OutcomeRestored {
				t.Errorf("result %+v", res)
			}
			testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
			for key, want := range map[string]string{"remote": "origin", "merge": wantMerge, "description": "mine"} {
				if got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get-all", section+key)); got != want {
					t.Errorf("%s is %q, want %q", key, got, want)
				}
			}
		})
	}
}

// A restore that stopped between making the branch again and saying so,
// then failed a config write, then runs a third time: the branch stays its
// own throughout, and the config comes back whole.
func TestRestoreKeepsABranchItMadeAcrossTwoFailures(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/thrice")
	branch := "fix_wt/thrice"
	section := "branch." + branch + "."
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", section+"remote", "origin")
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", section+"merge", "refs/heads/"+branch)
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	// The first run: the branch made, nothing journalled about it.
	testkit.GitIn(t, ctx.Repo.MainRoot, "update-ref", "refs/heads/"+branch, tip)
	r := testkit.LoadRecord(t, dir)
	recreate := quarantine.ActionRecreate
	r.Restore = &quarantine.Restore{Action: &recreate, Steps: []quarantine.Step{
		{Name: "branch", State: quarantine.Running}, {Name: "moveAdmin", State: quarantine.Pending},
		{Name: "relink", State: quarantine.Pending}, {Name: "moveCheckout", State: quarantine.Pending},
		{Name: "unlock", State: quarantine.Pending}, {Name: "unpin", State: quarantine.Pending}}}
	if err := r.Save("test"); err != nil {
		t.Fatal(err)
	}
	// The second: one entry back, then a failure.
	t.Cleanup(func() { repo.BeforeConfigAdd = nil })
	repo.BeforeConfigAdd = func(key, _ string) error {
		if strings.HasSuffix(key, ".merge") {
			return testkit.ErrCrash
		}
		return nil
	}
	if err := Restore(ctx, dir, RestoreOptions{}, &buf); err == nil {
		t.Fatal("the config write must fail")
	}
	repo.BeforeConfigAdd = nil
	restore(t, ctx, dir)
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
	got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get-regexp", `^branch\.fix_wt/thrice\.`))
	want := section + "remote origin\n" + section + "merge refs/heads/" + branch
	if got != want {
		t.Errorf("config:\n%s\nwant:\n%s", got, want)
	}
}

// A quarantine lock whose folder is gone too is still taken off.
func TestRestoreUnlocksAnOrphanWhoseFolderIsGone(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/orphan-gone")
	dir := testkit.TrashFor(t, ctx)
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "lock", "--reason", quarantine.LockReason(dir), path)
	res := restore(t, ctx, dir)
	if res.Outcome != quarantine.OutcomeRestored {
		t.Errorf("result %+v", res)
	}
	list, _ := ctx.Repo.Worktrees()
	if wt, _ := list.ByPath(path); wt.Locked {
		t.Error("the lock must be gone")
	}
}
