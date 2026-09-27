package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func overviewJSON(t *testing.T, ctx *Context) SyncOverview {
	t.Helper()
	var buf bytes.Buffer
	if err := SyncJSON(ctx, SyncOptions{NoFetch: true}, &buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	validateJSON(t, "sync", buf.Bytes())
	var o SyncOverview
	if err := json.Unmarshal(buf.Bytes(), &o); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return o
}

func overviewRow(t *testing.T, o SyncOverview, work string) OverviewWorktree {
	t.Helper()
	for _, w := range o.Worktrees {
		if w.Work == work {
			return w
		}
	}
	t.Fatalf("no row for %s in %+v", work, o.Worktrees)
	return OverviewWorktree{}
}

// Every worktree but the main checkout, filed under the overview's own
// groups, with what a run would do with it and why; and no ref moved.
func TestSyncJSONGroupsEveryWorktreeAndMovesNoRef(t *testing.T) {
	ctx, bump := runFixture(t, false)
	contestedSibling(t, ctx)
	refs := func() string {
		return gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "--format=%(refname) %(objectname)")
	}
	before := refs()
	o := overviewJSON(t, ctx)
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

	b := overviewRow(t, o, "bump")
	if b.Group != GroupReady || b.Class != "recipe" || !b.Verified || !b.Runnable || b.Verdict != VerdictProceed ||
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

	a := overviewRow(t, o, "alpha")
	if a.Group != GroupNeedsYou || a.Class != "contested" || a.Runnable || a.Verdict != VerdictProceed || a.Reason == nil {
		t.Errorf("alpha = %+v", a)
	}
	if len(a.Stops) != 1 || !a.Stops[0].Yours || a.Stops[0].Resolved || a.Stops[0].Files[0].Path != "a.txt" ||
		a.Stops[0].Files[0].Resolved || a.Stops[0].Files[0].Strategy != nil {
		t.Errorf("alpha stops = %+v", a.Stops)
	}

	other := overviewRow(t, o, "other")
	if other.Group != GroupSkipped || other.Class != "stale" || other.Verdict != VerdictSkip || other.Reason == nil {
		t.Errorf("other = %+v", other)
	}
}

// A worktree already on trunk is listed as current: the human overview
// leaves it out, a tool still sees it.
func TestSyncJSONListsACurrentWorktree(t *testing.T) {
	ctx, _ := runFixture(t, false)
	o := overviewJSON(t, ctx)
	if other := overviewRow(t, o, "other"); other.Group != GroupCurrent || other.Class != "current" {
		t.Errorf("other = %+v", other)
	}
}

// The stack a worktree belongs to is listed parents first, on every member.
func TestSyncJSONNamesTheStack(t *testing.T) {
	ctx, _ := runFixture(t, false)
	stackFixture(t, ctx)
	o := overviewJSON(t, ctx)
	for _, work := range []string{"bump", "child"} {
		var branches []string
		for _, m := range overviewRow(t, o, work).Stack {
			branches = append(branches, m.Branch)
		}
		if !slices.Equal(branches, []string{"feat_wt/bump", "feat_wt/child"}) {
			t.Errorf("%s stack = %v", work, branches)
		}
	}
}

// Nothing a run would start on: no token.
func TestSyncJSONHasNoTokenWhenNothingIsEligible(t *testing.T) {
	ctx, bump := runFixture(t, false)
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("dirt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := overviewJSON(t, ctx)
	if o.Token != nil {
		t.Errorf("token = %v with nothing eligible", *o.Token)
	}
	if b := overviewRow(t, o, "bump"); b.Verdict != VerdictRefuse || !b.Dirty || b.Group != GroupNeedsYou || b.Reason == nil {
		t.Errorf("bump = %+v", b)
	}
}

// The token changes with what a run would take, and not otherwise.
func TestSyncJSONTokenFollowsWhatARunWouldTake(t *testing.T) {
	ctx, _ := runFixture(t, false)
	first := overviewJSON(t, ctx).Token
	if again := overviewJSON(t, ctx).Token; *again != *first {
		t.Fatalf("the same overview gave %s and %s", *first, *again)
	}
	stackFixture(t, ctx)
	if grown := overviewJSON(t, ctx).Token; *grown == *first {
		t.Fatal("a grown stack kept the token")
	}
}
