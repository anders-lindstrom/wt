package synccmdtest

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// OverviewJSON is the checked wt sync --json overview, without a fetch.
func OverviewJSON(t *testing.T, ctx *kit.Context) synccmd.SyncOverview {
	t.Helper()
	var buf bytes.Buffer
	if err := synccmd.SyncJSON(ctx, synccmd.SyncOptions{NoFetch: true}, &buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	testkit.ValidateJSON(t, "sync", buf.Bytes())
	var o synccmd.SyncOverview
	if err := json.Unmarshal(buf.Bytes(), &o); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return o
}

// OverviewRow is the overview's row for work.
func OverviewRow(t *testing.T, o synccmd.SyncOverview, work string) synccmd.OverviewWorktree {
	t.Helper()
	for _, w := range o.Worktrees {
		if w.Work == work {
			return w
		}
	}
	t.Fatalf("no row for %s in %+v", work, o.Worktrees)
	return synccmd.OverviewWorktree{}
}
