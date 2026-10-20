package synccmd_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// Every worktree but the main checkout, filed under the overview's own
// groups, with what a run would do with it and why; and no ref moved.
func TestSyncJSONGroupsEveryWorktreeAndMovesNoRef(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	provisiontest.ContestedSibling(t, ctx)
	refs := func() string {
		return testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "--format=%(refname) %(objectname)")
	}
	before := refs()
	o := synccmdtest.OverviewJSON(t, ctx)
	// The simulation writes loose objects, as wt sync does; never a ref.
	if after := refs(); after != before {
		t.Errorf("the overview moved a ref:\nbefore %s\nafter %s", before, after)
	}
	if o.Schema != 1 || o.Command != "sync" || o.Repo != ctx.Repo.MainRoot || o.Name != ctx.Repo.Name || o.Trunk == nil || *o.Trunk != "main" ||
		o.Onto == nil || !o.Declared || o.Fetched || o.Error != nil || o.Token == nil {
		t.Fatalf("overview = %+v", o)
	}
	if len(o.Worktrees) != 3 {
		t.Fatalf("worktrees = %+v", o.Worktrees)
	}

	b := synccmdtest.OverviewRow(t, o, "bump")
	if b.Group != synccmd.GroupReady || b.Class != "recipe" || !b.Verified || !b.Runnable || b.Verdict != synccmd.VerdictProceed ||
		b.Reason != nil || b.Path != bump || b.Branch == nil || *b.Branch != "feat_wt/bump" || b.Behind != 2 || b.Ahead != 1 {
		t.Errorf("bump = %+v", b)
	}
	if len(b.Stack) != 1 || b.Stack[0].Branch != "feat_wt/bump" {
		t.Errorf("bump stack = %+v", b.Stack)
	}
	if len(b.Stops) != 1 || !b.Stops[0].Resolved || b.Stops[0].Yours || len(b.Stops[0].Files) != 1 ||
		b.Stops[0].Files[0].Path != "v.txt" || b.Stops[0].Files[0].Strategy == nil || *b.Stops[0].Files[0].Strategy != "owned-line" {
		t.Errorf("bump stops = %+v", b.Stops)
	}
	if !slices.Equal(b.Strategies, []string{"owned-line"}) {
		t.Errorf("bump strategies = %v", b.Strategies)
	}

	a := synccmdtest.OverviewRow(t, o, "alpha")
	if a.Group != synccmd.GroupNeedsYou || a.Class != "contested" || a.Runnable || a.Verdict != synccmd.VerdictProceed || a.Reason == nil {
		t.Errorf("alpha = %+v", a)
	}
	if len(a.Stops) != 1 || !a.Stops[0].Yours || a.Stops[0].Resolved || a.Stops[0].Files[0].Path != "a.txt" ||
		a.Stops[0].Files[0].Resolved || a.Stops[0].Files[0].Strategy != nil {
		t.Errorf("alpha stops = %+v", a.Stops)
	}

	other := synccmdtest.OverviewRow(t, o, "other")
	if other.Group != synccmd.GroupSkipped || other.Class != "stale" || other.Verdict != synccmd.VerdictSkip || other.Reason == nil {
		t.Errorf("other = %+v", other)
	}
}

// A worktree already on trunk is listed as current: the human overview
// leaves it out, a tool still sees it.
func TestSyncJSONListsACurrentWorktree(t *testing.T) {
	t.Parallel()
	ctx, _ := provisiontest.RunFixture(t, false)
	o := synccmdtest.OverviewJSON(t, ctx)
	if other := synccmdtest.OverviewRow(t, o, "other"); other.Group != synccmd.GroupCurrent || other.Class != "current" {
		t.Errorf("other = %+v", other)
	}
}

// The stack a worktree belongs to is listed parents first, on every member.
func TestSyncJSONNamesTheStack(t *testing.T) {
	t.Parallel()
	ctx, _ := provisiontest.RunFixture(t, false)
	provisiontest.StackFixture(t, ctx)
	o := synccmdtest.OverviewJSON(t, ctx)
	for _, work := range []string{"bump", "child"} {
		var branches []string
		for _, m := range synccmdtest.OverviewRow(t, o, work).Stack {
			branches = append(branches, m.Branch)
		}
		if !slices.Equal(branches, []string{"feat_wt/bump", "feat_wt/child"}) {
			t.Errorf("%s stack = %v", work, branches)
		}
	}
}

// Nothing a run would start on: no token.
func TestSyncJSONHasNoTokenWhenNothingIsEligible(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("dirt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := synccmdtest.OverviewJSON(t, ctx)
	if o.Token != nil {
		t.Errorf("token = %v with nothing eligible", *o.Token)
	}
	if b := synccmdtest.OverviewRow(t, o, "bump"); b.Verdict != synccmd.VerdictRefuse || !b.Dirty || b.Group != synccmd.GroupNeedsYou || b.Reason == nil {
		t.Errorf("bump = %+v", b)
	}
}

// The token changes with what a run would take, and not otherwise.
func TestSyncJSONTokenFollowsWhatARunWouldTake(t *testing.T) {
	t.Parallel()
	ctx, _ := provisiontest.RunFixture(t, false)
	first := synccmdtest.OverviewJSON(t, ctx).Token
	if again := synccmdtest.OverviewJSON(t, ctx).Token; *again != *first {
		t.Fatalf("the same overview gave %s and %s", *first, *again)
	}
	provisiontest.StackFixture(t, ctx)
	if grown := synccmdtest.OverviewJSON(t, ctx).Token; *grown == *first {
		t.Fatal("a grown stack kept the token")
	}
}
