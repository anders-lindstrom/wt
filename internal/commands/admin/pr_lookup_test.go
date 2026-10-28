package admin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// A pull request whose head branch is trunk belongs to the main checkout, and
// git gives a branch one worktree. Answering with the main checkout's path
// would walk `cd "$(wt pr checkout 12)"` into the repository you started in.
func TestPRCheckoutRefusesAPullRequestOnTrunk(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t, ctx, testkit.OpenPR(12, "main", "Straight onto trunk"))

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
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHubWith(t, ctx, `[ "$1 $2" = 'pr checkout' ] && { git checkout -q -b residential_fixes; `+
		`echo 'could not set up the remote' >&2; exit 1; }`,
		testkit.OpenPR(12, "residential_fixes", "Fixes"))

	var errs bytes.Buffer
	if _, err := PRCheckout(ctx, 12, PROptions{}, &errs); err == nil {
		t.Fatal("PRCheckout succeeded with a gh that failed")
	}
	if _, ok, err := ctx.Repo.ResolveRef("refs/heads/residential_fixes"); err != nil || ok {
		t.Error("the branch gh half-made was left behind")
	}
}

// A branch that was already here is not gh's to have made, so a failure must
// not take it.
func TestPRCheckoutKeepsABranchItDidNotMake(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "residential_fixes")
	testkit.FakeGitHubWith(t, ctx, `[ "$1 $2" = 'pr checkout' ] && { echo 'fetch failed' >&2; exit 1; }`,
		testkit.OpenPR(12, "residential_fixes", "Fixes"))

	var errs bytes.Buffer
	if _, err := PRCheckout(ctx, 12, PROptions{}, &errs); err == nil {
		t.Fatal("PRCheckout succeeded with a gh that failed")
	}
	if _, ok, err := ctx.Repo.ResolveRef("refs/heads/residential_fixes"); err != nil || !ok {
		t.Error("a branch that was here before was deleted")
	}
}

// The picker leads with what is waiting on you, and says which rows already
// have a worktree here.
func TestPRChoicesLeadWithYourReviewQueue(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	mine := testkit.OpenPR(12, "fix_wt/login-crash", "Login crash")
	wanted := testkit.OpenPR(41, "somebody/fix", "Please look at this")
	wanted.ReviewRequests.Nodes = append(wanted.ReviewRequests.Nodes, struct {
		RequestedReviewer github.Account `json:"requestedReviewer"`
	}{RequestedReviewer: github.Account{Login: "anders"}})
	testkit.FakeGitHubAs(t, ctx, "anders", "", mine, wanted)

	var errs bytes.Buffer
	path, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs)
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
