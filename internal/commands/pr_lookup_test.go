package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The question is the worktrees' branches and nothing else, which is what
// makes an old pull request findable: a listing by recency would have to
// reach back past every pull request opened since.
func TestListAsksAboutTheWorktreeBranchesByName(t *testing.T) {
	ctx, log := oneWorktreeWithAPR(t)
	var errs bytes.Buffer
	if _, err := New(ctx, "feat/api-tidy", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	out := listing(t, ctx, ListOptions{Refresh: true})
	if !strings.Contains(out, "#12 open") {
		t.Fatalf("no pull request in the listing:\n%s", out)
	}
	calls := argvOf(t, log)
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
	ctx, log := oneWorktreeWithAPR(t)
	listing(t, ctx, ListOptions{})
	calls := len(argvOf(t, log))

	var errs bytes.Buffer
	if _, err := New(ctx, "feat/brand-new", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	listing(t, ctx, ListOptions{})

	got := argvOf(t, log)
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
	ctx, log := oneWorktreeWithAPR(t)
	listing(t, ctx, ListOptions{})
	calls := len(argvOf(t, log))

	var out bytes.Buffer
	if err := StatusWorktree(ctx, "login-crash", StatusOptions{Agents: []wtsync.Agent{}}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pr      #12 open · Login crash") {
		t.Errorf("wt status said\n%s", out.String())
	}
	if got := argvOf(t, log); len(got) != calls {
		t.Errorf("wt status ran gh again: %q", got)
	}
}

// A pull request whose head branch is trunk belongs to the main checkout, and
// git gives a branch one worktree. Answering with the main checkout's path
// would walk `cd "$(wt pr checkout 12)"` into the repository you started in.
func TestPRCheckoutRefusesAPullRequestOnTrunk(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	fakeGitHub(t, openPR(12, "main", "Straight onto trunk"))

	var errs bytes.Buffer
	path, err := PRCheckout(ctx, 12, PROptions{}, &errs)
	if err == nil {
		t.Fatalf("PRCheckout answered %q", path)
	}
	if !strings.Contains(err.Error(), "which the main checkout at") {
		t.Errorf("err = %v", err)
	}
	if path != "" {
		t.Errorf("path = %q, want nothing on stdout", path)
	}
}

// gh can create the branch and then fail — fetching it, or configuring its
// remote. The worktree goes; so must the branch, or the next attempt finds a
// branch it did not make and refuses.
func TestPRCheckoutRemovesABranchGhLeftBehind(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	fakeGitHubWith(t, `[ "$1 $2" = 'pr checkout' ] && { git checkout -q -b residential_fixes; `+
		`echo 'could not set up the remote' >&2; exit 1; }`,
		openPR(12, "residential_fixes", "Fixes"))

	var errs bytes.Buffer
	if _, err := PRCheckout(ctx, 12, PROptions{}, &errs); err == nil {
		t.Fatal("PRCheckout succeeded with a gh that failed")
	}
	if _, ok := ctx.Repo.ResolveRef("refs/heads/residential_fixes"); ok {
		t.Error("the branch gh half-made was left behind")
	}
}

// A branch that was already here is not gh's to have made, so a failure must
// not take it.
func TestPRCheckoutKeepsABranchItDidNotMake(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, ctx.Repo.MainRoot, "branch", "residential_fixes")
	fakeGitHubWith(t, `[ "$1 $2" = 'pr checkout' ] && { echo 'fetch failed' >&2; exit 1; }`,
		openPR(12, "residential_fixes", "Fixes"))

	var errs bytes.Buffer
	if _, err := PRCheckout(ctx, 12, PROptions{}, &errs); err == nil {
		t.Fatal("PRCheckout succeeded with a gh that failed")
	}
	if _, ok := ctx.Repo.ResolveRef("refs/heads/residential_fixes"); !ok {
		t.Error("a branch that was here before was deleted")
	}
}

// The picker leads with what is waiting on you, and says which rows already
// have a worktree here.
func TestPRChoicesLeadWithYourReviewQueue(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	mine := openPR(12, "fix_wt/login-crash", "Login crash")
	wanted := openPR(41, "somebody/fix", "Please look at this")
	wanted.ReviewRequests.Nodes = append(wanted.ReviewRequests.Nodes, struct {
		RequestedReviewer github.Account `json:"requestedReviewer"`
	}{RequestedReviewer: github.Account{Login: "anders"}})
	fakeGitHubAs(t, "anders", "", mine, wanted)

	var errs bytes.Buffer
	path, err := New(ctx, "fix/login-crash", NewOptions{}, &errs)
	if err != nil {
		t.Fatal(err)
	}

	var offered []PRChoice
	opts := PROptions{Choose: func(rows []PRChoice) (github.PR, error) {
		offered = rows
		return rows[0].PR, nil
	}}
	if _, err := PRCheckout(ctx, 0, opts, &errs); err != nil {
		t.Fatalf("PRCheckout: %v", err)
	}
	if len(offered) != 2 {
		t.Fatalf("offered %+v", offered)
	}
	if offered[0].PR.Number != 41 || !offered[0].ReviewRequested {
		t.Errorf("the review queue is not first: %+v", offered)
	}
	if offered[1].Worktree != path {
		t.Errorf("the row with a worktree does not name it: %+v", offered[1])
	}
}
