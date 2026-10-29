package quarantinecmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// The original path taken by anything but this worktree refuses.
func TestRestoreRefusesWhenTheOriginalPathIsTaken(t *testing.T) {
	t.Parallel()
	ctx, path, _, _, dir := retiretest.Quarantined(t, "fix/path-taken", false)
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
	t.Parallel()
	ctx, path, _, _, dir := retiretest.Quarantined(t, "fix/restore-volume", false)
	h := &quarantine.Hooks{DeviceOf: func(p string) (uint64, error) {
		if p == filepath.Dir(path) {
			return 4242, nil
		}
		return quarantine.DeviceOf(p)
	}}
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{QuarantineHooks: h}, &buf); err == nil || !strings.Contains(err.Error(), "volume") {
		t.Fatalf("want a refusal: %v\n%s", err, buf.String())
	}
}

// --dry-run says what it would do to the branch and changes nothing.
func TestRestoreDryRunChangesNothing(t *testing.T) {
	t.Parallel()
	ctx, _, branch, _, dir := retiretest.Quarantined(t, "fix/dry", false)
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

func TestRestoreRefusesAFolderWithNoRecordAndNoLock(t *testing.T) {
	t.Parallel()
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

func TestRestoreJSONMatchesItsSchemas(t *testing.T) {
	t.Parallel()
	ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/json", false)
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

// Only the directories the removal moved go back: something put in their
// place in the quarantine is refused before anything is written.
func TestRestoreRefusesADirectoryPutInTheQuarantine(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"checkout", "admin"} {
		t.Run(which, func(t *testing.T) {
			ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/swapped-"+which, false)
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

// A path taken between the plan and the move is a failure, never a move
// journalled as done.
func TestRestoreFailsWhenAPathIsTakenMidway(t *testing.T) {
	t.Parallel()
	ctx, path, _, _, dir := retiretest.Quarantined(t, "fix/taken-midway", false)
	h := &quarantine.Hooks{AfterSave: func(_ *quarantine.Record, at string) error {
		if at == "restore.moveCheckout:running" {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}}
	var res quarantine.RestoreResult
	var buf bytes.Buffer
	if err := Restore(ctx, dir, RestoreOptions{Result: &res, QuarantineHooks: h}, &buf); err == nil {
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
