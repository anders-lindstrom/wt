package refs_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/refs/refstest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// Each worktree a sweep removes goes into its own folder in the quarantine,
// with its own recovery.json, and wt restore puts any one of them back.
func TestSweepQuarantinesEachWorktreeInItsOwnFolder(t *testing.T) {
	t.Parallel()
	ctx, _, _ := testkit.SweepRepo(t)
	one := provisiontest.MergedWorktree(t, ctx, "fix/one")
	two := provisiontest.MergedWorktree(t, ctx, "feat/one")
	dir := testkit.TrashFor(t, ctx)
	opts := retire.SweepOptions{NoFetch: true, Agents: []agents.Agent{}, PRs: map[string]github.PR{}, Quarantine: dir}
	r, human, err := retiretest.SweepJSON(t, ctx, opts)
	if err != nil || r.Outcome != kit.OutcomeDone {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	if r.Quarantine == nil || *r.Quarantine != dir {
		t.Errorf("quarantine %v", r.Quarantine)
	}
	subs := map[string]bool{}
	for _, it := range r.Items {
		if it.Category != retire.SweepRemove {
			continue
		}
		q := it.Quarantine
		if q == nil || !q.CheckoutMoved || !q.AdminMoved || filepath.Dir(q.Dir) != dir || it.Result != retire.SweepRemoved {
			t.Fatalf("item %+v quarantine %+v", it, q)
		}
		subs[q.Dir] = true
		if rec := testkit.LoadRecord(t, q.Dir); rec.Command != "sweep" || !strings.Contains(rec.Checkout.Path, "one") {
			t.Errorf("record %+v", rec)
		}
	}
	if len(subs) != 2 {
		t.Fatalf("want a folder each, got %v", subs)
	}
	if testkit.Exists(one) || testkit.Exists(two) {
		t.Error("both worktrees should be gone from their places")
	}
	for sub := range subs {
		refstest.Restore(t, ctx, sub)
	}
	if !testkit.Exists(one) || !testkit.Exists(two) {
		t.Error("wt restore puts each back")
	}
}
