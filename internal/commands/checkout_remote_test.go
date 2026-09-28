package commands

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"
)

// remoteFixture is a repository with remotes configured and remote-tracking
// refs written as a fetch would have left them, without any fetch: each
// entry of refs is "<remote>/<branch>", pointed at a commit of its own.
func remoteFixture(t *testing.T, remotes []string, refs ...string) *Context {
	t.Helper()
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	main := ctx.Repo.MainRoot
	for _, r := range remotes {
		gitIn(t, main, "remote", "add", r, "https://example.invalid/"+r+".git")
	}
	for _, ref := range refs {
		gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "on "+ref)
		gitIn(t, main, "update-ref", "refs/remotes/"+ref, "HEAD")
	}
	gitIn(t, main, "reset", "-q", "--hard", "HEAD~"+strconv.Itoa(len(refs)))
	return ctx
}

func oidOf(t *testing.T, ctx *Context, ref string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, ctx.Repo.MainRoot, "rev-parse", ref))
}

func upstreamOf(t *testing.T, ctx *Context, branch string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "--format=%(upstream)", "refs/heads/"+branch))
}

// wt checkout <remote>/<branch> makes the local branch at the
// remote-tracking commit, tracking it, and puts the worktree on it.
func TestCheckoutCreatesATrackingBranchFromARemote(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin"}, "origin/feature/x")
	want := oidOf(t, ctx, "refs/remotes/origin/feature/x")

	var buf bytes.Buffer
	path, err := Checkout(ctx, "origin/feature/x", "", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("Checkout: %v\n%s", err, buf.String())
	}
	if path != ctx.Scheme().Dir("feat", "feature-x") {
		t.Errorf("path = %q", path)
	}
	if got := ctx.Repo.BranchAt(path); got != "feature/x" {
		t.Errorf("worktree on %q, want feature/x", got)
	}
	if got := oidOf(t, ctx, "refs/heads/feature/x"); got != want {
		t.Errorf("branch at %s, want the remote-tracking commit %s", got, want)
	}
	if got := upstreamOf(t, ctx, "feature/x"); got != "refs/remotes/origin/feature/x" {
		t.Errorf("upstream = %q", got)
	}
	if !strings.Contains(buf.String(), "Creating feature/x at "+path+" (from origin/feature/x, tracking it)") {
		t.Errorf("progress = %q", buf.String())
	}
}

// A bare name no local branch has is looked for on every remote; exactly
// one having it is enough.
func TestCheckoutBareNameFindsTheOneRemoteThatHasIt(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin", "upstream"}, "upstream/release-2.1", "origin/other")
	path, err := Checkout(ctx, "release-2.1", "", NewOptions{NoSetup: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Repo.BranchAt(path) != "release-2.1" || upstreamOf(t, ctx, "release-2.1") != "refs/remotes/upstream/release-2.1" {
		t.Errorf("branch %q upstream %q", ctx.Repo.BranchAt(path), upstreamOf(t, ctx, "release-2.1"))
	}
}

// Several remotes having the name is refused, naming them, with nothing
// made — unless checkout.defaultRemote picks one, as it does for git.
func TestCheckoutBareNameOnSeveralRemotes(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin", "upstream"}, "origin/topic", "upstream/topic")
	before := repoState(t, ctx.Repo.MainRoot)
	_, err := Checkout(ctx, "topic", "", NewOptions{NoSetup: true}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "origin/topic") || !strings.Contains(err.Error(), "upstream/topic") {
		t.Fatalf("want a refusal naming both, got %v", err)
	}
	if after := repoState(t, ctx.Repo.MainRoot); after != before {
		t.Error("a refused checkout wrote to the repository")
	}
	p := checkoutPlanOf(t, ctx, "topic", "")
	if codes(p.Problems) != ProblemBranchAmbiguous || p.Token != nil {
		t.Errorf("plan problems %s", codes(p.Problems))
	}

	gitIn(t, ctx.Repo.MainRoot, "config", "checkout.defaultRemote", "upstream")
	if _, err := Checkout(ctx, "topic", "", NewOptions{NoSetup: true}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := upstreamOf(t, ctx, "topic"); got != "refs/remotes/upstream/topic" {
		t.Errorf("upstream = %q, want checkout.defaultRemote's", got)
	}
}

// A local branch of that name is checked out as it always was, whatever a
// remote has: the branch is not moved and its upstream not touched.
func TestCheckoutPrefersTheLocalBranch(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin"}, "origin/topic")
	gitIn(t, ctx.Repo.MainRoot, "branch", "topic")
	local := oidOf(t, ctx, "refs/heads/topic")
	p := checkoutPlanOf(t, ctx, "topic", "")
	if p.Source == nil || *p.Source != "local" || p.Remote != nil || p.RemoteRef != nil || p.RemoteCommit != nil {
		t.Errorf("plan = %+v", p)
	}
	if _, err := Checkout(ctx, "topic", "", NewOptions{NoSetup: true}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if oidOf(t, ctx, "refs/heads/topic") != local || upstreamOf(t, ctx, "topic") != "" {
		t.Error("the local branch was changed")
	}
}

// A local branch literally called <remote>/<branch> is the exact name, and
// wins over the remote-tracking ref.
func TestCheckoutExactLocalNameWinsOverARemote(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin"}, "origin/topic")
	gitIn(t, ctx.Repo.MainRoot, "branch", "origin/topic", "main")
	path, err := Checkout(ctx, "origin/topic", "odd", NewOptions{NoSetup: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ctx.Repo.BranchAt(path); got != "origin/topic" {
		t.Errorf("worktree on %q, want the local branch origin/topic", got)
	}
	if ctx.Repo.BranchExists("topic") {
		t.Error("a branch was created from the remote")
	}
}

// What cannot be checked out is a problem in the plan, with no token.
func TestCheckoutPlanRemoteProblems(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin"}, "origin/topic")
	gitIn(t, ctx.Repo.MainRoot, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/topic")
	gitIn(t, ctx.Repo.MainRoot, "branch", "taken")
	gitIn(t, ctx.Repo.MainRoot, "update-ref", "refs/remotes/origin/taken", "HEAD")
	for arg, want := range map[string]string{
		"origin/nope":  ProblemRemoteBranchMissing,
		"origin/HEAD":  ProblemRemoteBranchMissing,
		"nope":         ProblemBranchMissing,
		"origin/taken": ProblemBranchExists,
	} {
		p := checkoutPlanOf(t, ctx, arg, "")
		if got := codes(p.Problems); got != want || p.Token != nil {
			t.Errorf("%s: problems %s, want %s", arg, got, want)
		}
	}
	if _, err := Checkout(ctx, "origin/nope", "", NewOptions{NoSetup: true}, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "git fetch") {
		t.Errorf("want a refusal that says the refs are as last fetched, got %v", err)
	}
}

// The plan for a remote branch says it will create and track it, pins the
// remote-tracking commit, and writes nothing.
func TestCheckoutPlanForARemoteBranch(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin"}, "origin/feature/x")
	before := repoState(t, ctx.Repo.MainRoot)
	p := checkoutPlanOf(t, ctx, "origin/feature/x", "")
	if after := repoState(t, ctx.Repo.MainRoot); after != before {
		t.Error("the plan wrote to the repository")
	}
	oid := oidOf(t, ctx, "refs/remotes/origin/feature/x")
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
	if err := CheckoutDryRun(ctx, "origin/feature/x", "", NewOptions{}, &human); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(human.String(), "Would create feature/x from origin/feature/x ("+oid[:7]+"), tracking it\n") {
		t.Errorf("dry run = %q", human.String())
	}
}

// --json: the branch step is created, the upstream is read back, and the
// worktree is on the remote-tracking commit the plan pinned.
func TestCheckoutJSONFromARemote(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin"}, "origin/topic")
	p := checkoutPlanOf(t, ctx, "origin/topic", "")
	r, err := checkoutJSON(t, ctx, "origin/topic", "", NewOptions{NoSetup: true}, *p.Token)
	if err != nil || r.Outcome != CreateCreated {
		t.Fatalf("err %v result %+v", err, r)
	}
	if *r.ExpectedCommit != *p.RemoteCommit || *r.Commit != *p.RemoteCommit {
		t.Errorf("expected %v commit %v, want %s", *r.ExpectedCommit, *r.Commit, *p.RemoteCommit)
	}
	if *r.Source != "remote" || *r.Remote != "origin" || *r.RemoteRef != "refs/remotes/origin/topic" ||
		r.Upstream == nil || *r.Upstream != "refs/remotes/origin/topic" {
		t.Errorf("result = %+v", r)
	}
	b := stepOf(r.CreateResult, "branch")
	if b.Result != StepCreated || *b.Commit != *p.RemoteCommit || b.Reason == nil || *b.Reason != "tracking origin/topic" {
		t.Errorf("branch step %+v", b)
	}

	// A local checkout reports the branch's upstream as it is, and changes
	// nothing about it.
	gitIn(t, ctx.Repo.MainRoot, "branch", "plain")
	r, err = checkoutJSON(t, ctx, "plain", "", NewOptions{NoSetup: true}, "")
	if err != nil || *r.Source != "local" || r.Upstream != nil || r.Remote != nil || stepOf(r.CreateResult, "branch").Result != StepUntouched {
		t.Errorf("err %v result %+v", err, r)
	}
}

// --expect refuses, touching nothing, when the remote-tracking ref moved
// since the plan, or a local branch of that name appeared.
func TestCheckoutExpectRefusesAMovedRemote(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin"}, "origin/topic")
	main := ctx.Repo.MainRoot
	p := checkoutPlanOf(t, ctx, "origin/topic", "")
	gitIn(t, main, "update-ref", "refs/remotes/origin/topic", "main")
	before := repoState(t, main)
	r, err := checkoutJSON(t, ctx, "origin/topic", "", NewOptions{NoSetup: true}, *p.Token)
	if err == nil || r.Outcome != CreateRefused || codes(r.Problems) != ProblemPlanChanged {
		t.Errorf("moved remote: err %v result %+v", err, r)
	}
	if after := repoState(t, main); after != before {
		t.Error("a refused checkout wrote to the repository")
	}

	p = checkoutPlanOf(t, ctx, "topic", "")
	gitIn(t, main, "branch", "topic", "main")
	r, err = checkoutJSON(t, ctx, "topic", "", NewOptions{NoSetup: true}, *p.Token)
	if err == nil || r.Outcome != CreateRefused || codes(r.Problems) != ProblemPlanChanged {
		t.Errorf("branch appeared: err %v result %+v", err, r)
	}
	if _, err := os.Stat(*p.Path); err == nil {
		t.Error("a refused checkout created the worktree")
	}
}

// A local branch that appears after the plan was checked, just before it
// is created, is never overwritten: the run is refused as planChanged.
func TestCheckoutNeverOverwritesABranchThatAppeared(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin"}, "origin/topic")
	main := ctx.Repo.MainRoot
	target, prob := resolveCheckout(ctx, "origin/topic")
	if prob != nil {
		t.Fatal(prob.Message)
	}
	gitIn(t, main, "branch", "topic", "main")
	mainOID := oidOf(t, ctx, "main")
	path := ctx.Scheme().Dir("feat", "topic")
	j := NewCreateJournal(&bytes.Buffer{}, "checkout")
	err := checkoutAdd(ctx, path, target, nil, &bytes.Buffer{})()
	if err == nil {
		t.Fatal("want a refusal")
	}
	if oidOf(t, ctx, "refs/heads/topic") != mainOID || upstreamOf(t, ctx, "topic") != "" {
		t.Error("the branch that appeared was changed")
	}
	j.addFailed(ctx, path, err)
	if j.res.Problems == nil || codes(j.res.Problems) != ProblemPlanChanged || j.outcome(false) != CreateRefused {
		t.Errorf("problems %v outcome %s", j.res.Problems, j.outcome(false))
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the worktree was created")
	}
}

// Of two remotes whose names overlap, the one resolved is the upstream:
// git's own inference cannot tell team's origin/topic from team/origin's
// topic, so wt sets the upstream itself.
func TestCheckoutWithOverlappingRemoteNames(t *testing.T) {
	ctx := remoteFixture(t, []string{"team"}, "team/origin/topic")
	// git remote add refuses the overlap; configuration by hand does not.
	gitIn(t, ctx.Repo.MainRoot, "config", "remote.team/origin.url", "https://example.invalid/t.git")
	gitIn(t, ctx.Repo.MainRoot, "config", "remote.team/origin.fetch", "+refs/heads/*:refs/remotes/team/origin/*")
	path, err := Checkout(ctx, "team/origin/topic", "", NewOptions{NoSetup: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Repo.BranchAt(path) != "topic" {
		t.Errorf("worktree on %q", ctx.Repo.BranchAt(path))
	}
	remote := strings.TrimSpace(gitOut(t, ctx.Repo.MainRoot, "config", "branch.topic.remote"))
	merge := strings.TrimSpace(gitOut(t, ctx.Repo.MainRoot, "config", "branch.topic.merge"))
	if remote != "team/origin" || merge != "refs/heads/topic" {
		t.Errorf("upstream %s %s, want team/origin refs/heads/topic", remote, merge)
	}
}

// A plan made for <remote>/<branch> is refused as planChanged once a local
// branch of that name appears, whatever else the new plan finds wrong.
func TestCheckoutExpectRefusesAnExplicitRemoteOnceTheBranchAppears(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin"}, "origin/topic")
	p := checkoutPlanOf(t, ctx, "origin/topic", "")
	gitIn(t, ctx.Repo.MainRoot, "branch", "topic", "main")
	r, err := checkoutJSON(t, ctx, "origin/topic", "", NewOptions{NoSetup: true}, *p.Token)
	if err == nil || r.Outcome != CreateRefused || codes(r.Problems) != ProblemPlanChanged+","+ProblemBranchExists {
		t.Errorf("err %v problems %s outcome %s", err, codes(r.Problems), r.Outcome)
	}
}

// The branch is on the record the moment it is made, before the worktree
// is added: a signal while git worktree add runs must not report a branch
// that exists as never made.
func TestCheckoutRecordsTheBranchBeforeAddingTheWorktree(t *testing.T) {
	ctx := remoteFixture(t, []string{"origin"}, "origin/topic")
	target, prob := resolveCheckout(ctx, "origin/topic")
	if prob != nil {
		t.Fatal(prob.Message)
	}
	j := NewCreateJournal(&bytes.Buffer{}, "checkout")
	br := "topic"
	j.plan(CreatePlan{Branch: &br}, nil, &target.Commit)
	j.checkoutSource(target.source())
	path := ctx.Scheme().Dir("feat", "topic")
	if err := checkoutAdd(ctx, path, target, j, &bytes.Buffer{})(); err != nil {
		t.Fatal(err)
	}
	b := j.step(StepBranch)
	if b.Result != StepCreated || b.Commit == nil || *b.Commit != target.Commit {
		t.Errorf("branch step %+v before the worktree was read back", *b)
	}
	if j.co.Upstream == nil || *j.co.Upstream != "refs/remotes/origin/topic" {
		t.Errorf("upstream %v", j.co.Upstream)
	}
}
