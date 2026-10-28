package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// The overview a person reads: one line under the row for what a run would
// refuse or do first, and the detail of one worktree says how it stands.
func TestSyncSaysHowAWorktreeStandsAgainstItsRemote(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	f.Declare(t)
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	x, _ := f.PushedWork(t)
	f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.AdvanceOrigin(t, 1)
	var out bytes.Buffer
	if err := synccmd.Sync(f.Ctx, synccmd.SyncOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\n    1 behind origin/feat_wt/x; a run fast-forwards it first\n") {
		t.Fatalf("behind:\n%s", out.String())
	}
	testkit.Mine(t, x, "m.txt", "m\n")
	out.Reset()
	if err := synccmd.Sync(f.Ctx, synccmd.SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "needs you") || !strings.Contains(s, "diverged from origin/feat_wt/x\n") ||
		!strings.Contains(s, "\n    origin/feat_wt/x has 1 commit this branch never had: pull it in, or --allow-diverged rebases it as it stands\n") {
		t.Fatalf("diverged:\n%s", s)
	}
	out.Reset()
	if err := synccmd.SyncWorktree(f.Ctx, "x", synccmd.SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "  remote  diverged from origin/feat_wt/x: 1 of its own, and 1 there it never had (as last fetched)\n") ||
		!strings.Contains(out.String(), "  run     refused: origin/feat_wt/x has 1 commit this branch never had") {
		t.Fatalf("detail:\n%s", out.String())
	}
	out.Reset()
	if err := StatusWorktree(f.Ctx, "x", StatusOptions{NoPR: true, Agents: []agents.Agent{}}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "  remote") || !strings.Contains(out.String(), "diverged from origin/feat_wt/x") {
		t.Fatalf("status:\n%s", out.String())
	}
}
