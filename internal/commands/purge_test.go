package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/quarantine"
)

func purge(t *testing.T, dir string) quarantine.PurgeResult {
	t.Helper()
	var res quarantine.PurgeResult
	var buf bytes.Buffer
	if err := QuarantinePurge(dir, PurgeOptions{Result: &res}, &buf); err != nil {
		t.Fatalf("QuarantinePurge: %v\n%s", err, buf.String())
	}
	return res
}

// purgeRefused runs a purge that must not happen, checks it names code, and
// that it deleted nothing: every file under dir and every pin is still
// there.
func purgeRefused(t *testing.T, ctx *Context, dir, code string) {
	t.Helper()
	before, pins := treeOf(t, dir), pinsOf(t, ctx)
	var plan quarantine.PurgePlan
	var buf bytes.Buffer
	err := QuarantinePurge(dir, PurgeOptions{Planned: func(p quarantine.PurgePlan) { plan = p }}, &buf)
	if err == nil {
		t.Fatalf("want a refusal (%s):\n%s", code, buf.String())
	}
	var codes []string
	for _, p := range plan.Problems {
		codes = append(codes, p.Code)
	}
	if !slices.Contains(codes, code) {
		t.Errorf("problems %v, want %s: %v", codes, code, err)
	}
	if after := treeOf(t, dir); !slices.Equal(after, before) {
		t.Errorf("a refused purge deleted something:\nbefore %v\nafter  %v", before, after)
	}
	if after := pinsOf(t, ctx); after != pins {
		t.Errorf("a refused purge touched the pins:\nbefore %s\nafter  %s", pins, after)
	}
}

// treeOf is every path under dir, symlinks not followed; nil when there is
// no dir.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func pinsOf(t *testing.T, ctx *Context) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "--format=%(objectname) %(refname)",
		quarantine.PinPrefix))
}

// assertPurged checks the folder and every pin are gone.
func assertPurged(t *testing.T, ctx *Context, dir string) {
	t.Helper()
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("the folder must be gone: %v %v", err, treeOf(t, dir))
	}
	if pins := pinsOf(t, ctx); pins != "" {
		t.Errorf("the pins must be gone:\n%s", pins)
	}
}

// A quarantined worktree whose branch was kept and then deleted by hand is
// held by nothing but the pins: the plan counts its commit as lost, and
// after the purge the folder, the pins and every reference to the commit are
// gone.
func TestPurgeDeletesTheFolderAndThePins(t *testing.T) {
	ctx, path, _, tip, dir := quarantined(t, "fix/gone", true)
	gitIn(t, ctx.Repo.MainRoot, "branch", "-D", "gone")
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
	res := purge(t, dir)
	if res.Outcome != quarantine.OutcomePurged {
		t.Errorf("result %+v", res)
	}
	assertPurged(t, ctx, dir)
	if strings.Contains(gitOut(t, ctx.Repo.MainRoot, "rev-list", "--all"), tip) {
		t.Error("nothing may reference the purged commit any more")
	}
	if exists(path) {
		t.Error("the worktree's own path must not come back")
	}
	if list, _ := ctx.Repo.Worktrees(); len(list) != 1 {
		t.Errorf("worktrees %+v", list)
	}
}

// A symlink inside the quarantine is deleted, never what it names.
func TestPurgeNeverFollowsASymlinkOut(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/linked", false)
	outside := filepath.Join(ctx.Repo.Parent, "keep-me")
	mustWrite(t, filepath.Join(outside, "precious.txt"), "precious")
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
	purge(t, dir)
	assertPurged(t, ctx, dir)
	if got := mustRead(t, filepath.Join(outside, "precious.txt")); got != "precious" {
		t.Errorf("what a symlink named was touched: %q", got)
	}
}

// A folder nobody may write to inside the checkout is deleted too.
func TestPurgeDeletesAReadOnlyFolder(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/readonly", false)
	ro := filepath.Join(dir, "checkout", "ro")
	mustWrite(t, filepath.Join(ro, "deep", "f"), "x")
	for _, d := range []string{filepath.Join(ro, "deep"), ro} {
		if err := os.Chmod(d, 0o500); err != nil {
			t.Fatal(err)
		}
	}
	purge(t, dir)
	assertPurged(t, ctx, dir)
}

// Restored already: the folder, with only recovery.json in it, goes, and
// the worktree that came back is untouched.
func TestPurgeAfterRestoreDeletesOnlyTheLeftovers(t *testing.T) {
	ctx, path, branch, _, dir := quarantined(t, "fix/back-then-purged", false)
	restore(t, ctx, dir)
	plan := quarantine.PlanPurge(dir)
	if plan.State != quarantine.StateRestored || plan.Refusal() != "" {
		t.Fatalf("plan %+v", plan)
	}
	if !slices.Equal(plan.Entries, []string{quarantine.FileName}) || len(plan.Lost) != 0 {
		t.Errorf("plan %+v", plan)
	}
	var buf bytes.Buffer
	renderPurgePlan(plan, &buf)
	if !strings.Contains(buf.String(), "restored already") {
		t.Errorf("the plan must say it was restored:\n%s", buf.String())
	}
	purge(t, dir)
	assertPurged(t, ctx, dir)
	assertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
}

// A purge stopped after any step is finished by the next, and in between
// no restore takes the worktree back: its pins may be gone.
func TestPurgeFinishesAPurgeStoppedAtEveryStep(t *testing.T) {
	var points []string
	{
		_, _, _, _, dir := quarantined(t, "fix/count-purge", true)
		t.Cleanup(func() { quarantine.AfterPurge, quarantine.AfterSave = nil, nil })
		quarantine.AfterSave = func(_ *quarantine.Record, at string) error {
			points = append(points, at)
			return nil
		}
		quarantine.AfterPurge = func(at string) error {
			points = append(points, at)
			return nil
		}
		purge(t, dir)
		quarantine.AfterPurge, quarantine.AfterSave = nil, nil
	}
	want := []string{"purge.begin", "pin:head", "pin:tip", "pin:dir", "entry:checkout", "entry:admin", "record"}
	if !slices.Equal(points, want) {
		t.Fatalf("points %v, want %v", points, want)
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			ctx, _, _, _, dir := quarantined(t, "fix/crash-purge", true)
			t.Cleanup(func() { quarantine.AfterPurge, quarantine.AfterSave = nil, nil })
			if strings.HasPrefix(point, "purge.") {
				crashAt(t, point)
			} else {
				quarantine.AfterPurge = func(at string) error {
					if at == point {
						return errCrash
					}
					return nil
				}
			}
			var res quarantine.PurgeResult
			var buf bytes.Buffer
			if err := QuarantinePurge(dir, PurgeOptions{Result: &res}, &buf); err == nil {
				t.Fatalf("the purge must stop at %s", point)
			}
			quarantine.AfterPurge, quarantine.AfterSave = nil, nil
			if res.Outcome != quarantine.OutcomePartial {
				t.Errorf("outcome %s", res.Outcome)
			}
			if point != "record" {
				if err := Restore(ctx, dir, RestoreOptions{}, &buf); err == nil {
					t.Fatal("a restore must refuse a quarantine being purged")
				}
			}
			if point == "record" {
				// Stopped between its journal and the folder: nothing names
				// it any more, and an empty folder is left alone.
				if plan := quarantine.PlanPurge(dir); plan.State != quarantine.StateEmpty || plan.Refusal() == "" {
					t.Errorf("plan %+v", plan)
				}
				if pins := pinsOf(t, ctx); pins != "" || len(treeOf(t, dir)) != 1 {
					t.Errorf("only an empty folder may be left: %v %s", treeOf(t, dir), pins)
				}
				return
			}
			if plan := quarantine.PlanPurge(dir); plan.State != quarantine.StatePurging {
				t.Errorf("state %s: %s", plan.State, plan.Refusal())
			}
			purge(t, dir)
			assertPurged(t, ctx, dir)
		})
	}
}

// Everything that is not a settled quarantine, named by a record written
// for exactly this folder, is refused before anything is deleted.
func TestPurgeRefuses(t *testing.T) {
	t.Run("no folder", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/r-missing", false)
		purgeRefused(t, ctx, filepath.Join(dir, "nope"), quarantine.ProblemMissing)
	})
	t.Run("no record", func(t *testing.T) {
		ctx, _ := safetyWorktree(t, "fix/r-norecord")
		dir := filepath.Join(ctx.Repo.Parent, "just-files")
		mustWrite(t, filepath.Join(dir, "precious.txt"), "precious")
		purgeRefused(t, ctx, dir, quarantine.ProblemNoRecord)
	})
	t.Run("record not wt's", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/r-invalid", false)
		mustWrite(t, filepath.Join(dir, quarantine.FileName), `{"schema":1,"steps":[]}`)
		purgeRefused(t, ctx, dir, quarantine.ProblemNoRecord)
	})
	t.Run("symlink", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/r-symlink", false)
		link := dir + "-link"
		if err := os.Symlink(dir, link); err != nil {
			t.Fatal(err)
		}
		purgeRefused(t, ctx, link, quarantine.ProblemNotAFolder)
	})
	t.Run("record copied elsewhere", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/r-copied", false)
		other := filepath.Join(ctx.Repo.Parent, "other")
		mustWrite(t, filepath.Join(other, "checkout", "precious.txt"), "precious")
		mustWrite(t, filepath.Join(other, quarantine.FileName), mustRead(t, filepath.Join(dir, quarantine.FileName)))
		purgeRefused(t, ctx, other, quarantine.ProblemWrongFolder)
	})
	t.Run("folder moved", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/r-moved", false)
		moved := dir + "-moved"
		if err := os.Rename(dir, moved); err != nil {
			t.Fatal(err)
		}
		purgeRefused(t, ctx, moved, quarantine.ProblemWrongFolder)
	})
	t.Run("pin moved", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/r-pin", false)
		r := loadRecord(t, dir)
		gitIn(t, ctx.Repo.MainRoot, "commit", "-q", "--allow-empty", "-m", "elsewhere")
		gitIn(t, ctx.Repo.MainRoot, "update-ref", *r.Pins.Head, "HEAD")
		purgeRefused(t, ctx, dir, quarantine.ProblemPins)
	})
	t.Run("pin renamed in the record", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/r-pinname", false)
		r := loadRecord(t, dir)
		r.Pins.Dir = "refs/heads/main"
		if err := r.Save("test"); err != nil {
			t.Fatal(err)
		}
		purgeRefused(t, ctx, dir, quarantine.ProblemPins)
	})
	t.Run("removal stopped", func(t *testing.T) {
		ctx, path := safetyWorktree(t, "fix/r-removal")
		dir := trashFor(t, ctx)
		crashAt(t, "moveAdmin:running")
		var buf bytes.Buffer
		_ = RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf)
		quarantine.AfterSave = nil
		purgeRefused(t, ctx, dir, quarantine.ProblemUnsettled)
	})
	t.Run("removal stopped at its branch step", func(t *testing.T) {
		ctx, path := safetyWorktree(t, "fix/r-branchstep")
		dir := trashFor(t, ctx)
		crashAt(t, "branch:running")
		var buf bytes.Buffer
		_ = RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf)
		quarantine.AfterSave = nil
		purgeRefused(t, ctx, dir, quarantine.ProblemUnsettled)
	})
	t.Run("restore stopped", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/r-restore", false)
		crashAt(t, "restore.moveAdmin:done")
		var buf bytes.Buffer
		_ = Restore(ctx, dir, RestoreOptions{}, &buf)
		quarantine.AfterSave = nil
		purgeRefused(t, ctx, dir, quarantine.ProblemUnsettled)
	})
	t.Run("worktree registered inside", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/r-registered", false)
		gitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "--detach", filepath.Join(dir, "checkout", "nested"))
		purgeRefused(t, ctx, dir, quarantine.ProblemRegistered)
	})
	t.Run("worktree locked for it", func(t *testing.T) {
		ctx, path, _, _, dir := quarantined(t, "fix/r-locked", false)
		restore(t, ctx, dir)
		gitIn(t, ctx.Repo.MainRoot, "worktree", "lock", "--reason", quarantine.LockReason(dir), path)
		purgeRefused(t, ctx, dir, quarantine.ProblemLocked)
	})
	t.Run("inside a checkout", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/r-inside", false)
		inside := filepath.Join(ctx.Repo.MainRoot, "trash")
		if err := os.Rename(dir, inside); err != nil {
			t.Fatal(err)
		}
		// The record rewritten for where it is now, as if it were made there.
		data := strings.ReplaceAll(mustRead(t, filepath.Join(inside, quarantine.FileName)), dir, inside)
		mustWrite(t, filepath.Join(inside, quarantine.FileName), data)
		purgeRefused(t, ctx, inside, quarantine.ProblemLocation)
	})
}

// --expect holds the purge to the plan a tool read: a pin moved since
// refuses it, the plan's own token purges.
func TestPurgeExpectHoldsItToThePlan(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/expect", false)
	var plan bytes.Buffer
	if err := QuarantinePurgePlanJSON(dir, &plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var p PurgePlanOutput
	if err := json.Unmarshal(plan.Bytes(), &p); err != nil || p.Token == nil {
		t.Fatalf("plan %s", plan.String())
	}
	r := loadRecord(t, dir)
	head := strings.TrimSpace(gitOut(t, ctx.Repo.MainRoot, "rev-parse", *r.Pins.Head))
	gitIn(t, ctx.Repo.MainRoot, "update-ref", "-d", *r.Pins.Head)
	var out bytes.Buffer
	if err := QuarantinePurgeJSON(dir, PurgeOptions{Expect: *p.Token}, &out, &bytes.Buffer{}); err == nil {
		t.Fatal("a plan that changed must refuse")
	}
	var o PurgeOutput
	_ = json.Unmarshal(out.Bytes(), &o)
	if o.Outcome != quarantine.OutcomeRefused || len(o.Problems) != 1 || o.Problems[0].Code != ProblemPlanChanged {
		t.Errorf("result %s", out.String())
	}
	if !exists(dir) {
		t.Fatal("a refused purge deleted the folder")
	}
	gitIn(t, ctx.Repo.MainRoot, "update-ref", *r.Pins.Head, head)
	out.Reset()
	if err := QuarantinePurgeJSON(dir, PurgeOptions{Expect: *p.Token}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	assertPurged(t, ctx, dir)
}

// The question is asked once; no is nothing deleted, and with nobody to ask
// and no --yes the purge refuses.
func TestPurgeAsksAndRefusesWithNobodyToAsk(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/ask", false)
	var buf bytes.Buffer
	asked := 0
	no := func() bool { asked++; return false }
	if err := QuarantinePurge(dir, PurgeOptions{Confirm: no}, &buf); err != nil || asked != 1 {
		t.Fatalf("%v, asked %d\n%s", err, asked, buf.String())
	}
	if !exists(dir) {
		t.Fatal("no must delete nothing")
	}
	if err := QuarantinePurge(dir, PurgeOptions{NoTerminal: true}, &buf); err == nil {
		t.Fatal("with nobody to ask it must refuse")
	}
	if err := QuarantinePurge(dir, PurgeOptions{DryRun: true}, &buf); err != nil || !exists(dir) {
		t.Fatalf("--dry-run: %v", err)
	}
	yes := func() bool { return true }
	if err := QuarantinePurge(dir, PurgeOptions{Confirm: yes}, &buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	assertPurged(t, ctx, dir)
}

// The plan asked about is the one purged: a pin that moved while the
// question was open refuses the purge.
func TestPurgeReadsThePlanAgainAfterTheQuestion(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/ask-changed", false)
	r := loadRecord(t, dir)
	yes := func() bool {
		gitIn(t, ctx.Repo.MainRoot, "update-ref", "-d", *r.Pins.Head)
		return true
	}
	var buf bytes.Buffer
	if err := QuarantinePurge(dir, PurgeOptions{Confirm: yes}, &buf); err == nil {
		t.Fatal("a plan that changed under the question must refuse")
	}
	if !exists(filepath.Join(dir, "checkout")) {
		t.Error("nothing may be deleted")
	}
}

func TestPurgeJSONMatchesItsSchemas(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/purge-json", false)
	var plan bytes.Buffer
	if err := QuarantinePurgePlanJSON(dir, &plan, &bytes.Buffer{}); err != nil {
		t.Fatalf("%v\n%s", err, plan.String())
	}
	validateJSON(t, "quarantine-purge-plan", plan.Bytes())

	crashAt(t, "purge.begin")
	_ = QuarantinePurge(dir, PurgeOptions{}, &bytes.Buffer{})
	quarantine.AfterSave = nil
	if exists(filepath.Join(dir, quarantine.FileName)) {
		t.Error("recovery.json must be gone once the purge began: an older wt restore would read it")
	}
	validateJSON(t, "recovery", []byte(mustRead(t, filepath.Join(dir, quarantine.PurgingName))))
	var restorePlan bytes.Buffer
	_ = RestoreJSON(ctx, dir, RestoreOptions{DryRun: true}, &restorePlan, &bytes.Buffer{})
	validateJSON(t, "restore-plan", restorePlan.Bytes())

	var out bytes.Buffer
	if err := QuarantinePurgeJSON(dir, PurgeOptions{}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	validateJSON(t, "quarantine-purge", out.Bytes())
	var o PurgeOutput
	_ = json.Unmarshal(out.Bytes(), &o)
	if o.Outcome != quarantine.OutcomePurged || !o.FolderDeleted || o.State == nil || *o.State != quarantine.StatePurging {
		t.Errorf("result %s", out.String())
	}
	for _, pin := range o.Pins {
		if pin.Result != PinDeleted {
			t.Errorf("pin %+v", pin)
		}
	}

	var refused bytes.Buffer
	if err := QuarantinePurgePlanJSON(dir, &refused, &bytes.Buffer{}); err == nil {
		t.Error("want an error for a folder that is not there")
	}
	validateJSON(t, "quarantine-purge-plan", refused.Bytes())
	refused.Reset()
	if err := QuarantinePurgeJSON(dir, PurgeOptions{}, &refused, &bytes.Buffer{}); err == nil {
		t.Error("want an error for a folder that is not there")
	}
	validateJSON(t, "quarantine-purge", refused.Bytes())
}

// The journal a purge writes when a signal ends it reads each pin and the
// folder as they are.
func TestPurgeJournalWritesOneObjectWhenInterrupted(t *testing.T) {
	_, _, _, _, dir := quarantined(t, "fix/purge-signal", false)
	var out bytes.Buffer
	j := &purgeJournal{out: &out, dir: dir, plan: quarantine.PlanPurge(dir)}
	j.interrupted("", "interrupted")
	j.write(quarantine.OutcomePurged, nil, "")
	validateJSON(t, "quarantine-purge", out.Bytes())
	var o PurgeOutput
	if err := json.Unmarshal(out.Bytes(), &o); err != nil {
		t.Fatalf("one object: %v\n%s", err, out.String())
	}
	if o.Outcome != OutcomeInterrupted || o.FolderDeleted {
		t.Errorf("result %s", out.String())
	}
	for _, pin := range o.Pins {
		if pin.Result != PinKept {
			t.Errorf("pin %+v", pin)
		}
	}
}

// A symlink left at the journal's temp name is replaced, never written
// through.
func TestPurgeNeverWritesThroughAJournalSymlink(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/tmp-link", false)
	outside := filepath.Join(ctx.Repo.Parent, "precious.txt")
	mustWrite(t, outside, "precious")
	for _, name := range []string{quarantine.FileName + ".tmp", quarantine.PurgingName + ".tmp"} {
		if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	purge(t, dir)
	assertPurged(t, ctx, dir)
	if got := mustRead(t, outside); got != "precious" {
		t.Errorf("a file outside was written: %q", got)
	}
}

// The folder swapped for a symlink to another while the purge runs: every
// deletion stays inside the folder it opened, and the other is untouched.
func TestPurgeStaysInTheFolderItOpened(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/swapped", false)
	victim := filepath.Join(ctx.Repo.Parent, "victim")
	mustWrite(t, filepath.Join(victim, "checkout", "precious.txt"), "precious")
	mustWrite(t, filepath.Join(victim, "admin", "precious.txt"), "precious")
	moved := dir + "-moved"
	t.Cleanup(func() { quarantine.AfterPurge = nil })
	quarantine.AfterPurge = func(at string) error {
		if at == "pin:dir" {
			if err := os.Rename(dir, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(victim, dir); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	var buf bytes.Buffer
	if err := QuarantinePurge(dir, PurgeOptions{}, &buf); err == nil {
		t.Fatalf("the swapped path must not be removed as the folder\n%s", buf.String())
	}
	for _, f := range []string{"checkout", "admin"} {
		if got := mustRead(t, filepath.Join(victim, f, "precious.txt")); got != "precious" {
			t.Errorf("the other folder lost %s", f)
		}
	}
	if exists(filepath.Join(moved, "checkout")) {
		t.Error("the folder it opened must have been emptied")
	}
}

// A plan is only carried out as read: a file added to the checkout since,
// or a worktree registered inside it once the purge began, stops it.
func TestPurgeGoesByThePlanItWasGiven(t *testing.T) {
	t.Run("file added", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/stale-file", false)
		plan := quarantine.PlanPurge(dir)
		mustWrite(t, filepath.Join(dir, "checkout", "sub", "new.txt"), "new")
		if _, err := quarantine.DoPurge(plan); !errors.Is(err, quarantine.ErrPurgeChanged) {
			t.Fatalf("want ErrPurgeChanged, got %v", err)
		}
		if !exists(filepath.Join(dir, "checkout", "sub", "new.txt")) || pinsOf(t, ctx) == "" {
			t.Error("nothing may be deleted")
		}
	})
	t.Run("worktree registered", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/stale-wt", false)
		nested := filepath.Join(dir, "checkout", "nested")
		t.Cleanup(func() { quarantine.AfterSave = nil })
		quarantine.AfterSave = func(_ *quarantine.Record, at string) error {
			if at == "purge.begin" {
				gitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "--detach", nested)
			}
			return nil
		}
		var buf bytes.Buffer
		if err := QuarantinePurge(dir, PurgeOptions{}, &buf); err == nil {
			t.Fatal("a worktree registered inside must stop the purge")
		}
		if !exists(filepath.Join(nested, ".git")) {
			t.Error("the registered worktree must survive")
		}
	})
	t.Run("restore holds the lock", func(t *testing.T) {
		_, _, _, _, dir := quarantined(t, "fix/stale-lock", false)
		unlock, err := quarantine.LockDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		if _, err := quarantine.DoPurge(quarantine.PlanPurge(dir)); err == nil || !exists(dir) {
			t.Fatalf("a purge must wait for nobody: %v", err)
		}
	})
}

// A record copied into a new folder at the quarantine's old path, the
// quarantine itself moved away: what is there is not what the removal
// moved, and it is refused.
func TestPurgeRefusesACopiedRecordAtAReusedPath(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/reused", false)
	record := mustRead(t, filepath.Join(dir, quarantine.FileName))
	if err := os.Rename(dir, dir+"-away"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "checkout", "precious.txt"), "precious")
	mustWrite(t, filepath.Join(dir, quarantine.FileName), record)
	purgeRefused(t, ctx, dir, quarantine.ProblemWrongFolder)
}

// Named in another case on a volume that ignores case, the folder is still
// the one a registered worktree is inside.
func TestPurgeComparesFoldersNotSpellings(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/cased", false)
	upper := filepath.Join(filepath.Dir(dir), strings.ToUpper(filepath.Base(dir)))
	if _, err := os.Lstat(upper); err != nil {
		t.Skip("this volume tells case apart")
	}
	gitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "--detach", filepath.Join(dir, "checkout", "nested"))
	purgeRefused(t, ctx, upper, quarantine.ProblemRegistered)
}

// A purge begun through one spelling of the folder — a symlink on the way —
// is finished through another: the record keeps the folder it names.
func TestPurgeBegunThroughAnAliasIsFinishedThroughTheName(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/alias", false)
	link := filepath.Join(ctx.Repo.Parent, "trash-link")
	if err := os.Symlink(filepath.Dir(dir), link); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(link, filepath.Base(dir))
	crashAt(t, "purge.begin")
	_ = QuarantinePurge(alias, PurgeOptions{}, &bytes.Buffer{})
	quarantine.AfterSave = nil
	purge(t, dir)
	assertPurged(t, ctx, dir)
}

// A pin git cannot read is not taken for one that is gone.
func TestPurgeRefusesAPinItCannotRead(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/broken-pin", false)
	r := loadRecord(t, dir)
	mustWrite(t, filepath.Join(ctx.Repo.MainRoot, ".git", *r.Pins.Head), "garbage\n")
	purgeRefused(t, ctx, dir, quarantine.ProblemPinsUnreadable)
}

// A live quarantine inside the folder is never purged with it: not at its
// top level, which only ever holds what a quarantine leaves there, and not
// deeper, where a record of one is found.
func TestPurgeNeverDeletesAQuarantineInsideIt(t *testing.T) {
	t.Run("beside the record of a restored one", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/outer-restored", false)
		restore(t, ctx, dir)
		_, _, _, _, inner := quarantined(t, "fix/inner-a", false)
		if err := os.Rename(inner, filepath.Join(dir, "inner")); err != nil {
			t.Fatal(err)
		}
		purgeRefused(t, ctx, dir, quarantine.ProblemForeignEntry)
	})
	t.Run("inside the quarantined checkout", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/outer-q", false)
		_, _, _, _, inner := quarantined(t, "fix/inner-b", false)
		if err := os.MkdirAll(filepath.Join(dir, "checkout", "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(inner, filepath.Join(dir, "checkout", "sub", "inner")); err != nil {
			t.Fatal(err)
		}
		purgeRefused(t, ctx, dir, quarantine.ProblemNestedQuarantine)
	})
	t.Run("a project file named recovery.json is not one", func(t *testing.T) {
		ctx, _, _, _, dir := quarantined(t, "fix/outer-file", false)
		mustWrite(t, filepath.Join(dir, "checkout", quarantine.FileName), `{"name":"not wt's"}`)
		purge(t, dir)
		assertPurged(t, ctx, dir)
	})
}

// wt remove --quarantine refuses a folder inside a quarantine, which a
// purge of that one would delete.
func TestQuarantineRefusesAFolderInsideAQuarantine(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/host", false)
	restore(t, ctx, dir)
	_, path := safetyWorktreeIn(t, ctx, "fix/guest")
	var buf bytes.Buffer
	err := RemoveAt(ctx, path, RemoveOptions{Quarantine: filepath.Join(dir, "inner")}, &buf)
	if err == nil || !strings.Contains(err.Error()+buf.String(), "inside the quarantine") {
		t.Fatalf("want a refusal naming the quarantine it is in: %v\n%s", err, buf.String())
	}
	if !exists(path) {
		t.Error("the worktree must stay")
	}
}

// An empty folder names nothing wt owns: refused, and left as it is.
func TestPurgeRefusesAnEmptyFolder(t *testing.T) {
	ctx, _ := safetyWorktree(t, "fix/empty")
	dir := filepath.Join(ctx.Repo.MainRoot, "empty")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	purgeRefused(t, ctx, dir, quarantine.ProblemEmpty)
	if !exists(dir) {
		t.Error("the folder must stay")
	}
}

// Its repository deleted, and the pins with it: the purge goes ahead on the
// record alone, and says why there are no pins to delete.
func TestPurgeAfterTheRepositoryIsGone(t *testing.T) {
	ctx, _, _, _, dir := quarantined(t, "fix/orphaned", false)
	if err := os.RemoveAll(ctx.Repo.MainRoot); err != nil {
		t.Fatal(err)
	}
	plan := quarantine.PlanPurge(dir)
	if plan.Refusal() != "" || !plan.RepoGone || plan.State != quarantine.StateQuarantined {
		t.Fatalf("plan %+v", plan)
	}
	var buf bytes.Buffer
	renderPurgePlan(plan, &buf)
	if !strings.Contains(buf.String(), "is gone") {
		t.Errorf("the plan must say the repository is gone:\n%s", buf.String())
	}
	var out bytes.Buffer
	if err := QuarantinePurgeJSON(dir, PurgeOptions{}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	validateJSON(t, "quarantine-purge", out.Bytes())
	if exists(dir) {
		t.Error("the folder must be gone")
	}
}

// safetyWorktreeIn is a wt worktree for work in ctx's repository.
func safetyWorktreeIn(t *testing.T, ctx *Context, work string) (*Context, string) {
	t.Helper()
	var buf bytes.Buffer
	path, err := New(ctx, work, NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return ctx, path
}
