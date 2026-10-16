package purge_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/purge"
	"github.com/anders-lindstrom/wt/internal/commands/purge/purgetest"
	"github.com/anders-lindstrom/wt/internal/commands/refs"
	"github.com/anders-lindstrom/wt/internal/commands/refs/refstest"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// A quarantined worktree whose branch was kept and then deleted by hand is
// held by nothing but the pins: the plan counts its commit as lost, and
// after the purge the folder, the pins and every reference to the commit are
// gone.
func TestPurgeDeletesTheFolderAndThePins(t *testing.T) {
	t.Parallel()
	ctx, path, _, tip, dir := retiretest.Quarantined(t, "fix/gone", true)
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "-D", "gone")
	plan := quarantine.PlanPurge(dir)
	if plan.State != quarantine.StateQuarantined || plan.Refusal() != "" {
		t.Fatalf("plan %+v", plan)
	}
	if len(plan.Pins) != 3 {
		t.Errorf("pins %+v", plan.Pins)
	}
	if len(plan.Lost) != 1 || plan.Lost[0].OID != tip || plan.Lost[0].Count != 1 {
		t.Errorf("lost %+v", plan.Lost)
	}
	if !slices.Equal(plan.Entries, []string{"admin", "checkout", quarantine.FileName}) || plan.Bytes <= 0 {
		t.Errorf("entries %v, %d bytes", plan.Entries, plan.Bytes)
	}
	res := purgetest.Purge(t, dir)
	if res.Outcome != quarantine.OutcomePurged {
		t.Errorf("result %+v", res)
	}
	testkit.AssertPurged(t, ctx, dir)
	if strings.Contains(testkit.GitOut(t, ctx.Repo.MainRoot, "rev-list", "--all"), tip) {
		t.Error("nothing may reference the purged commit any more")
	}
	if testkit.Exists(path) {
		t.Error("the worktree's own path must not come back")
	}
	if list, _ := ctx.Repo.Worktrees(); len(list) != 1 {
		t.Errorf("worktrees %+v", list)
	}
}

// A symlink inside the quarantine is deleted, never what it names.
func TestPurgeNeverFollowsASymlinkOut(t *testing.T) {
	t.Parallel()
	ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/linked", false)
	outside := filepath.Join(ctx.Repo.Parent, "keep-me")
	testkit.MustWrite(t, filepath.Join(outside, "precious.txt"), "precious")
	for _, link := range []string{filepath.Join(dir, "checkout", "out"), filepath.Join(dir, "admin", "out"),
		filepath.Join(dir, "checkout", "precious.txt")} {
		target := outside
		if filepath.Base(link) == "precious.txt" {
			target = filepath.Join(outside, "precious.txt")
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	purgetest.Purge(t, dir)
	testkit.AssertPurged(t, ctx, dir)
	if got := testkit.MustRead(t, filepath.Join(outside, "precious.txt")); got != "precious" {
		t.Errorf("what a symlink named was touched: %q", got)
	}
}

// A folder nobody may write to inside the checkout is deleted too.
func TestPurgeDeletesAReadOnlyFolder(t *testing.T) {
	t.Parallel()
	ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/readonly", false)
	ro := filepath.Join(dir, "checkout", "ro")
	testkit.MustWrite(t, filepath.Join(ro, "deep", "f"), "x")
	for _, d := range []string{filepath.Join(ro, "deep"), ro} {
		if err := os.Chmod(d, 0o500); err != nil {
			t.Fatal(err)
		}
	}
	purgetest.Purge(t, dir)
	testkit.AssertPurged(t, ctx, dir)
}

// Restored already: the folder, with only recovery.json in it, goes, and
// the worktree that came back is untouched.
func TestPurgeAfterRestoreDeletesOnlyTheLeftovers(t *testing.T) {
	t.Parallel()
	ctx, path, branch, _, dir := retiretest.Quarantined(t, "fix/back-then-purged", false)
	refstest.Restore(t, ctx, dir)
	plan := quarantine.PlanPurge(dir)
	if plan.State != quarantine.StateRestored || plan.Refusal() != "" {
		t.Fatalf("plan %+v", plan)
	}
	if !slices.Equal(plan.Entries, []string{quarantine.FileName}) || len(plan.Lost) != 0 {
		t.Errorf("plan %+v", plan)
	}
	var buf bytes.Buffer
	purge.RenderPurgePlan(plan, &buf)
	if !strings.Contains(buf.String(), "restored already") {
		t.Errorf("the plan must say it was restored:\n%s", buf.String())
	}
	purgetest.Purge(t, dir)
	testkit.AssertPurged(t, ctx, dir)
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
}

// A purge stopped after any step is finished by the next, and in between
// no restore takes the worktree back: its pins may be gone.
func TestPurgeFinishesAPurgeStoppedAtEveryStep(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: stops the run at every step, over real repositories")
	}
	var points []string
	{
		_, _, _, _, dir := retiretest.Quarantined(t, "fix/count-purge", true)
		purgetest.PurgeWith(t, dir, &quarantine.Hooks{
			AfterSave: func(_ *quarantine.Record, at string) error {
				points = append(points, at)
				return nil
			},
			AfterPurge: func(at string) error {
				points = append(points, at)
				return nil
			},
		})
	}
	want := []string{"purge.begin", "pin:head", "pin:tip", "pin:dir", "entry:checkout", "entry:admin", "record"}
	if !slices.Equal(points, want) {
		t.Fatalf("points %v, want %v", points, want)
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/crash-purge", true)
			h := testkit.CrashAt(point)
			if !strings.HasPrefix(point, "purge.") {
				h = &quarantine.Hooks{AfterPurge: func(at string) error {
					if at == point {
						return testkit.ErrCrash
					}
					return nil
				}}
			}
			var res quarantine.PurgeResult
			var buf bytes.Buffer
			if err := purge.QuarantinePurge(dir, purge.Options{Result: &res, QuarantineHooks: h}, &buf); err == nil {
				t.Fatalf("the purge must stop at %s", point)
			}
			if res.Outcome != quarantine.OutcomePartial {
				t.Errorf("outcome %s", res.Outcome)
			}
			if point != "record" {
				if err := refs.Restore(ctx, dir, refs.RestoreOptions{}, &buf); err == nil {
					t.Fatal("a restore must refuse a quarantine being purged")
				}
			}
			if point == "record" {
				// Stopped between its journal and the folder: nothing names
				// it any more, and an empty folder is left alone.
				if plan := quarantine.PlanPurge(dir); plan.State != quarantine.StateEmpty || plan.Refusal() == "" {
					t.Errorf("plan %+v", plan)
				}
				if pins := testkit.PinsOf(t, ctx); pins != "" || len(testkit.TreeOf(t, dir)) != 1 {
					t.Errorf("only an empty folder may be left: %v %s", testkit.TreeOf(t, dir), pins)
				}
				return
			}
			if plan := quarantine.PlanPurge(dir); plan.State != quarantine.StatePurging {
				t.Errorf("state %s: %s", plan.State, plan.Refusal())
			}
			purgetest.Purge(t, dir)
			testkit.AssertPurged(t, ctx, dir)
		})
	}
}

// A symlink left at the journal's temp name is replaced, never written
// through.
func TestPurgeNeverWritesThroughAJournalSymlink(t *testing.T) {
	t.Parallel()
	ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/tmp-link", false)
	outside := filepath.Join(ctx.Repo.Parent, "precious.txt")
	testkit.MustWrite(t, outside, "precious")
	for _, name := range []string{quarantine.FileName + ".tmp", quarantine.PurgingName + ".tmp"} {
		if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	purgetest.Purge(t, dir)
	testkit.AssertPurged(t, ctx, dir)
	if got := testkit.MustRead(t, outside); got != "precious" {
		t.Errorf("a file outside was written: %q", got)
	}
}

// A purge begun through one spelling of the folder — a symlink on the way —
// is finished through another: the record keeps the folder it names.
func TestPurgeBegunThroughAnAliasIsFinishedThroughTheName(t *testing.T) {
	t.Parallel()
	ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/alias", false)
	link := filepath.Join(ctx.Repo.Parent, "trash-link")
	if err := os.Symlink(filepath.Dir(dir), link); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(link, filepath.Base(dir))
	if err := purge.QuarantinePurge(alias, purge.Options{QuarantineHooks: testkit.CrashAt("purge.begin")}, &bytes.Buffer{}); !errors.Is(err, testkit.ErrCrash) {
		t.Fatalf("the purge through the alias: err %v, want the injected crash", err)
	}
	purgetest.Purge(t, dir)
	testkit.AssertPurged(t, ctx, dir)
}

// A live quarantine inside the folder is never purged with it: not at its
// top level, which only ever holds what a quarantine leaves there, and not
// deeper, where a record of one is found.
func TestPurgeNeverDeletesAQuarantineInsideIt(t *testing.T) {
	t.Parallel()
	t.Run("beside the record of a restored one", func(t *testing.T) {
		ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/outer-restored", false)
		refstest.Restore(t, ctx, dir)
		_, _, _, _, inner := retiretest.Quarantined(t, "fix/inner-a", false)
		if err := os.Rename(inner, filepath.Join(dir, "inner")); err != nil {
			t.Fatal(err)
		}
		purge.PurgeRefused(t, ctx, dir, quarantine.ProblemForeignEntry)
	})
	t.Run("inside the quarantined checkout", func(t *testing.T) {
		ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/outer-q", false)
		_, _, _, _, inner := retiretest.Quarantined(t, "fix/inner-b", false)
		if err := os.MkdirAll(filepath.Join(dir, "checkout", "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(inner, filepath.Join(dir, "checkout", "sub", "inner")); err != nil {
			t.Fatal(err)
		}
		purge.PurgeRefused(t, ctx, dir, quarantine.ProblemNestedQuarantine)
	})
	t.Run("a project file named recovery.json is not one", func(t *testing.T) {
		ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/outer-file", false)
		testkit.MustWrite(t, filepath.Join(dir, "checkout", quarantine.FileName), `{"name":"not wt's"}`)
		purgetest.Purge(t, dir)
		testkit.AssertPurged(t, ctx, dir)
	})
}
