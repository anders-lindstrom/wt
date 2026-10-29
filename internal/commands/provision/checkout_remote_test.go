package provision

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/jsonout"
)

// RemoteFixture is a repository with remotes configured and remote-tracking
// refs written as a fetch would have left them, without any fetch: each
// entry of refs is "<remote>/<branch>", pointed at a commit of its own.
func RemoteFixture(t *testing.T, remotes []string, refs ...string) *kit.Context {
	t.Helper()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	main := ctx.Repo.MainRoot
	for _, r := range remotes {
		testkit.GitIn(t, main, "remote", "add", r, "https://example.invalid/"+r+".git")
	}
	for _, ref := range refs {
		testkit.GitIn(t, main, "commit", "-q", "--allow-empty", "-m", "on "+ref)
		testkit.GitIn(t, main, "update-ref", "refs/remotes/"+ref, "HEAD")
	}
	testkit.GitIn(t, main, "reset", "-q", "--hard", "HEAD~"+strconv.Itoa(len(refs)))
	return ctx
}

func OidOf(t *testing.T, ctx *kit.Context, ref string) string {
	t.Helper()
	return strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", ref))
}

func UpstreamOf(t *testing.T, ctx *kit.Context, branch string) string {
	t.Helper()
	return strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "--format=%(upstream)", "refs/heads/"+branch))
}

// wt checkout <remote>/<branch> makes the local branch at the
// remote-tracking commit, tracking it, and puts the worktree on it.
func TestCheckoutCreatesATrackingBranchFromARemote(t *testing.T) {
	t.Parallel()
	ctx := RemoteFixture(t, []string{"origin"}, "origin/feature/x")
	want := OidOf(t, ctx, "refs/remotes/origin/feature/x")

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
	if got := OidOf(t, ctx, "refs/heads/feature/x"); got != want {
		t.Errorf("branch at %s, want the remote-tracking commit %s", got, want)
	}
	if got := UpstreamOf(t, ctx, "feature/x"); got != "refs/remotes/origin/feature/x" {
		t.Errorf("upstream = %q", got)
	}
	if !strings.Contains(buf.String(), "Creating feature/x at "+path+" (from origin/feature/x, tracking it)") {
		t.Errorf("progress = %q", buf.String())
	}
}

// A bare name no local branch has is looked for on every remote; exactly
// one having it is enough.
func TestCheckoutBareNameFindsTheOneRemoteThatHasIt(t *testing.T) {
	t.Parallel()
	ctx := RemoteFixture(t, []string{"origin", "upstream"}, "upstream/release-2.1", "origin/other")
	path, err := Checkout(ctx, "release-2.1", "", NewOptions{NoSetup: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Repo.BranchAt(path) != "release-2.1" || UpstreamOf(t, ctx, "release-2.1") != "refs/remotes/upstream/release-2.1" {
		t.Errorf("branch %q upstream %q", ctx.Repo.BranchAt(path), UpstreamOf(t, ctx, "release-2.1"))
	}
}

// A local branch literally called <remote>/<branch> is the exact name, and
// wins over the remote-tracking ref.
func TestCheckoutExactLocalNameWinsOverARemote(t *testing.T) {
	t.Parallel()
	ctx := RemoteFixture(t, []string{"origin"}, "origin/topic")
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "origin/topic", "main")
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

// Of two remotes whose names overlap, the one resolved is the upstream:
// git's own inference cannot tell team's origin/topic from team/origin's
// topic, so wt sets the upstream itself.
func TestCheckoutWithOverlappingRemoteNames(t *testing.T) {
	t.Parallel()
	ctx := RemoteFixture(t, []string{"team"}, "team/origin/topic")
	// git remote add refuses the overlap; configuration by hand does not.
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "remote.team/origin.url", "https://example.invalid/t.git")
	testkit.GitIn(t, ctx.Repo.MainRoot, "config", "remote.team/origin.fetch", "+refs/heads/*:refs/remotes/team/origin/*")
	path, err := Checkout(ctx, "team/origin/topic", "", NewOptions{NoSetup: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Repo.BranchAt(path) != "topic" {
		t.Errorf("worktree on %q", ctx.Repo.BranchAt(path))
	}
	remote := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "branch.topic.remote"))
	merge := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "config", "branch.topic.merge"))
	if remote != "team/origin" || merge != "refs/heads/topic" {
		t.Errorf("upstream %s %s, want team/origin refs/heads/topic", remote, merge)
	}
}

// The branch is on the record the moment it is made, before the worktree
// is added: a signal while git worktree add runs must not report a branch
// that exists as never made.
func TestCheckoutRecordsTheBranchBeforeAddingTheWorktree(t *testing.T) {
	t.Parallel()
	ctx := RemoteFixture(t, []string{"origin"}, "origin/topic")
	target, prob := ResolveCheckout(ctx, "origin/topic")
	if prob != nil {
		t.Fatal(prob.Message)
	}
	j := NewCreateJournal(&bytes.Buffer{}, "checkout")
	br := "topic"
	j.plan(CreatePlan{Branch: &br}, nil, &target.Commit)
	j.checkoutSource(target.source())
	path := ctx.Scheme().Dir("feat", "topic")
	if err := CheckoutAdd(ctx, path, target, j, &bytes.Buffer{})(); err != nil {
		t.Fatal(err)
	}
	b := j.step(jsonout.StepBranch)
	if b.Result != jsonout.StepCreated || b.Commit == nil || *b.Commit != target.Commit {
		t.Errorf("branch step %+v before the worktree was read back", *b)
	}
	if j.co.Upstream == nil || *j.co.Upstream != "refs/remotes/origin/topic" {
		t.Errorf("upstream %v", j.co.Upstream)
	}
}
