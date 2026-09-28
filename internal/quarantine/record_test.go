package quarantine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func sampleRecord(dir string) Record {
	return Record{Command: "remove", Repo: "/r", CommonDir: "/r/.git", WorktreeID: "x", Dir: dir,
		GitFile:  "gitdir: /r/.git/worktrees/x\n",
		Checkout: Place{Path: "/r_wt/x", Quarantined: filepath.Join(dir, "checkout")},
		Admin:    Place{Path: "/r/.git/worktrees/x", Quarantined: filepath.Join(dir, "admin")}}
}

// The folder is made, never reused, and holds recovery.json before Begin
// returns: the record is what a crash after it is resolved from.
func TestBeginMakesTheFolderAndWritesTheRecordFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "q")
	r, err := Begin(dir, sampleRecord(dir))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != 1 || got.SchemaVersion == "" || got.CreatedAt == "" {
		t.Errorf("header %+v", got)
	}
	var names []string
	for _, s := range got.Steps {
		names = append(names, s.Name+"="+s.State)
	}
	want := []string{"lock=pending", "pin=pending", "moveCheckout=pending", "moveAdmin=pending",
		"relink=pending", "branch=pending"}
	if len(names) != len(want) {
		t.Fatalf("steps %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("steps %v, want %v", names, want)
		}
	}
	if r.Step(StepLock).State != Pending {
		t.Error("the in-memory record should match the file")
	}
	if _, err := os.Stat(filepath.Join(dir, "recovery.json.tmp")); !os.IsNotExist(err) {
		t.Error("the temp file must not be left behind")
	}
}

func TestBeginRefusesAFolderThatExists(t *testing.T) {
	dir := t.TempDir()
	if _, err := Begin(dir, sampleRecord(dir)); err == nil {
		t.Fatal("want a refusal: the folder exists")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Error("nothing may be written into a folder that was there already")
	}
}

// mkdir, not mkdir -p: a missing parent is a caller's mistake, not
// something to create.
func TestBeginDoesNotCreateParents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing", "q")
	if _, err := Begin(dir, sampleRecord(dir)); err == nil {
		t.Fatal("want a refusal: the parent does not exist")
	}
}

// Every change of a step is on disk before the call returns, and the hook a
// test crashes the run with sees it there.
func TestSetWritesEachStepAndCallsTheHook(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "q")
	r, err := Begin(dir, sampleRecord(dir))
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	crash := errors.New("crash")
	t.Cleanup(func() { AfterSave = nil })
	AfterSave = func(r *Record, point string) error {
		seen = append(seen, point)
		onDisk, err := Load(r.Dir)
		if err != nil {
			t.Fatal(err)
		}
		if onDisk.Step(StepLock).State != r.Step(StepLock).State {
			t.Errorf("%s: the file says %s", point, onDisk.Step(StepLock).State)
		}
		return crash
	}
	if err := r.Set(StepLock, Running, nil); !errors.Is(err, crash) {
		t.Fatalf("the hook's error must come back: %v", err)
	}
	if len(seen) != 1 || seen[0] != "lock:running" {
		t.Errorf("hook points %v", seen)
	}
}

func TestLoadRejectsAnotherMajor(t *testing.T) {
	dir := t.TempDir()
	data, _ := json.Marshal(map[string]any{"schema": 2})
	if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("a record of another major is not one this wt can act on")
	}
}

func TestLockReasonNamesTheFolder(t *testing.T) {
	if got := LockReason("/t/q"); got != "wt quarantine /t/q" {
		t.Errorf("reason %q", got)
	}
	if dir, ok := DirOf("wt quarantine /t/q"); !ok || dir != "/t/q" {
		t.Errorf("DirOf = %q %v", dir, ok)
	}
	if _, ok := DirOf("claude session (pid 3)"); ok {
		t.Error("another lock is not a quarantine's")
	}
}

// A record that names no restore steps, or lacks a removal step, is refused
// when it is read, before anything acts on it.
func TestLoadRejectsARecordMissingSteps(t *testing.T) {
	for name, edit := range map[string]func(r *Record){
		"restore with no steps": func(r *Record) { r.Restore = &Restore{} },
		"no removal steps":      func(r *Record) { r.Steps = nil },
		"a duplicated step":     func(r *Record) { r.Steps[1].Name = StepLock },
		"an unknown state":      func(r *Record) { r.Steps[0].State = "halfway" },
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "q")
			r, err := Begin(dir, sampleRecord(dir))
			if err != nil {
				t.Fatal(err)
			}
			edit(r)
			if err := r.Save("test"); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(dir); err == nil {
				t.Error("want the record refused")
			}
		})
	}
}
