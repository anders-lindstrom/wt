package syncrun

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A deferred step not run because an earlier one failed is owed as much as
// the one that failed: both are recorded for wt sync steps, the second with
// why it did not run.
func TestRecordOwedKeepsTheStepsAFailureLeftUnrun(t *testing.T) {
	t.Parallel()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := gittest.NewRepo(t, parent, "repo")
	head := gittest.Git(t, dir, "rev-parse", "HEAD")
	steps := []wtsync.Deferred{{Run: "exit 3"}, {Run: "true"}}
	ran, err := wtsync.RunDeferred(dir, steps, head, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordOwed(dir, "b", 1, wtsync.Result{}, ran); err != nil {
		t.Fatal(err)
	}
	owed, err := wtsync.ReadOwed(dir, "b")
	if err != nil || owed == nil || len(owed.Steps) != 2 {
		t.Fatalf("owed %+v %v, want both steps", owed, err)
	}
	if s := owed.Steps[1]; s.Step != "true" || s.Why != "not run: an earlier step failed: exit 3" {
		t.Errorf("the second step is owed as %+v", s)
	}
}
