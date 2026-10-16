package commands

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// The keeper fast-forwards local trunk after its fetch, even on a pass with
// nothing to rebase.
func TestSyncKeepRunFastForwardsLocalTrunk(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	f := synccmdtest.NewTrunkFixture(t)
	f.Declare(t)
	f.AdvanceOrigin(t, 1)
	// Moved again after the last fetch: only the pass's own fetch sees it.
	testkit.GitIn(t, f.Other, "commit", "-q", "--allow-empty", "-m", "again")
	testkit.GitIn(t, f.Other, "push", "-q", "origin", "main")
	remote := testkit.GitOut(t, f.Other, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := SyncKeepRun(f.Ctx, keepOpts(synccmd.PushNever), &out); err != nil {
		t.Fatalf("keep: %v\n%s", err, out.String())
	}
	if got := f.Local(t); got != remote {
		t.Fatalf("local main at %s, want %s\n%s", got, remote, out.String())
	}
	if !strings.Contains(out.String(), "fast-forwarded to origin/main") {
		t.Fatalf("the pass does not say so:\n%s", out.String())
	}
	log, _ := synccmdtest.KeepFiles(t, f.Ctx)
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "fast-forwarded to origin/main") {
		t.Fatalf("the log does not say so:\n%s", b)
	}
}

func TestSyncKeepRunNoFFTrunkLeavesLocalTrunk(t *testing.T) {
	f := synccmdtest.NewTrunkFixture(t)
	before := f.Local(t)
	f.AdvanceOrigin(t, 1)
	opts := keepOpts(synccmd.PushNever)
	opts.NoFFTrunk = true
	var out bytes.Buffer
	_ = SyncKeepRun(f.Ctx, opts, &out)
	if got := f.Local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
}
