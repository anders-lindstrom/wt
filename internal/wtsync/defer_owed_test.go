package wtsync

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// A step an earlier failure kept from running is owed only when it was due:
// one whose paths the rebase did not touch is skipped as before, not owed.
func TestAStepThatWasNotDueIsNotOwedAfterAFailure(t *testing.T) {
	t.Parallel()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := gittest.NewRepo(t, parent, "repo")
	head := gitIn(t, dir, "rev-parse", "HEAD")
	steps := []Deferred{{Run: "exit 3"}, {Run: "true", Paths: []string{"package.json"}}, {Run: "echo due"}}
	ran, err := RunDeferred(dir, steps, head, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b := ran[1]; b.Blocked != "" || b.Why != "no listed path changed" {
		t.Errorf("the step not due: %+v, want skipped and not owed", b)
	}
	if c := ran[2]; c.Blocked != "exit 3" {
		t.Errorf("the step due: %+v, want owed behind exit 3", c)
	}
}
