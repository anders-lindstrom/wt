package quarantine

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

const testHead = "1111111111111111111111111111111111111111"

// quarantined builds a finished quarantine without git: a folder holding
// checkout/ and admin/, each with a file in it, and a recovery.json naming
// them by inode, every removal step done. Its repository is a path that was
// never made, so PlanPurge reads it as a repository gone and decides on the
// record and the folder alone, and PlanRestore fails to open it without
// running git. It returns the folder and the record as saved.
func quarantined(t *testing.T) (string, *Record) {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "q")
	repo := filepath.Join(base, "repo")
	head := testHead
	r, err := Begin(dir, Record{Command: "remove", Repo: repo, CommonDir: filepath.Join(repo, ".git"), WorktreeID: "x",
		GitFile:  "gitdir: " + filepath.Join(repo, ".git", "worktrees", "x") + "\n",
		Checkout: Place{Path: filepath.Join(base, "wt", "x"), Quarantined: filepath.Join(dir, "checkout")},
		Admin:    Place{Path: filepath.Join(repo, ".git", "worktrees", "x"), Quarantined: filepath.Join(dir, "admin")},
		Head:     &head,
		Branch:   &Branch{Name: "fix/a", Tip: head, Plan: BranchPlanDelete}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, pl := range []*Place{&r.Checkout, &r.Admin} {
		gittest.WriteFile(t, filepath.Join(pl.Quarantined, "f"), "x")
		id, err := Identify(pl.Quarantined)
		if err != nil {
			t.Fatal(err)
		}
		pl.Device, pl.Inode = id.Device, id.Inode
	}
	for i := range r.Steps {
		r.Steps[i].State = Done
	}
	deleted := BranchDeleted
	r.Branch.Result = &deleted
	save(t, r)
	return dir, r
}

func save(t *testing.T, r *Record) {
	t.Helper()
	if err := r.Save("test"); err != nil {
		t.Fatal(err)
	}
}

func rename(t *testing.T, from, to string) {
	t.Helper()
	mkdir(t, filepath.Dir(to))
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func codes(ps []Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

// restored marks the record's restore finished and takes both directories
// out of the folder, as wt restore leaves it: the checkout back at its path,
// the admin dir gone with the repository, which is never made, so the plan
// still runs no git.
func restored(t *testing.T, r *Record) {
	t.Helper()
	r.Restore = &Restore{Steps: pendingSteps(restoreSteps)}
	for i := range r.Restore.Steps {
		r.Restore.Steps[i].State = Done
	}
	save(t, r)
	rename(t, r.Checkout.Quarantined, r.Checkout.Path)
	if err := os.RemoveAll(r.Admin.Quarantined); err != nil {
		t.Fatal(err)
	}
}

// PlanPurge decides whether a folder is deleted. It goes ahead only on a
// record a removal wrote for exactly this folder, settled, with nothing in
// the folder wt did not put there; everything else is refused with the
// reason, and a refused plan has no fingerprint to purge by.
func TestPlanPurgeDecidesWhatItDeletes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		// edit changes the quarantine and returns the folder to plan, ""
		// for the quarantine itself.
		edit    func(t *testing.T, dir string, r *Record) string
		problem string // "" for a plan that goes ahead
		state   string // "" when it does not matter
	}{
		{"a finished quarantine", func(*testing.T, string, *Record) string { return "" }, "", StateQuarantined},
		{"its branch step failed", func(t *testing.T, _ string, r *Record) string {
			r.Step(StepBranch).State = Failed
			save(t, r)
			return ""
		}, "", StateQuarantined},
		{"Finder's file and a journal's temp file", func(t *testing.T, dir string, _ *Record) string {
			gittest.WriteFile(t, filepath.Join(dir, ".DS_Store"), "")
			gittest.WriteFile(t, filepath.Join(dir, FileName+".tmp"), "{")
			gittest.WriteFile(t, filepath.Join(dir, PurgingName+".tmp"), "{")
			return ""
		}, "", StateQuarantined},
		{"a project's own recovery.json in the checkout", func(t *testing.T, dir string, _ *Record) string {
			gittest.WriteFile(t, filepath.Join(dir, "checkout", "docs", FileName), `{"what":"the project's own"}`)
			return ""
		}, "", StateQuarantined},
		{"a purge that began", func(t *testing.T, dir string, _ *Record) string {
			rename(t, filepath.Join(dir, FileName), filepath.Join(dir, PurgingName))
			return ""
		}, "", StatePurging},
		{"a purge that began and took the checkout", func(t *testing.T, dir string, _ *Record) string {
			rename(t, filepath.Join(dir, FileName), filepath.Join(dir, PurgingName))
			if err := os.RemoveAll(filepath.Join(dir, "checkout")); err != nil {
				t.Fatal(err)
			}
			return ""
		}, "", StatePurging},
		{"restored", func(t *testing.T, _ string, r *Record) string {
			restored(t, r)
			return ""
		}, "", StateRestored},

		{"restored, then something put in it", func(t *testing.T, dir string, r *Record) string {
			restored(t, r)
			gittest.WriteFile(t, filepath.Join(dir, "notes.txt"), "mine")
			return ""
		}, ProblemForeignEntry, StateRestored},
		{"restored, then a new checkout folder put in it", func(t *testing.T, dir string, r *Record) string {
			restored(t, r)
			mkdir(t, filepath.Join(dir, "checkout"))
			return ""
		}, ProblemWrongFolder, ""},
		{"a file of someone else's in it", func(t *testing.T, dir string, _ *Record) string {
			gittest.WriteFile(t, filepath.Join(dir, "precious.txt"), "precious")
			return ""
		}, ProblemForeignEntry, StateQuarantined},
		{"another quarantine inside it", func(t *testing.T, dir string, _ *Record) string {
			data, err := os.ReadFile(filepath.Join(dir, FileName))
			if err != nil {
				t.Fatal(err)
			}
			gittest.WriteFile(t, filepath.Join(dir, "checkout", "deep", FileName), string(data))
			return ""
		}, ProblemNestedQuarantine, ""},
		{"the removal stopped at moveAdmin", func(t *testing.T, _ string, r *Record) string {
			r.Step(StepMoveAdmin).State = Running
			r.Step(StepRelink).State = Pending
			r.Step(StepBranch).State = Pending
			save(t, r)
			return ""
		}, ProblemUnsettled, StateUnsettled},
		{"the removal stopped at its branch step", func(t *testing.T, _ string, r *Record) string {
			r.Step(StepBranch).State = Running
			save(t, r)
			return ""
		}, ProblemUnsettled, StateUnsettled},
		{"a restore stopped", func(t *testing.T, _ string, r *Record) string {
			r.Restore = &Restore{Steps: pendingSteps(restoreSteps)}
			r.Restore.Steps[0].State = Done
			r.Restore.Steps[1].State = Running
			save(t, r)
			return ""
		}, ProblemUnsettled, StateUnsettled},
		{"its checkout back at its path, the journal saying not", func(t *testing.T, _ string, r *Record) string {
			rename(t, r.Checkout.Quarantined, r.Checkout.Path)
			return ""
		}, ProblemUnsettled, StateUnsettled},
		{"a pin that is not this folder's", func(t *testing.T, _ string, r *Record) string {
			r.Pins.Dir = "refs/heads/main"
			save(t, r)
			return ""
		}, ProblemPins, ""},
		{"a head pin of another quarantine", func(t *testing.T, _ string, r *Record) string {
			other := pinRef(pinKey(r.WorktreeID, "/elsewhere"), "head")
			r.Pins.Head = &other
			save(t, r)
			return ""
		}, ProblemPins, ""},
		{"a record naming another admin dir", func(t *testing.T, dir string, r *Record) string {
			r.Admin.Quarantined = filepath.Join(filepath.Dir(dir), "elsewhere", "admin")
			save(t, r)
			return ""
		}, ProblemWrongFolder, ""},
		{"a record naming its checkout by another name", func(t *testing.T, dir string, r *Record) string {
			r.Checkout.Quarantined = filepath.Join(dir, "work")
			save(t, r)
			return ""
		}, ProblemWrongFolder, ""},
		{"its checkout swapped for another folder", func(t *testing.T, dir string, _ *Record) string {
			// The old one kept elsewhere, so the new one cannot reuse its inode.
			rename(t, filepath.Join(dir, "checkout"), filepath.Join(filepath.Dir(dir), "kept"))
			mkdir(t, filepath.Join(dir, "checkout"))
			return ""
		}, ProblemWrongFolder, ""},
		{"its record copied into another folder", func(t *testing.T, dir string, _ *Record) string {
			data, err := os.ReadFile(filepath.Join(dir, FileName))
			if err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(filepath.Dir(dir), "other")
			gittest.WriteFile(t, filepath.Join(other, "checkout", "precious.txt"), "precious")
			gittest.WriteFile(t, filepath.Join(other, FileName), string(data))
			return other
		}, ProblemWrongFolder, ""},
		{"the folder moved", func(t *testing.T, dir string, _ *Record) string {
			rename(t, dir, dir+"-moved")
			return dir + "-moved"
		}, ProblemWrongFolder, ""},
		{"no record", func(t *testing.T, dir string, _ *Record) string {
			if err := os.Remove(filepath.Join(dir, FileName)); err != nil {
				t.Fatal(err)
			}
			return ""
		}, ProblemNoRecord, ""},
		{"a record that is not JSON", func(t *testing.T, dir string, _ *Record) string {
			gittest.WriteFile(t, filepath.Join(dir, FileName), "{")
			return ""
		}, ProblemNoRecord, ""},
		{"a record of another schema", func(t *testing.T, dir string, _ *Record) string {
			gittest.WriteFile(t, filepath.Join(dir, FileName), `{"schema":2}`)
			return ""
		}, ProblemNoRecord, ""},
		{"a record wt cannot act on", func(t *testing.T, dir string, _ *Record) string {
			gittest.WriteFile(t, filepath.Join(dir, FileName), `{"schema":1,"steps":[]}`)
			return ""
		}, ProblemNoRecord, ""},
		{"a journal that is a symlink to a good record", func(t *testing.T, dir string, _ *Record) string {
			away := filepath.Join(filepath.Dir(dir), "record.json")
			rename(t, filepath.Join(dir, FileName), away)
			if err := os.Symlink(away, filepath.Join(dir, FileName)); err != nil {
				t.Fatal(err)
			}
			return ""
		}, ProblemNoRecord, ""},
		{"an empty folder", func(t *testing.T, dir string, _ *Record) string {
			empty := filepath.Join(filepath.Dir(dir), "empty")
			mkdir(t, empty)
			return empty
		}, ProblemEmpty, StateEmpty},
		{"a file", func(t *testing.T, dir string, _ *Record) string {
			file := filepath.Join(filepath.Dir(dir), "file")
			gittest.WriteFile(t, file, "")
			return file
		}, ProblemNotAFolder, ""},
		{"a symlink to the folder", func(t *testing.T, dir string, _ *Record) string {
			if err := os.Symlink(dir, dir+"-link"); err != nil {
				t.Fatal(err)
			}
			return dir + "-link"
		}, ProblemNotAFolder, ""},
		{"nothing there", func(_ *testing.T, dir string, _ *Record) string { return dir + "-nope" },
			ProblemMissing, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, r := quarantined(t)
			target := c.edit(t, dir, r)
			if target == "" {
				target = dir
			}
			p := PlanPurge(target)
			got := codes(p.Problems)
			switch {
			case c.problem == "" && len(got) > 0:
				t.Errorf("refused: %s", p.Refusal())
			case c.problem != "" && !slices.Contains(got, c.problem):
				t.Errorf("problems %v, want %s among them", got, c.problem)
			}
			if c.state != "" && p.State != c.state {
				t.Errorf("state %q, want %q", p.State, c.state)
			}
			if fp := p.Fingerprint(); (fp == "") != (c.problem != "") {
				t.Errorf("fingerprint %q for a plan with problems %v", fp, got)
			}
		})
	}
}

// With its repository gone the pins went with it: each is listed with no
// commit, so no pin is deleted and nothing is counted lost.
func TestPlanPurgeWithTheRepositoryGoneListsThePinsAsGone(t *testing.T) {
	t.Parallel()
	dir, r := quarantined(t)
	p := PlanPurge(dir)
	if p.Refusal() != "" || !p.RepoGone {
		t.Fatalf("plan %+v", p)
	}
	want := []PurgePin{{Name: "head", Ref: *r.Pins.Head}, {Name: "tip", Ref: *r.Pins.Tip}, {Name: "dir", Ref: r.Pins.Dir}}
	if !slices.Equal(p.Pins, want) {
		t.Errorf("pins %+v, want %+v", p.Pins, want)
	}
	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Entries, []string{"admin", "checkout", FileName}) || len(p.Lost) != 0 || p.Bytes != 2+info.Size() {
		t.Errorf("entries %v lost %v bytes %d", p.Entries, p.Lost, p.Bytes)
	}
}

// The fingerprint is what a purge is held to: the same folder reads the
// same, and a file changed anywhere in it, or a changed record, does not.
func TestPurgeFingerprintFollowsTheFolder(t *testing.T) {
	t.Parallel()
	dir, r := quarantined(t)
	first := PlanPurge(dir).Fingerprint()
	if first == "" || PlanPurge(dir).Fingerprint() != first {
		t.Fatalf("the same folder must read the same fingerprint: %q", first)
	}
	gittest.WriteFile(t, filepath.Join(dir, "checkout", "f"), "changed")
	second := PlanPurge(dir).Fingerprint()
	if second == first {
		t.Error("a file changed in the checkout must change the fingerprint")
	}
	r.Command = "sweep"
	save(t, r)
	if PlanPurge(dir).Fingerprint() == second {
		t.Error("a changed record must change the fingerprint")
	}
}

// A purge deletes the checkout, then the admin dir, then the rest by name,
// then its journal, then the folder; a symlink in it is deleted, never what
// it names.
func TestDoPurgeDeletesTheFolderInItsOrder(t *testing.T) {
	t.Parallel()
	dir, _ := quarantined(t)
	outside := filepath.Join(filepath.Dir(dir), "keep-me")
	gittest.WriteFile(t, filepath.Join(outside, "precious.txt"), "precious")
	if err := os.Symlink(outside, filepath.Join(dir, "checkout", "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "precious.txt"), filepath.Join(dir, "admin", "p")); err != nil {
		t.Fatal(err)
	}
	gittest.WriteFile(t, filepath.Join(dir, FileName+".tmp"), "{")
	gittest.WriteFile(t, filepath.Join(dir, ".DS_Store"), "")
	plan := PlanPurge(dir)
	if why := plan.Refusal(); why != "" {
		t.Fatal(why)
	}
	var points []string
	h := &Hooks{
		AfterSave:  func(_ *Record, at string) error { points = append(points, at); return nil },
		AfterPurge: func(at string) error { points = append(points, at); return nil },
	}
	res, err := DoPurge(plan, h)
	if err != nil || res.Outcome != OutcomePurged {
		t.Fatalf("purge: %+v %v", res, err)
	}
	want := []string{"purge.begin", "entry:checkout", "entry:admin", "entry:.DS_Store", "entry:" + FileName + ".tmp", "record"}
	if !slices.Equal(points, want) {
		t.Errorf("points %v, want %v", points, want)
	}
	if exists(dir) {
		t.Error("the folder must be gone")
	}
	if b, err := os.ReadFile(filepath.Join(outside, "precious.txt")); err != nil || string(b) != "precious" {
		t.Errorf("what a symlink named was touched: %q %v", b, err)
	}
}

// A purge is held to the plan it was given: a folder changed since, or a
// plan that refused, deletes nothing.
func TestDoPurgeDeletesNothingOffThePlan(t *testing.T) {
	t.Parallel()
	t.Run("changed since the plan", func(t *testing.T) {
		t.Parallel()
		dir, _ := quarantined(t)
		plan := PlanPurge(dir)
		gittest.WriteFile(t, filepath.Join(dir, "checkout", "new"), "since")
		if _, err := DoPurge(plan, nil); !errors.Is(err, ErrPurgeChanged) {
			t.Fatalf("err %v, want ErrPurgeChanged", err)
		}
		for _, name := range []string{FileName, "checkout/new", "admin/f"} {
			if !exists(filepath.Join(dir, name)) {
				t.Errorf("%s must stay", name)
			}
		}
		if exists(filepath.Join(dir, PurgingName)) {
			t.Error("no purge may have begun")
		}
	})
	t.Run("a refused plan", func(t *testing.T) {
		t.Parallel()
		dir, _ := quarantined(t)
		gittest.WriteFile(t, filepath.Join(dir, "precious.txt"), "precious")
		plan := PlanPurge(dir)
		if res, err := DoPurge(plan, nil); err == nil || res.Outcome != OutcomeRefused {
			t.Fatalf("want a refusal: %+v %v", res, err)
		}
		for _, name := range []string{FileName, "precious.txt", "checkout/f", "admin/f"} {
			if !exists(filepath.Join(dir, name)) {
				t.Errorf("%s must stay", name)
			}
		}
	})
}

// Something that appears in the folder after the purge began was never
// shown to anyone: it is left, and the purge stays refused over it.
func TestDoPurgeLeavesWhatAppearedAfterItBegan(t *testing.T) {
	t.Parallel()
	dir, _ := quarantined(t)
	late := filepath.Join(dir, "late.txt")
	h := &Hooks{AfterSave: func(_ *Record, at string) error {
		if at == "purge.begin" {
			gittest.WriteFile(t, late, "late")
		}
		return nil
	}}
	_, err := DoPurge(PlanPurge(dir), h)
	if err == nil || !strings.Contains(err.Error(), "appeared") {
		t.Fatalf("err %v, want it to name what appeared", err)
	}
	if !exists(late) || !exists(filepath.Join(dir, PurgingName)) {
		t.Fatal("what appeared, and the purge's journal, must stay")
	}
	p := PlanPurge(dir)
	if p.State != StatePurging || !slices.Contains(codes(p.Problems), ProblemForeignEntry) {
		t.Errorf("plan again: state %s problems %v", p.State, codes(p.Problems))
	}
}

// A purge stopped part-way leaves its journal: no restore takes back a
// worktree whose pins may be gone, and the next purge finishes it.
func TestDoPurgeStoppedIsFinishedByTheNextAndNeverRestored(t *testing.T) {
	t.Parallel()
	dir, _ := quarantined(t)
	crash := errors.New("crash")
	h := &Hooks{AfterPurge: func(at string) error {
		if at == "entry:checkout" {
			return crash
		}
		return nil
	}}
	if _, err := DoPurge(PlanPurge(dir), h); !errors.Is(err, crash) {
		t.Fatalf("err %v, want the crash", err)
	}
	if rp := PlanRestore(dir, nil, nil); !slices.Equal(codes(rp.Problems), []string{ProblemPurging}) || rp.Record != nil {
		t.Errorf("restore plan: problems %v record %v", codes(rp.Problems), rp.Record)
	}
	again := PlanPurge(dir)
	if again.State != StatePurging || again.Refusal() != "" {
		t.Fatalf("plan again: state %s refusal %s", again.State, again.Refusal())
	}
	if res, err := DoPurge(again, nil); err != nil || res.Outcome != OutcomePurged {
		t.Fatalf("purge again: %+v %v", res, err)
	}
	if exists(dir) {
		t.Error("the folder must be gone")
	}
}
