package timemachine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anlindstrom/wt/internal/gittest"
)

// fakeTmutil is a tmutil that keeps its exclusions in a file beside itself,
// so the real one is never run. fail makes addexclusion fail the way tmutil
// does, with its complaint on stderr.
func fakeTmutil(t *testing.T, fail bool) (Tmutil, string) {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "excluded")
	add := "echo \"$2\" >> " + state
	if fail {
		add = "echo 'Error (100002): Error setting exclusion.' >&2; exit 1"
	}
	gittest.WriteExecutable(t, filepath.Join(dir, "tmutil"), "#!/bin/sh\n"+
		"case \"$1\" in\n"+
		"  isexcluded) if grep -qxF \"$2\" "+state+" 2>/dev/null; then echo \"[Excluded]    $2\"; else echo \"[Included]    $2\"; fi ;;\n"+
		"  addexclusion) "+add+" ;;\n"+
		"  *) exit 2 ;;\n"+
		"esac\n")
	return Tmutil{Exe: filepath.Join(dir, "tmutil")}, state
}

func TestExcludeThenExcluded(t *testing.T) {
	tm, _ := fakeTmutil(t, false)
	dir := t.TempDir()
	if excluded, err := tm.Excluded(dir); err != nil || excluded {
		t.Fatalf("Excluded before = %v, %v", excluded, err)
	}
	if err := tm.Exclude(dir); err != nil {
		t.Fatalf("Exclude: %v", err)
	}
	if excluded, err := tm.Excluded(dir); err != nil || !excluded {
		t.Fatalf("Excluded after = %v, %v", excluded, err)
	}
}

// A tmutil that fails says why in tmutil's own words.
func TestExcludeFailureCarriesTmutilsLine(t *testing.T) {
	tm, _ := fakeTmutil(t, true)
	err := tm.Exclude(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "Error setting exclusion") {
		t.Fatalf("Exclude = %v, want tmutil's complaint", err)
	}
}

// An answer that is neither Excluded nor Included is an error, not a guess.
func TestExcludedRefusesAnUnknownAnswer(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tmutil")
	gittest.WriteExecutable(t, exe, "#!/bin/sh\necho 'something else'\n")
	if _, err := (Tmutil{Exe: exe}).Excluded(dir); err == nil {
		t.Fatal("an unreadable answer was read as one")
	}
}

// A tmutil that hangs is given up on.
func TestExcludeGivesUpOnAHang(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tmutil")
	gittest.WriteExecutable(t, exe, "#!/bin/sh\nsleep 30\n")
	old := Deadline
	Deadline = 200 * time.Millisecond
	t.Cleanup(func() { Deadline = old })
	start := time.Now()
	err := (Tmutil{Exe: exe}).Exclude(dir)
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("Exclude = %v after %s", err, time.Since(start))
	}
}

// Find looks on the PATH and nowhere else.
func TestFindReadsThePath(t *testing.T) {
	tm, _ := fakeTmutil(t, false)
	t.Setenv("PATH", filepath.Dir(tm.Exe))
	got, ok := Find()
	if !ok || got.Exe != tm.Exe {
		t.Errorf("Find = %+v, %v; want %s", got, ok, tm.Exe)
	}
	t.Setenv("PATH", t.TempDir())
	if _, ok := Find(); ok {
		t.Error("found a tmutil on a PATH that has none")
	}
}
