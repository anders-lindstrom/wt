package retire

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// A run begun for a branch that then is not moved leaves nothing behind:
// its meta is dropped, and finish names no run.
func TestBranchPinsDropARunThatPinnedNothing(t *testing.T) {
	ctx, path := provisiontest.SafetyWorktree(t, "fix/busy")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	var begun string
	pins := &refpins.BranchPins{Command: "wt test", Begun: func(id string) { begun = id }}
	var inUse *repo.BranchInUseError
	if _, err := pins.Move(ctx, "fix_wt/busy", tip); !errors.As(err, &inUse) {
		t.Fatalf("a branch a worktree uses is refused: %v", err)
	}
	if begun == "" || !ctx.Repo.BranchExists("fix_wt/busy") {
		t.Fatalf("the run is begun before the move, and the branch stays: %q", begun)
	}
	if run := pins.Finish(ctx, &bytes.Buffer{}); run != "" {
		t.Errorf("finish named %s", run)
	}
	if left := testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", refpins.SweptPrefix); left != "" {
		t.Errorf("nothing may be left under %s: %s", refpins.SweptPrefix, left)
	}
}

// A run id another run takes between the check and the meta write refuses
// the move, and the other run's meta is left as it was.
func TestBranchPinsLeaveARunTheyDidNotBegin(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 1)
	tip := testkit.GitOut(t, main, "rev-parse", "done-work")
	var told []string
	pins := &refpins.BranchPins{Command: "wt test", Begun: func(id string) {
		told = append(told, id)
		if id != "" {
			// Another run writes its meta at this id first.
			if err := refpins.BeginRun(main, id, time.Now(), nil); err != nil {
				t.Fatal(err)
			}
		}
	}}
	if _, err := pins.Move(ctx, "done-work", tip); err == nil {
		t.Fatal("the move must refuse a run id another run holds")
	}
	if len(told) != 2 || told[1] != "" || !ctx.Repo.BranchExists("done-work") {
		t.Fatalf("told %q", told)
	}
	if run := pins.Finish(ctx, &bytes.Buffer{}); run != "" {
		t.Errorf("finish named %s", run)
	}
	if meta := testkit.GitOut(t, main, "for-each-ref", "--format=%(refname)", refpins.SweptPrefix); meta != refpins.MetaRef(told[0]) {
		t.Errorf("the other run's meta must stay: %q", meta)
	}
}
