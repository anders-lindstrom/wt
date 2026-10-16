package refs_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/refs/refstest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// journalPoints is every journal write run makes, in order, given hooks
// that record them.
func journalPoints(run func(h *quarantine.Hooks)) []string {
	var points []string
	run(&quarantine.Hooks{AfterSave: func(_ *quarantine.Record, at string) error {
		points = append(points, at)
		return nil
	}})
	return points
}

// A move whose rename went through and whose fsync then failed has moved
// the checkout: the lock stays, so no prune can take the registration, and
// the run says it is partial.
func TestQuarantineKeepsTheLockWhenAMovedCheckoutFailsToSync(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/unsynced")
	dir := testkit.TrashFor(t, ctx)
	h := &quarantine.Hooks{AfterRename: func(to string) error {
		if to == filepath.Join(dir, "checkout") {
			return errors.New("fsync: input/output error")
		}
		return nil
	}}
	var res retire.RemoveResult
	var buf bytes.Buffer
	if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir, QuarantineHooks: h, Result: &res}, &buf); err == nil {
		t.Fatalf("want an error\n%s", buf.String())
	}
	if res.Outcome != retire.RemovePartial || !res.CheckoutMoved {
		t.Errorf("result %+v", res)
	}
	list, _ := ctx.Repo.Worktrees()
	if wt, ok := list.ByPath(path); !ok || !wt.Locked {
		t.Fatalf("the registration must stay, locked: %+v", wt)
	}
	refstest.Restore(t, ctx, dir)
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/fix_wt/unsynced")
}
