package provision_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// Several remotes having the name is refused, naming them, with nothing
// made — unless checkout.defaultRemote picks one, as it does for git.
func TestCheckoutBareNameOnSeveralRemotes(t *testing.T) {
	t.Parallel()
	ctx := provision.RemoteFixture(t, []string{"origin", "upstream"}, "origin/topic", "upstream/topic")
	before := testkit.RepoState(t, ctx.Repo.MainRoot)
	_, err := provision.Checkout(ctx, "topic", "", provision.NewOptions{NoSetup: true}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "origin/topic") || !strings.Contains(err.Error(), "upstream/topic") {
		t.Fatalf("want a refusal naming both, got %v", err)
	}
	if after := testkit.RepoState(t, ctx.Repo.MainRoot); after != before {
		t.Error("a refused checkout wrote to the repository")
	}
	p := provisiontest.CheckoutPlanOf(t, ctx, "topic", "")
	if provisiontest.Codes(p.Problems) != kit.ProblemBranchAmbiguous || p.Token != nil {
		t.Errorf("plan problems %s", provisiontest.Codes(p.Problems))
	}

	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "checkout.defaultRemote", "upstream")
	if _, err := provision.Checkout(ctx, "topic", "", provision.NewOptions{NoSetup: true}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := provision.UpstreamOf(t, ctx, "topic"); got != "refs/remotes/upstream/topic" {
		t.Errorf("upstream = %q, want checkout.defaultRemote's", got)
	}
}

// A local branch of that name is checked out as it always was, whatever a
// remote has: the branch is not moved and its upstream not touched.
func TestCheckoutPrefersTheLocalBranch(t *testing.T) {
	t.Parallel()
	ctx := provision.RemoteFixture(t, []string{"origin"}, "origin/topic")
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "topic")
	local := provision.OidOf(t, ctx, "refs/heads/topic")
	p := provisiontest.CheckoutPlanOf(t, ctx, "topic", "")
	if p.Source == nil || *p.Source != "local" || p.Remote != nil || p.RemoteRef != nil || p.RemoteCommit != nil {
		t.Errorf("plan = %+v", p)
	}
	if _, err := provision.Checkout(ctx, "topic", "", provision.NewOptions{NoSetup: true}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if provision.OidOf(t, ctx, "refs/heads/topic") != local || provision.UpstreamOf(t, ctx, "topic") != "" {
		t.Error("the local branch was changed")
	}
}

// What cannot be checked out is a problem in the plan, with no token.
func TestCheckoutPlanRemoteProblems(t *testing.T) {
	t.Parallel()
	ctx := provision.RemoteFixture(t, []string{"origin"}, "origin/topic")
	testkit.GitIn(t, ctx.Repo.MainRoot, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/topic")
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "taken")
	testkit.GitIn(t, ctx.Repo.MainRoot, "update-ref", "refs/remotes/origin/taken", "HEAD")
	for arg, want := range map[string]string{
		"origin/nope":  kit.ProblemRemoteBranchMissing,
		"origin/HEAD":  kit.ProblemRemoteBranchMissing,
		"nope":         kit.ProblemBranchMissing,
		"origin/taken": kit.ProblemBranchExists,
	} {
		p := provisiontest.CheckoutPlanOf(t, ctx, arg, "")
		if got := provisiontest.Codes(p.Problems); got != want || p.Token != nil {
			t.Errorf("%s: problems %s, want %s", arg, got, want)
		}
	}
	if _, err := provision.Checkout(ctx, "origin/nope", "", provision.NewOptions{NoSetup: true}, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "git fetch") {
		t.Errorf("want a refusal that says the refs are as last fetched, got %v", err)
	}
}

// The plan for a remote branch says it will create and track it, pins the
// remote-tracking commit, and writes nothing.
func TestCheckoutPlanForARemoteBranch(t *testing.T) {
	t.Parallel()
	ctx := provision.RemoteFixture(t, []string{"origin"}, "origin/feature/x")
	before := testkit.RepoState(t, ctx.Repo.MainRoot)
	p := provisiontest.CheckoutPlanOf(t, ctx, "origin/feature/x", "")
	if after := testkit.RepoState(t, ctx.Repo.MainRoot); after != before {
		t.Error("the plan wrote to the repository")
	}
	oid := provision.OidOf(t, ctx, "refs/remotes/origin/feature/x")
	switch {
	case p.Token == nil || len(p.Problems) != 0:
		t.Fatalf("plan = %+v", p)
	case *p.Branch != "feature/x" || *p.Work != "feature-x" || p.BranchCommit != nil:
		t.Errorf("branch %v work %v commit %v", *p.Branch, *p.Work, p.BranchCommit)
	case *p.Source != "remote" || *p.Remote != "origin" || *p.RemoteRef != "refs/remotes/origin/feature/x" ||
		*p.RemoteCommit != oid:
		t.Errorf("source %v remote %v ref %v commit %v", *p.Source, *p.Remote, *p.RemoteRef, *p.RemoteCommit)
	}
	var human bytes.Buffer
	if err := provision.CheckoutDryRun(ctx, "origin/feature/x", "", provision.NewOptions{}, &human); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(human.String(), "Would create feature/x from origin/feature/x ("+oid[:7]+"), tracking it\n") {
		t.Errorf("dry run = %q", human.String())
	}
}

// --json: the branch step is created, the upstream is read back, and the
// worktree is on the remote-tracking commit the plan pinned.
func TestCheckoutJSONFromARemote(t *testing.T) {
	t.Parallel()
	ctx := provision.RemoteFixture(t, []string{"origin"}, "origin/topic")
	p := provisiontest.CheckoutPlanOf(t, ctx, "origin/topic", "")
	r, err := checkoutJSON(t, ctx, "origin/topic", "", provision.NewOptions{NoSetup: true}, *p.Token)
	if err != nil || r.Outcome != provision.CreateCreated {
		t.Fatalf("err %v result %+v", err, r)
	}
	if *r.ExpectedCommit != *p.RemoteCommit || *r.Commit != *p.RemoteCommit {
		t.Errorf("expected %v commit %v, want %s", *r.ExpectedCommit, *r.Commit, *p.RemoteCommit)
	}
	if *r.Source != "remote" || *r.Remote != "origin" || *r.RemoteRef != "refs/remotes/origin/topic" ||
		r.Upstream == nil || *r.Upstream != "refs/remotes/origin/topic" {
		t.Errorf("result = %+v", r)
	}
	b := provision.StepOf(r.CreateResult, "branch")
	if b.Result != kit.StepCreated || *b.Commit != *p.RemoteCommit || b.Reason == nil || *b.Reason != "tracking origin/topic" {
		t.Errorf("branch step %+v", b)
	}

	// A local checkout reports the branch's upstream as it is, and changes
	// nothing about it.
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "plain")
	r, err = checkoutJSON(t, ctx, "plain", "", provision.NewOptions{NoSetup: true}, "")
	if err != nil || *r.Source != "local" || r.Upstream != nil || r.Remote != nil || provision.StepOf(r.CreateResult, "branch").Result != kit.StepUntouched {
		t.Errorf("err %v result %+v", err, r)
	}
}

// --expect refuses, touching nothing, when the remote-tracking ref moved
// since the plan, or a local branch of that name appeared.
func TestCheckoutExpectRefusesAMovedRemote(t *testing.T) {
	t.Parallel()
	ctx := provision.RemoteFixture(t, []string{"origin"}, "origin/topic")
	main := ctx.Repo.MainRoot
	p := provisiontest.CheckoutPlanOf(t, ctx, "origin/topic", "")
	testkit.GitIn(t, main, "update-ref", "refs/remotes/origin/topic", "main")
	before := testkit.RepoState(t, main)
	r, err := checkoutJSON(t, ctx, "origin/topic", "", provision.NewOptions{NoSetup: true}, *p.Token)
	if err == nil || r.Outcome != provision.CreateRefused || provisiontest.Codes(r.Problems) != kit.ProblemPlanChanged {
		t.Errorf("moved remote: err %v result %+v", err, r)
	}
	if after := testkit.RepoState(t, main); after != before {
		t.Error("a refused checkout wrote to the repository")
	}

	p = provisiontest.CheckoutPlanOf(t, ctx, "topic", "")
	testkit.GitIn(t, main, "branch", "topic", "main")
	r, err = checkoutJSON(t, ctx, "topic", "", provision.NewOptions{NoSetup: true}, *p.Token)
	if err == nil || r.Outcome != provision.CreateRefused || provisiontest.Codes(r.Problems) != kit.ProblemPlanChanged {
		t.Errorf("branch appeared: err %v result %+v", err, r)
	}
	if _, err := os.Stat(*p.Path); err == nil {
		t.Error("a refused checkout created the worktree")
	}
}

// A local branch that appears after the plan was checked, just before it
// is created, is never overwritten: the run is refused as planChanged.
func TestCheckoutNeverOverwritesABranchThatAppeared(t *testing.T) {
	t.Parallel()
	ctx := provision.RemoteFixture(t, []string{"origin"}, "origin/topic")
	main := ctx.Repo.MainRoot
	target, prob := provision.ResolveCheckout(ctx, "origin/topic")
	if prob != nil {
		t.Fatal(prob.Message)
	}
	testkit.GitIn(t, main, "branch", "topic", "main")
	mainOID := provision.OidOf(t, ctx, "main")
	path := ctx.Scheme().Dir("feat", "topic")
	j := provision.NewCreateJournal(&bytes.Buffer{}, "checkout")
	err := provision.CheckoutAdd(ctx, path, target, nil, &bytes.Buffer{})()
	if err == nil {
		t.Fatal("want a refusal")
	}
	if provision.OidOf(t, ctx, "refs/heads/topic") != mainOID || provision.UpstreamOf(t, ctx, "topic") != "" {
		t.Error("the branch that appeared was changed")
	}
	j.AddFailed(ctx, path, err)
	if j.Res.Problems == nil || provisiontest.Codes(j.Res.Problems) != kit.ProblemPlanChanged || j.Outcome(false) != provision.CreateRefused {
		t.Errorf("problems %v outcome %s", j.Res.Problems, j.Outcome(false))
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the worktree was created")
	}
}

// A plan made for <remote>/<branch> is refused as planChanged once a local
// branch of that name appears, whatever else the new plan finds wrong.
func TestCheckoutExpectRefusesAnExplicitRemoteOnceTheBranchAppears(t *testing.T) {
	t.Parallel()
	ctx := provision.RemoteFixture(t, []string{"origin"}, "origin/topic")
	p := provisiontest.CheckoutPlanOf(t, ctx, "origin/topic", "")
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "topic", "main")
	r, err := checkoutJSON(t, ctx, "origin/topic", "", provision.NewOptions{NoSetup: true}, *p.Token)
	if err == nil || r.Outcome != provision.CreateRefused || provisiontest.Codes(r.Problems) != kit.ProblemPlanChanged+","+kit.ProblemBranchExists {
		t.Errorf("err %v problems %s outcome %s", err, provisiontest.Codes(r.Problems), r.Outcome)
	}
}
