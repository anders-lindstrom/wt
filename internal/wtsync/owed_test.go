package wtsync

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anlindstrom/wt/internal/gittest"
)

// What a run left owed is kept on disk per branch, read back by the next
// wt sync steps, replaced by each run that finishes, and gone once nothing
// is owed.
func TestOwedStepsAreKeptPerBranchUntilSettled(t *testing.T) {
	t.Parallel()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := gittest.NewRepo(t, parent, "repo")
	if got, err := ReadOwed(dir, "tmp/fold"); err != nil || got != nil {
		t.Fatalf("nothing written: %+v %v", got, err)
	}
	owed := &Owed{Branch: "tmp/fold", Epoch: 7, Steps: []OwedStep{{Step: "./gen.sh", Layer: "f3", Why: "exit 1"}, {Step: "./gen.sh", Layer: "f4", Why: "not run"}}}
	if err := WriteOwed(dir, owed); err != nil {
		t.Fatal(err)
	}
	if err := WriteOwed(dir, &Owed{Branch: "other", Epoch: 7, Steps: []OwedStep{{Step: "./x.sh"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadOwed(dir, "tmp/fold")
	if err != nil || !reflect.DeepEqual(got, owed) {
		t.Fatalf("read %+v %v, want %+v", got, err, owed)
	}
	if !reflect.DeepEqual(got.StepSet(), map[string]bool{"./gen.sh": true}) {
		t.Errorf("step set %v", got.StepSet())
	}
	if err := WriteOwed(dir, &Owed{Branch: "tmp/fold", Epoch: 8}); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadOwed(dir, "tmp/fold"); err != nil || got != nil {
		t.Fatalf("settled, yet read %+v %v", got, err)
	}
	if got, _ := ReadOwed(dir, "other"); got == nil {
		t.Error("another branch's record went with it")
	}
}
