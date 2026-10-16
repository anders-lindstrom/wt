//go:build darwin || linux

package commands

import (
	"errors"
	"path/filepath"
	"testing"
)

// A clone that fails for an ordinary reason, here a missing parent, is an
// error to report, not a filesystem that cannot clone.
func TestCloneTreeOrdinaryErrorIsNotUnsupported(t *testing.T) {
	from := filepath.Join(t.TempDir(), "node_modules")
	mustWrite(t, filepath.Join(from, "a.txt"), "a")
	err := cloneTreeCOW(from, filepath.Join(t.TempDir(), "no", "such", "parent", "node_modules"))
	if err == nil {
		t.Fatal("cloned into a directory that does not exist")
	}
	var unsupported errCloneUnsupported
	if errors.As(err, &unsupported) {
		t.Errorf("an ordinary error counted as unsupported: %v", err)
	}
}
