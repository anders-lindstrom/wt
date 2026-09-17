package wtsync

import (
	"errors"
	"os"
	"testing"
)

// An interrupt exits without running deferred cleanups, so every directory
// still open has to go in one call: the one already cleaned up is not
// counted, and the one still open is removed.
func TestRemoveTempDirsTakesEveryDirectoryStillOpen(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	done, cleanup, err := tempDir("wtsync-test-")
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	open, _, err := tempDir("wtsync-test-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(done); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup left %s (%v)", done, err)
	}
	if n := RemoveTempDirs(); n != 1 {
		t.Fatalf("removed %d directories, want the 1 still open", n)
	}
	if _, err := os.Stat(open); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the interrupt left %s (%v)", open, err)
	}
	if entries, _ := os.ReadDir(os.TempDir()); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}
