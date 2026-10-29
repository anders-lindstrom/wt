package synccmd_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// --if-ready runs a named worktree only when nothing would be handed to you:
// a contested one is refused, untouched, and the run fails saying why.
func TestSyncRunIfReadyRefusesANamedWorktreeThatWouldHandOver(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.ContestedFixture(t)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	opts := synccmdtest.NoAgents()
	opts.IfReady = true
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out)
	if err == nil {
		t.Fatalf("want a failure:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "bump would not sync cleanly: it would stop at 1/1 with a conflict that is yours; wt sync bump for the detail") {
		t.Errorf("say why:\n%s", out.String())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("the worktree moved")
	}
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || busy {
		t.Fatalf("a rebase was started: %v %v", busy, err)
	}
}

// Stops the declared strategies resolve are ready: --if-ready runs them.
func TestSyncRunIfReadyRunsAWorktreeTheStrategiesResolve(t *testing.T) {
	t.Parallel()
	ctx, _ := provisiontest.RunFixture(t, false)
	opts := synccmdtest.NoAgents()
	opts.IfReady = true
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "✓ rebased 1 commit") {
		t.Errorf("not rebased:\n%s", out.String())
	}
}

// With nothing named, the ready ones go and the ones left behind trunk fail
// the run; a stale one is a lifecycle question, not a failure.
func TestSyncRunIfReadyWithNothingNamedFailsOnWhatItLeft(t *testing.T) {
	t.Parallel()
	ctx, _ := provisiontest.RunFixture(t, false)
	provisiontest.ContestedSibling(t, ctx)
	opts := synccmdtest.NoAgents()
	opts.IfReady = true
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, nil, opts, &out)
	if err == nil || err.Error() != "not completed: alpha (not ready)" {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "rebased 1 commit") {
		t.Errorf("the ready one still goes:\n%s", out.String())
	}
}

// Across repositories: one question, the ready worktrees rebased repository
// by repository, the rest reported and failing the run with --if-ready.
func TestSyncRunAllRebasesTheReadyOnesAcrossRepositories(t *testing.T) {
	t.Parallel()
	ready, readyBump := provisiontest.RunFixture(t, false)
	contested, contestedBump := provisiontest.ContestedFixture(t)
	oldContested := testkit.GitOut(t, contestedBump, "rev-parse", "HEAD")
	u := &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{ready.Repo.MainRoot, contested.Repo.MainRoot}}}}
	var asked []string
	opts := synccmd.SyncRebaseAllOptions{RunOptions: synccmdtest.NoAgents(),
		Ask: func(q string) bool { asked = append(asked, q); return true }}
	opts.IfReady = true
	var out bytes.Buffer
	err := synccmd.SyncRebaseAll(u, kit.Selection{Profiles: []string{"p"}}, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "demo/bump (not ready)") {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	if !slices.Equal(asked, []string{"Rebase 1 worktree across 1 repository?"}) {
		t.Errorf("asked %q", asked)
	}
	if !testkit.GitAncestor(t, readyBump, "origin/main", "HEAD") {
		t.Errorf("the ready worktree was not rebased:\n%s", out.String())
	}
	if testkit.GitOut(t, contestedBump, "rev-parse", "HEAD") != oldContested {
		t.Fatal("the contested worktree moved")
	}
}

// Declining the question does not hide what was not ready.
func TestSyncRunIfReadyFailsWhenDeclinedWithSomethingNotReady(t *testing.T) {
	t.Parallel()
	ctx, _ := provisiontest.RunFixture(t, false)
	provisiontest.ContestedSibling(t, ctx)
	opts := synccmdtest.NoAgents()
	opts.IfReady = true
	opts.Confirm = func([]string) bool { return false }
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, nil, opts, &out); err == nil || !strings.Contains(err.Error(), "alpha (not ready)") {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
}

// A trunk that moved while the question was open is not the one the plan
// showed: that repository is refused, untouched.
func TestSyncRunAllRefusesARepositoryWhoseTrunkMoved(t *testing.T) {
	t.Parallel()
	ready, bump := provisiontest.RunFixture(t, false)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	main := ready.Repo.MainRoot
	u := &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{main}}}}
	opts := synccmd.SyncRebaseAllOptions{RunOptions: synccmdtest.NoAgents(), Ask: func(string) bool {
		testkit.GitOut(t, main, "commit", "-q", "--allow-empty", "-m", "trunk moved on")
		testkit.GitOut(t, main, "fetch", "-q", "origin")
		return true
	}}
	var out bytes.Buffer
	err := synccmd.SyncRebaseAll(u, kit.Selection{Profiles: []string{"p"}}, opts, &out)
	if err == nil || !strings.Contains(out.String(), "trunk moved since the plan") {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("rebased onto a trunk the plan did not show")
	}
}
