package refs_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/refs"
	"github.com/anders-lindstrom/wt/internal/commands/refs/refstest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/repo"
)

func branchTip(t *testing.T, ctx *kit.Context, name string) string {
	t.Helper()
	tip, _ := ctx.Repo.ResolveRef("refs/heads/" + name)
	return tip
}

// §8 step 3: deleted and the name free — made again at the recorded tip,
// its config with it.
func TestRestoreRecreatesADeletedBranchWithItsConfig(t *testing.T) {
	t.Parallel()
	ctx, path, branch, tip, dir := retiretest.Quarantined(t, "fix/back", false)
	if ctx.Repo.BranchExists(branch) {
		t.Fatal("the merged branch should have been deleted")
	}
	res := refstest.Restore(t, ctx, dir)
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
	t.Parallel()
	ctx, path, branch, tip, dir := retiretest.Quarantined(t, "fix/kept-back", true)
	if !ctx.Repo.BranchExists("kept-back") {
		t.Fatal("the unmerged branch should have been kept as kept-back")
	}
	res := refstest.Restore(t, ctx, dir)
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
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main)
	path := filepath.Join(ctx.Repo.Parent, "theirs")
	testkit.GitIn(t, main, "worktree", "add", "-q", "-b", "theirs", path)
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "theirs")
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	res := refstest.Restore(t, ctx, dir)
	if res.Action != quarantine.ActionNone {
		t.Errorf("action %s", res.Action)
	}
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/theirs")
}

// §8 step 2: a branch another worktree has — as HEAD, as the branch a
// rebase returns to, or as the branch a bisect started from — is not
// touched, and the worktree comes back detached at its commit.
func TestRestoreDetachesWhenAnotherWorktreeHasTheBranch(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"HEAD", "rebase", "bisect"} {
		t.Run(how, func(t *testing.T) {
			ctx, path, branch, tip, dir := retiretest.Quarantined(t, "fix/taken-"+strings.ToLower(how), false)
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
			res := refstest.Restore(t, ctx, dir)
			if res.Action != quarantine.ActionDetach || !strings.Contains(res.OccupiedBy, "other-"+how) {
				t.Errorf("result %+v", res)
			}
			testkit.AssertBack(t, ctx, path, dir, tip)
		})
	}
}

// The kept name counts as the branch too.
func TestRestoreDetachesWhenAnotherWorktreeHasTheKeptName(t *testing.T) {
	t.Parallel()
	ctx, path, _, tip, dir := retiretest.Quarantined(t, "fix/kept-taken", true)
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", filepath.Join(ctx.Repo.Parent, "holder"), "kept-taken")
	res := refstest.Restore(t, ctx, dir)
	if res.Action != quarantine.ActionDetach {
		t.Errorf("action %s", res.Action)
	}
	testkit.AssertBack(t, ctx, path, dir, tip)
}

// The name taken by another tip, the kept name still at the recorded one:
// HEAD names the kept branch.
func TestRestoreAttachesToTheKeptNameWhenTheOriginalIsTaken(t *testing.T) {
	t.Parallel()
	ctx, path, branch, _, dir := retiretest.Quarantined(t, "fix/attach", true)
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", branch, "main")
	res := refstest.Restore(t, ctx, dir)
	if res.Action != quarantine.ActionAttach {
		t.Errorf("action %s", res.Action)
	}
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/attach")
}

// The kept branch moved on: HEAD is detached at the recorded commit, and
// neither branch is touched.
func TestRestoreDetachesWhenTheKeptBranchMovedOn(t *testing.T) {
	t.Parallel()
	ctx, path, branch, tip, dir := retiretest.Quarantined(t, "fix/moved-on", true)
	testkit.GitIn(t, ctx.Repo.MainRoot, "update-ref", "refs/heads/moved-on", "main")
	res := refstest.Restore(t, ctx, dir)
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
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/squashed-away")
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "only on the branch")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	opts := retire.RemoveOptions{Quarantine: dir, Landed: func(string, string) int { return 9 }}
	if err := retire.RemoveAt(ctx, path, opts, &buf); err != nil {
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
	refstest.Restore(t, ctx, dir)
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
	t.Parallel()
	ctx, path, branch, _, dir := retiretest.Quarantined(t, "fix/lonely", false)
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "prune")
	testkit.GitIn(t, ctx.Repo.MainRoot, "gc", "-q")
	if err := os.Remove(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if testkit.Exists(filepath.Join(ctx.Repo.MainRoot, ".git", "worktrees")) {
		t.Fatal("for this test to mean anything, .git/worktrees must be gone")
	}
	refstest.Restore(t, ctx, dir)
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
}

// A removal stopped before anything was unregistered left a worktree that
// is still in use: restore only unlocks it and retires the record, whatever
// happened to its branch since.
func TestRestoreOnlyUnlocksAWorktreeThatNeverMoved(t *testing.T) {
	// Not parallel: swaps quarantine.AfterSave.
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
			if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir}, &buf); err == nil {
				t.Fatalf("the removal must stop\n%s", buf.String())
			}
			quarantine.AfterSave = nil
			testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "kept working")
			res := refstest.Restore(t, ctx, dir)
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
	t.Parallel()
	ctx, path, branch, tip, dir := retiretest.Quarantined(t, "fix/reconfigured", false)
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", branch, tip)
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch."+branch+".description", "theirs")
	refstest.Restore(t, ctx, dir)
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
	if got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get-all", "branch."+branch+".description")); got != "theirs" {
		t.Errorf("config %q, want only theirs", got)
	}
}

// While quarantined, the checkout's .git names the quarantined admin dir,
// so a new worktree that takes the same id is never the one git in the
// quarantine works on; restore names the original again.
func TestQuarantinedCheckoutPointsAtItsOwnAdminDir(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/same-id")
	before := testkit.MustRead(t, filepath.Join(path, ".git"))
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if got := testkit.MustRead(t, filepath.Join(dir, "checkout", ".git")); got != "gitdir: "+filepath.Join(dir, "admin") {
		t.Errorf(".git in the quarantine says %q", got)
	}
	refstest.Restore(t, ctx, dir)
	if got := testkit.MustRead(t, filepath.Join(path, ".git")); got != before {
		t.Errorf(".git after restore says %q, was %q", got, before)
	}
}

// gittree's rule: a quarantine's lock with no recovery.json was stopped
// before anything moved, and restoring it is unlocking it.
func TestRestoreUnlocksAQuarantineLockWithNoRecord(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/orphan-lock")
	dir := testkit.TrashFor(t, ctx)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "lock", "--reason", quarantine.LockReason(dir), path)
	res := refstest.Restore(t, ctx, dir)
	if res.Outcome != quarantine.OutcomeRestored {
		t.Errorf("result %+v", res)
	}
	list, _ := ctx.Repo.Worktrees()
	if wt, _ := list.ByPath(path); wt.Locked {
		t.Error("the lock must be gone")
	}
}

// A removal that stops after any journal write — or after a step it ran but
// did not get to record — is put back by wt restore.
func TestRestorePutsBackARemovalStoppedAtEveryStep(t *testing.T) {
	// Not parallel: swaps quarantine.AfterSave.
	if testing.Short() {
		t.Skip("integration: stops the run at every step, over real repositories")
	}
	points := journalPoints(t, func() { retiretest.Quarantined(t, "fix/count", true) })
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
				if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir}, &buf); err == nil && point != points[len(points)-1] {
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
				refstest.Restore(t, ctx, dir)
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
	// Not parallel: swaps quarantine.AfterSave.
	if testing.Short() {
		t.Skip("integration: stops the run at every step, over real repositories")
	}
	var points []string
	{
		ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/count-restore", false)
		points = journalPoints(t, func() { refstest.Restore(t, ctx, dir) })
	}
	if len(points) < 12 {
		t.Fatalf("points %v", points)
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			ctx, path, branch, tip, dir := retiretest.Quarantined(t, "fix/crash-restore", false)
			testkit.CrashAt(t, point)
			var buf bytes.Buffer
			_ = refs.Restore(ctx, dir, refs.RestoreOptions{}, &buf)
			quarantine.AfterSave = nil
			refstest.Restore(t, ctx, dir)
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

// A restore that stopped after its branch step is not trusted when it
// resumes: the branch is read again before the worktree is registered, and
// a branch that vanished or moved since leaves HEAD detached, never on it.
func TestRestoreResumedReadsTheBranchAgain(t *testing.T) {
	// Not parallel: swaps quarantine.AfterSave.
	for _, change := range []string{"deleted", "moved"} {
		t.Run(change, func(t *testing.T) {
			ctx, path, branch, tip, dir := retiretest.Quarantined(t, "fix/resume-"+change, false)
			testkit.CrashAt(t, "restore.branch:done")
			var buf bytes.Buffer
			_ = refs.Restore(ctx, dir, refs.RestoreOptions{}, &buf)
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
			res := refstest.Restore(t, ctx, dir)
			if res.Action != quarantine.ActionDetach {
				t.Errorf("action %s", res.Action)
			}
			testkit.AssertBack(t, ctx, path, dir, tip)
		})
	}
}

// Config is put back entry by entry, so a restore stopped between two of
// them adds the rest, and none twice.
func TestRestoreFinishesAConfigSectionPutBackInPart(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/two-keys")
	branch := "fix_wt/two-keys"
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch."+branch+".remote", "origin")
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir}, &buf); err != nil {
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
	refstest.Restore(t, ctx, dir)
	got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get-regexp", `^branch\.fix_wt/two-keys\.`))
	want := "branch." + branch + ".remote origin\nbranch." + branch + ".merge refs/heads/" + branch
	if got != want {
		t.Errorf("config:\n%s\nwant:\n%s", got, want)
	}
}

// A rename that went through and a run that stopped before its fsync, and a
// delete that stopped between the ref and its config, are put back too.
func TestRestorePutsBackARemovalStoppedInsideAStep(t *testing.T) {
	// Not parallel: swaps repo.AfterRefDeleted and quarantine.AfterRename.
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
			if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir}, &buf); err == nil {
				t.Fatalf("the run must stop\n%s", buf.String())
			}
			quarantine.AfterRename, repo.AfterRefDeleted = nil, nil
			refstest.Restore(t, ctx, dir)
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
	// Not parallel: swaps repo.BeforeConfigAdd.
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
			if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir}, &buf); err != nil {
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
			if err := refs.Restore(ctx, dir, refs.RestoreOptions{Result: &res}, &buf); err == nil {
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
			res = refstest.Restore(t, ctx, dir)
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
	// Not parallel: swaps repo.BeforeConfigAdd.
	ctx, path := provisiontest.SafetyWorktree(t, "fix/thrice")
	branch := "fix_wt/thrice"
	section := "branch." + branch + "."
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", section+"remote", "origin")
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", section+"merge", "refs/heads/"+branch)
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	dir := testkit.TrashFor(t, ctx)
	var buf bytes.Buffer
	if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir}, &buf); err != nil {
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
	if err := refs.Restore(ctx, dir, refs.RestoreOptions{}, &buf); err == nil {
		t.Fatal("the config write must fail")
	}
	repo.BeforeConfigAdd = nil
	refstest.Restore(t, ctx, dir)
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
	got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get-regexp", `^branch\.fix_wt/thrice\.`))
	want := section + "remote origin\n" + section + "merge refs/heads/" + branch
	if got != want {
		t.Errorf("config:\n%s\nwant:\n%s", got, want)
	}
}

// A quarantine lock whose folder is gone too is still taken off.
func TestRestoreUnlocksAnOrphanWhoseFolderIsGone(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/orphan-gone")
	dir := testkit.TrashFor(t, ctx)
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "lock", "--reason", quarantine.LockReason(dir), path)
	res := refstest.Restore(t, ctx, dir)
	if res.Outcome != quarantine.OutcomeRestored {
		t.Errorf("result %+v", res)
	}
	list, _ := ctx.Repo.Worktrees()
	if wt, _ := list.ByPath(path); wt.Locked {
		t.Error("the lock must be gone")
	}
}
