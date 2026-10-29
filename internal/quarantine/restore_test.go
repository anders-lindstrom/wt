package quarantine

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Locate decides where a restore takes each directory from, and it goes by
// inode alone: whatever else stands at either path is not the directory the
// removal moved.
func TestLocateFindsTheDirectoryByItsInode(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		edit func(t *testing.T, pl Place)
		want string
	}{
		{"in quarantine", func(*testing.T, Place) {}, InQuarantine},
		{"moved back", func(t *testing.T, pl Place) { rename(t, pl.Quarantined, pl.Path) }, AtOriginal},
		{"moved back, something new at the quarantine path", func(t *testing.T, pl Place) {
			rename(t, pl.Quarantined, pl.Path)
			mkdir(t, pl.Quarantined)
		}, AtOriginal},
		{"in quarantine, another folder at its path", func(t *testing.T, pl Place) { mkdir(t, pl.Path) }, Taken},
		{"in quarantine, a file at its path", func(t *testing.T, pl Place) { gittest.WriteFile(t, pl.Path, "") }, Taken},
		{"in quarantine, a symlink to it at its path", func(t *testing.T, pl Place) {
			mkdir(t, filepath.Dir(pl.Path))
			if err := os.Symlink(pl.Quarantined, pl.Path); err != nil {
				t.Fatal(err)
			}
		}, Taken},
		{"gone from both", func(t *testing.T, pl Place) {
			if err := os.RemoveAll(pl.Quarantined); err != nil {
				t.Fatal(err)
			}
		}, Missing},
		{"another folder in its place in the quarantine", func(t *testing.T, pl Place) {
			rename(t, pl.Quarantined, pl.Quarantined+"-kept")
			mkdir(t, pl.Quarantined)
		}, Missing},
		{"a symlink to it in its place in the quarantine", func(t *testing.T, pl Place) {
			rename(t, pl.Quarantined, pl.Quarantined+"-kept")
			if err := os.Symlink(pl.Quarantined+"-kept", pl.Quarantined); err != nil {
				t.Fatal(err)
			}
		}, Missing},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, r := quarantined(t)
			c.edit(t, r.Checkout)
			if got := Locate(r.Checkout); got != c.want {
				t.Errorf("Locate = %s, want %s", got, c.want)
			}
		})
	}
}

// PlanRestore refuses a directory that cannot go back where it came from:
// its path taken, the directory nowhere, or a move that would cross
// volumes. A parent of where it came from that is gone is not one of them:
// the restore makes it again. The repository in these records was never
// made, so every plan also says it cannot be opened, last.
func TestPlanRestoreRefusesADirectoryThatCannotGoBack(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name            string
		edit            func(t *testing.T, dir string, r *Record) *Hooks
		checkout, admin string
		problems        []string
	}{
		{"both in quarantine, their parents gone", func(*testing.T, string, *Record) *Hooks { return nil },
			InQuarantine, InQuarantine, nil},
		{"the checkout's path taken", func(t *testing.T, _ string, r *Record) *Hooks {
			mkdir(t, r.Checkout.Path)
			return nil
		}, Taken, InQuarantine, []string{ProblemCheckoutTaken}},
		{"the admin dir gone", func(t *testing.T, _ string, r *Record) *Hooks {
			if err := os.RemoveAll(r.Admin.Quarantined); err != nil {
				t.Fatal(err)
			}
			return nil
		}, InQuarantine, Missing, []string{ProblemAdminMissing}},
		{"the checkout back already", func(t *testing.T, _ string, r *Record) *Hooks {
			rename(t, r.Checkout.Quarantined, r.Checkout.Path)
			return nil
		}, AtOriginal, InQuarantine, nil},
		{"the checkout's way back on another volume", func(_ *testing.T, dir string, _ *Record) *Hooks {
			return &Hooks{DeviceOf: func(p string) (uint64, error) {
				if strings.HasPrefix(p, dir) {
					return 1, nil
				}
				return 2, nil
			}}
		}, InQuarantine, InQuarantine, []string{ProblemVolume, ProblemVolume}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, r := quarantined(t)
			h := c.edit(t, dir, r)
			p := PlanRestore(dir, nil, h)
			if p.Record == nil || p.Checkout != c.checkout || p.Admin != c.admin {
				t.Fatalf("checkout %s admin %s record %v, want %s %s", p.Checkout, p.Admin, p.Record, c.checkout, c.admin)
			}
			if want := append(slices.Clone(c.problems), ProblemRepository); !slices.Equal(codes(p.Problems), want) {
				t.Errorf("problems %v, want %v: %s", codes(p.Problems), want, p.Refusal())
			}
		})
	}
}

// A folder whose record cannot be read is nothing to restore from, and with
// no repository to find an orphaned lock in, nothing is unlocked either.
func TestPlanRestoreWithNoRecordRestoresNothing(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string]func(t *testing.T, dir string){
		"no record": func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, FileName)); err != nil {
				t.Fatal(err)
			}
		},
		"a record that is not JSON": func(t *testing.T, dir string) { gittest.WriteFile(t, filepath.Join(dir, FileName), "{") },
		"a record wt cannot act on": func(t *testing.T, dir string) {
			gittest.WriteFile(t, filepath.Join(dir, FileName), `{"schema":1,"steps":[]}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir, _ := quarantined(t)
			edit(t, dir)
			p := PlanRestore(dir, nil, nil)
			if !slices.Equal(codes(p.Problems), []string{ProblemNoRecord}) || p.Record != nil || p.Orphan != "" ||
				p.Locked || p.Action != "" {
				t.Errorf("plan %+v", p)
			}
		})
	}
}

// The restore takes off only its own quarantine's lock, read from the admin
// dir wherever it is now.
func TestPlanRestoreTakesOffOnlyItsOwnLock(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		reason func(dir string) string
		back   bool
		want   bool
	}{
		{"this quarantine's", LockReason, false, true},
		{"this quarantine's, the admin dir back", LockReason, true, true},
		{"another quarantine's", func(dir string) string { return LockReason(dir + "-other") }, false, false},
		{"somebody else's", func(string) string { return "claude session (pid 3)" }, false, false},
		{"none", nil, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, r := quarantined(t)
			if c.reason != nil {
				gittest.WriteFile(t, filepath.Join(r.Admin.Quarantined, "locked"), c.reason(dir)+"\n")
			}
			if c.back {
				// Back outside the repository's path, which stays unmade, so
				// the plan still runs no git.
				r.Admin.Path = filepath.Join(filepath.Dir(dir), "admin-back", "x")
				save(t, r)
				rename(t, r.Admin.Quarantined, r.Admin.Path)
			}
			if got := PlanRestore(dir, nil, nil).Locked; got != c.want {
				t.Errorf("Locked = %v, want %v", got, c.want)
			}
		})
	}
}

// configAgainst decides which of a deleted branch's recorded config entries
// a restore adds back: each one counted as often as it was recorded, and a
// key holding a value the record does not have was set since, so it is left
// alone.
func TestConfigAgainst(t *testing.T) {
	t.Parallel()
	type entry = ConfigEntry
	remote, merge := entry{"remote", "origin"}, entry{"merge", "refs/heads/a"}
	mergeB := entry{"merge", "refs/heads/b"}
	for _, c := range []struct {
		name     string
		recorded []ConfigEntry
		now      []ConfigEntry
		have     map[ConfigEntry]int
		newer    map[string]bool
	}{
		{"nothing there", []ConfigEntry{remote, merge}, nil, map[ConfigEntry]int{}, map[string]bool{}},
		{"some there", []ConfigEntry{remote, merge}, []ConfigEntry{remote},
			map[ConfigEntry]int{remote: 1}, map[string]bool{}},
		{"all there", []ConfigEntry{remote, merge}, []ConfigEntry{merge, remote},
			map[ConfigEntry]int{remote: 1, merge: 1}, map[string]bool{}},
		{"a value set since", []ConfigEntry{remote, merge}, []ConfigEntry{mergeB},
			map[ConfigEntry]int{}, map[string]bool{"merge": true}},
		{"a multi-valued key in part", []ConfigEntry{merge, mergeB}, []ConfigEntry{mergeB},
			map[ConfigEntry]int{mergeB: 1}, map[string]bool{}},
		{"a value recorded twice, there once", []ConfigEntry{merge, merge}, []ConfigEntry{merge},
			map[ConfigEntry]int{merge: 1}, map[string]bool{}},
		{"a key the record never had", []ConfigEntry{remote}, []ConfigEntry{remote, {"rebase", "true"}},
			map[ConfigEntry]int{remote: 1}, map[string]bool{"rebase": true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var now []repo.ConfigEntry
			for _, e := range c.now {
				now = append(now, repo.ConfigEntry{Key: e.Key, Value: e.Value})
			}
			have, newer := configAgainst(c.recorded, now)
			if !maps.Equal(have, c.have) || !maps.Equal(newer, c.newer) {
				t.Errorf("have %v newer %v, want %v %v", have, newer, c.have, c.newer)
			}
		})
	}
}

func TestExistingAncestor(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "a"))
	for path, want := range map[string]string{
		filepath.Join(root, "a"):           filepath.Join(root, "a"),
		filepath.Join(root, "a", "b", "c"): filepath.Join(root, "a"),
		filepath.Join(root, "x", "y"):      root,
	} {
		if got := existingAncestor(path); got != want {
			t.Errorf("existingAncestor(%s) = %s, want %s", path, got, want)
		}
	}
}
