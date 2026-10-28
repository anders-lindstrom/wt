package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// The question is the worktrees' branches and nothing else, which is what
// makes an old pull request findable: a listing by recency would have to
// reach back past every pull request opened since.
func TestListAsksAboutTheWorktreeBranchesByName(t *testing.T) {
	t.Parallel()
	ctx, log := provisiontest.OneWorktreeWithAPR(t)
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "feat/api-tidy", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	out := listing(t, ctx, ListOptions{Refresh: true})
	if !strings.Contains(out, "#12 open") {
		t.Fatalf("no pull request in the listing:\n%s", out)
	}
	calls := testkit.ArgvOf(t, log)
	asked := calls[len(calls)-1]
	if !strings.HasPrefix(asked, "api graphql") {
		t.Fatalf("wt list asked %q", asked)
	}
	for _, want := range []string{"=fix_wt/login-crash ", "=feat_wt/api-tidy "} {
		if !strings.Contains(asked, want) {
			t.Errorf("the question does not name %q: %q", want, asked)
		}
	}
	// The main checkout's branch is not a worktree's work, so it is not asked
	// about.
	if strings.Contains(asked, "=main ") {
		t.Errorf("trunk was asked about: %q", asked)
	}
}

// A branch made since the cache was written must not wait the TTL out. Only
// the branches the cache cannot answer are asked about.
func TestListFetchesOnlyTheBranchesTheCacheCannotAnswer(t *testing.T) {
	t.Parallel()
	ctx, log := provisiontest.OneWorktreeWithAPR(t)
	listing(t, ctx, ListOptions{})
	calls := len(testkit.ArgvOf(t, log))

	var errs bytes.Buffer
	if _, err := provision.New(ctx, "feat/brand-new", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	listing(t, ctx, ListOptions{})

	got := testkit.ArgvOf(t, log)
	if len(got) != calls+1 {
		t.Fatalf("ran %d calls, want one more than %d: %q", len(got), calls, got)
	}
	asked := got[len(got)-1]
	if !strings.Contains(asked, "b0=feat_wt/brand-new") {
		t.Errorf("the new branch was not asked about: %q", asked)
	}
	if strings.Contains(asked, "fix_wt/login-crash") {
		t.Errorf("a branch the cache already answered was asked again: %q", asked)
	}
}

// `wt status` reads the same cache `wt list` fills, so standing in a worktree
// and asking about it costs nothing a listing has already paid for.
func TestStatusReadsTheCacheListFilled(t *testing.T) {
	t.Parallel()
	ctx, log := provisiontest.OneWorktreeWithAPR(t)
	listing(t, ctx, ListOptions{})
	calls := len(testkit.ArgvOf(t, log))

	var out bytes.Buffer
	if err := StatusWorktree(ctx, "login-crash", StatusOptions{Agents: []agents.Agent{}}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pr      #12 open · Login crash") {
		t.Errorf("wt status said\n%s", out.String())
	}
	if got := testkit.ArgvOf(t, log); len(got) != calls {
		t.Errorf("wt status ran gh again: %q", got)
	}
}
