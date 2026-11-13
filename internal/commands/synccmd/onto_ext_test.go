package synccmd_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

func skipShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A feature rebased onto a local integration branch: the owned line comes
// out against the integration branch's version, not trunk's, and the
// safety ref pins the old tip.
func TestOntoALocalBranchResolvesTheVersionAgainstIt(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, bump := provisiontest.RunFixture(t, false)
	integ := synccmdtest.Integration(t, ctx)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.Onto("integ", ""), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"onto integ " + git.ShortID(integ, 7), "✓ rebased 1 commit onto integ"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if got := testkit.GitOut(t, bump, "rev-parse", "HEAD~1"); got != integ {
		t.Fatalf("bump sits on %s, want integ %s\n%s", got, integ, s)
	}
	if got := read(t, filepath.Join(bump, "v.txt")); got != "1.0.8\n" {
		t.Errorf("v.txt %q, want 1.0.8 above integ's 1.0.7", got)
	}
	if got := testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", wtsync.SafetyRef("feat_wt/bump", 99)); got != old {
		t.Errorf("safety ref at %s, want the old tip %s", got, old)
	}
	// Trunk did not move, and nothing was rebased onto it.
	if testkit.GitAncestor(t, ctx.Repo.MainRoot, "integ", "origin/main") {
		t.Error("trunk moved")
	}
}

// A remote-tracking ref is fetched with the run's fetch, and --no-fetch
// takes it as last fetched.
func TestOntoARemoteTrackingRefIsFetchedAndNoFetchTakesTheLastFetch(t *testing.T) {
	t.Parallel()
	skipShort(t)
	for _, noFetch := range []bool{true, false} {
		ctx, bump := provisiontest.RunFixture(t, false)
		main := ctx.Repo.MainRoot
		first := synccmdtest.Branch(t, ctx, "rel", "main", map[string]string{"r.txt": "1\n"})
		testkit.GitOut(t, main, "fetch", "-q", "origin")
		second := synccmdtest.Branch(t, ctx, "rel2", "rel", map[string]string{"r.txt": "2\n"})
		testkit.GitOut(t, main, "branch", "-f", "rel", second)
		opts := synccmdtest.Onto("origin/rel", "")
		opts.NoFetch, opts.NoFFTrunk = noFetch, true
		var out bytes.Buffer
		if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
			t.Fatalf("no-fetch %v: err %v\n%s", noFetch, err, out.String())
		}
		want := second
		if noFetch {
			want = first
		}
		if got := testkit.GitOut(t, bump, "rev-parse", "HEAD~1"); got != want {
			t.Errorf("no-fetch %v: bump sits on %s, want %s\n%s", noFetch, git.ShortID(got, 7), git.ShortID(want, 7), out.String())
		}
		if !strings.Contains(out.String(), "onto origin/rel "+git.ShortID(want, 7)) {
			t.Errorf("no-fetch %v: the run does not name the target:\n%s", noFetch, out.String())
		}
	}
}

// A layer moved off its parent onto another branch: --upstream replays its
// own commits only, and the look says the same before anything moves.
func TestOntoWithUpstreamMovesALayerWithItsOwnCommitsOnly(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, bump := provisiontest.RunFixture(t, false)
	synccmdtest.DeclareSteps(t, ctx, "each_commit:\n  - run: \"true\"\n")
	child := provisiontest.StackFixture(t, ctx)
	integ := synccmdtest.Integration(t, ctx)
	parentTip := testkit.GitOut(t, bump, "rev-parse", "HEAD")

	var look bytes.Buffer
	if err := synccmd.SyncWorktree(ctx, "child", synccmd.SyncOptions{NoFetch: true, Onto: "integ", Upstream: "feat_wt/bump"}, &look); err != nil {
		t.Fatalf("look: %v\n%s", err, look.String())
	}
	l := look.String()
	for _, want := range []string{"against integ " + git.ShortID(integ, 7), "conflict-free", "1 ahead", "checks 1 commit"} {
		if !strings.Contains(l, want) {
			t.Errorf("look lacks %q:\n%s", want, l)
		}
	}
	if strings.Contains(l, "stop") {
		t.Errorf("the look shows a stop below the upstream:\n%s", l)
	}
	look.Reset()
	if err := synccmd.SyncWorktree(ctx, "child", synccmd.SyncOptions{NoFetch: true, Onto: "integ"}, &look); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(look.String(), "2 ahead") || !strings.Contains(look.String(), "checks 2 commits") {
		t.Errorf("without --upstream the look counts the parent's commit too:\n%s", look.String())
	}

	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"child"}, synccmdtest.Onto("integ", "feat_wt/bump"), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "is a stack with") {
		t.Errorf("the parent was added to the run:\n%s", out.String())
	}
	main := ctx.Repo.MainRoot
	if got := testkit.GitOut(t, main, "rev-list", "--count", "integ..feat_wt/child"); got != "1" {
		t.Errorf("%s commits above integ, want child's own one", got)
	}
	if got := testkit.GitOut(t, child, "rev-parse", "HEAD~1"); got != integ {
		t.Errorf("child sits on %s, want integ", got)
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != parentTip {
		t.Error("the parent moved")
	}
}

// The look's simulated result is the tree the run then commits.
func TestOntoTheLookEndsOnTheTreeTheRunCommits(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, _ := provisiontest.RunFixture(t, false)
	child := provisiontest.StackFixture(t, ctx)
	integ := synccmdtest.Integration(t, ctx)
	main := ctx.Repo.MainRoot
	bumpTip := testkit.GitOut(t, main, "rev-parse", "feat_wt/bump")
	cfg, err := wtsync.LoadFromRef(main, testkit.GitOut(t, main, "rev-parse", "origin/main"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := wtsync.SimulateRebaseTo(main, wtsync.Target{Onto: integ, Upstream: bumpTip}, "feat_wt/child", cfg)
	if err != nil || rep.Tree == "" {
		t.Fatalf("simulation %+v %v", rep, err)
	}
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"child"}, synccmdtest.Onto("integ", "feat_wt/bump"), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if got := testkit.GitOut(t, child, "rev-parse", "HEAD^{tree}"); got != rep.Tree {
		t.Errorf("run's tree %s, look's %s", got, rep.Tree)
	}
}

// Two worktrees stacked above the one named: only it moves, and each above
// is listed with the command that follows it onto its immediate parent.
// Run in order, they leave each layer with its own commits only.
func TestOntoAWorktreeStackMovesOnlyTheOneNamedAndListsTheRest(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, bump := provisiontest.RunFixture(t, false)
	child := provisiontest.StackFixture(t, ctx)
	var buf bytes.Buffer
	grand, err := provision.New(ctx, "feat/grand", provision.NewOptions{NoSetup: true, Base: "feat_wt/child"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	gittest.WriteFile(t, filepath.Join(grand, "g.txt"), "g\n")
	testkit.GitOut(t, grand, "add", "-A")
	testkit.GitOut(t, grand, "commit", "-q", "-m", "grand")
	integ := synccmdtest.Integration(t, ctx)
	main := ctx.Repo.MainRoot
	bumpOld := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	childOld := testkit.GitOut(t, child, "rev-parse", "HEAD")

	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.Onto("integ", ""), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	follow := []string{
		"wt sync rebase child --onto feat_wt/bump --upstream " + git.ShortID(bumpOld, 7),
		"wt sync rebase grand --onto feat_wt/child --upstream " + git.ShortID(childOld, 7),
	}
	for _, want := range follow {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if i, j := strings.Index(s, follow[0]), strings.Index(s, follow[1]); i < 0 || j < i {
		t.Errorf("the follow-ups are not bottom first:\n%s", s)
	}
	if testkit.GitOut(t, child, "rev-parse", "HEAD") != childOld {
		t.Fatal("child moved with the run")
	}
	if got := testkit.GitOut(t, bump, "rev-parse", "HEAD~1"); got != integ {
		t.Fatalf("bump sits on %s, want integ", got)
	}
	runs := []struct{ work, onto, upstream string }{
		{"child", "feat_wt/bump", git.ShortID(bumpOld, 7)},
		{"grand", "feat_wt/child", git.ShortID(childOld, 7)},
	}
	for i, r := range runs {
		out.Reset()
		opts := synccmdtest.Onto(r.onto, r.upstream)
		opts.Now = func() time.Time { return time.Unix(0, int64(100+i)) }
		if err := synccmd.SyncRebase(ctx, []string{r.work}, opts, &out); err != nil {
			t.Fatalf("%s: err %v\n%s", r.work, err, out.String())
		}
	}
	for _, rng := range []string{"feat_wt/bump..feat_wt/child", "feat_wt/child..feat_wt/grand", "integ..feat_wt/bump"} {
		if got := testkit.GitOut(t, main, "rev-list", "--count", rng); got != "1" {
			t.Errorf("%s: %s commits, want 1", rng, got)
		}
	}
}

// Classification is against the target: a branch already on trunk is
// still rebased onto a target it is not on, and one the target already
// contains is skipped as nothing ahead of it.
func TestOntoClassifiesAgainstTheTarget(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, _ := provisiontest.RunFixture(t, false)
	var buf bytes.Buffer
	fresh, err := provision.New(ctx, "feat/fresh", provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	gittest.WriteFile(t, filepath.Join(fresh, "f.txt"), "f\n")
	testkit.GitOut(t, fresh, "add", "-A")
	testkit.GitOut(t, fresh, "commit", "-q", "-m", "fresh")
	integ := synccmdtest.Integration(t, ctx)
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"fresh"}, synccmdtest.Onto("integ", ""), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if got := testkit.GitOut(t, fresh, "rev-parse", "HEAD~1"); got != integ {
		t.Fatalf("fresh, on trunk already, was not rebased onto integ:\n%s", out.String())
	}
	synccmdtest.Branch(t, ctx, "landed", "feat_wt/bump", map[string]string{"l.txt": "l\n"})
	out.Reset()
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.Onto("landed", ""), &out); err != nil {
		t.Fatalf("a skip is not an error: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "skipped: nothing ahead of landed") {
		t.Errorf("out %s", out.String())
	}
}

// Every refusal comes before anything moves.
func TestOntoRefusesBeforeAnythingMoves(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, bump := provisiontest.RunFixture(t, false)
	synccmdtest.Integration(t, ctx)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	for _, c := range []struct{ onto, upstream, want string }{
		{"nope", "", "--onto nope: not a commit here"},
		{"feat_wt/bump", "", "--onto feat_wt/bump is bump's own branch"},
		{"integ", "integ", "--upstream integ is not an ancestor of feat_wt/bump"},
	} {
		var out bytes.Buffer
		err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.Onto(c.onto, c.upstream), &out)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("--onto %s --upstream %q: err %v, want %q\n%s", c.onto, c.upstream, err, c.want, out.String())
		}
		if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
			t.Fatalf("--onto %s: bump moved", c.onto)
		}
	}
	if refs := testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

// A branch on the --stack line is no target for it.
func TestOntoRefusesABranchOnTheStackLine(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, s := synccmdtest.StackLine(t, "")
	opts := synccmdtest.Onto("s2", "")
	_, human, err := synccmdtest.StackRun(t, ctx, "tmp/stack", opts)
	if err == nil || !strings.Contains(err.Error(), "--onto s2 is a layer on the line of tmp/stack") {
		t.Fatalf("err %v\n%s", err, human)
	}
	if testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", "s2") != s.Tips["s2"] {
		t.Fatal("s2 moved")
	}
}

func TestOntoUsageMistakes(t *testing.T) {
	t.Parallel()
	ctx, _ := provisiontest.RunFixture(t, false)
	provisiontest.StackFixture(t, ctx)
	synccmdtest.Integration(t, ctx)
	for _, c := range []struct {
		works []string
		opts  synccmd.RunOptions
		want  string
	}{
		{nil, synccmdtest.Onto("integ", ""), "--onto rebases the worktrees you name onto it; name them"},
		{[]string{"bump"}, synccmdtest.Onto("", "integ"), "--upstream needs --onto"},
		{[]string{"bump"}, autosquashOnto("integ"), "--autosquash folds in place"},
		{[]string{"bump", "child"}, synccmdtest.Onto("integ", ""), "child is stacked on bump"},
	} {
		var out bytes.Buffer
		err := synccmd.SyncRebase(ctx, c.works, c.opts, &out)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err %v, want %q", c.works, err, c.want)
		}
	}
	opts := synccmdtest.Onto("integ", "")
	opts.Expect = "1:0123"
	var out bytes.Buffer
	err := synccmd.Up(ctx, "bump", opts, &out)
	if err == nil || !strings.Contains(err.Error(), "wt sync bump --onto integ --json") {
		t.Errorf("wt up --onto --expect: err %v", err)
	}
}

// wt up --onto works where trunk declares nothing, conflict-free.
func TestUpOntoWorksWithoutADeclaration(t *testing.T) {
	t.Parallel()
	skipShort(t)
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	testkit.GitIn(t, main, "remote", "add", "origin", main)
	testkit.GitIn(t, main, "fetch", "-q", "origin")
	ctx, err := kit.Open(main, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	bump, err := provision.New(ctx, "feat/bump", provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	gittest.WriteFile(t, filepath.Join(bump, "b.txt"), "b\n")
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "-m", "b")
	integ := synccmdtest.Branch(t, ctx, "integ", "main", map[string]string{"i.txt": "i\n"})
	var out bytes.Buffer
	opts := synccmdtest.Onto("integ", "")
	if err := synccmd.Up(ctx, "bump", opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if got := testkit.GitOut(t, bump, "rev-parse", "HEAD~1"); got != integ {
		t.Fatalf("bump sits on %s, want integ\n%s", got, out.String())
	}
}

// autosquashOnto is a --stack --autosquash run onto ref.
func autosquashOnto(ref string) synccmd.RunOptions {
	opts := synccmdtest.Onto(ref, "")
	opts.Stack = &synccmd.StackOptions{Autosquash: true}
	return opts
}

// A remote-tracking target is fetched from the branch the remote's own
// fetch refspec maps to it, not from a branch of the same name; one no
// refspec maps is refused before anything moves.
func TestOntoARemoteTrackingRefIsFetchedThroughTheRemotesRefspec(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, bump := provisiontest.RunFixture(t, false)
	main := ctx.Repo.MainRoot
	testkit.GitOut(t, main, "config", "--add", "remote.origin.fetch", "+refs/heads/release:refs/remotes/origin/integration")
	first := synccmdtest.Branch(t, ctx, "release", "main", map[string]string{"r.txt": "1\n"})
	testkit.GitOut(t, main, "fetch", "-q", "origin")
	second := synccmdtest.Branch(t, ctx, "release2", "release", map[string]string{"r.txt": "2\n"})
	testkit.GitOut(t, main, "branch", "-f", "release", second)

	testkit.GitOut(t, main, "remote", "add", "mirror", main)
	testkit.GitOut(t, main, "config", "--replace-all", "remote.mirror.fetch", "+refs/heads/release:refs/remotes/mirror/integration")
	testkit.GitOut(t, main, "update-ref", "refs/remotes/mirror/other", first)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	opts := synccmdtest.Onto("mirror/other", "")
	opts.NoFetch, opts.NoFFTrunk = false, true
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "no fetch refspec of mirror writes refs/remotes/mirror/other") {
		t.Fatalf("err %v, want the unmapped ref refused\n%s", err, out.String())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("bump moved")
	}

	opts = synccmdtest.Onto("origin/integration", "")
	opts.NoFetch, opts.NoFFTrunk = false, true
	out.Reset()
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if got := testkit.GitOut(t, bump, "rev-parse", "HEAD~1"); got != second {
		t.Errorf("bump sits on %s, want release's fetched tip %s\n%s", git.ShortID(got, 7), git.ShortID(second, 7), out.String())
	}
}

// An --onto refusal comes before the run touches local trunk: a trunk the
// run would fast-forward is left where it is, in a repository that declares
// .wt-sync.yaml and in one that does not, by wt sync rebase and wt up.
func TestOntoRefusesBeforeLocalTrunkMoves(t *testing.T) {
	t.Parallel()
	skipShort(t)
	for _, declared := range []bool{true, false} {
		f := synccmdtest.NewTrunkFixture(t)
		if declared {
			f.Declare(t)
		}
		opts := f.WithWork(t)
		f.AdvanceOrigin(t, 1)
		local := f.Local(t)
		for _, c := range []struct {
			onto, upstream, want string
			up                   bool
		}{
			{"feat_wt/x", "", "is x's own branch", false},
			{"origin/main", "origin/main", "is not an ancestor of feat_wt/x", false},
			{"feat_wt/x", "", "is x's own branch", true},
		} {
			o := opts
			o.Onto, o.Upstream = c.onto, c.upstream
			var out bytes.Buffer
			var err error
			if c.up {
				err = synccmd.Up(f.Ctx, "x", o, &out)
			} else {
				err = synccmd.SyncRebase(f.Ctx, []string{"x"}, o, &out)
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("declared %v, up %v, --onto %s: err %v, want %q\n%s", declared, c.up, c.onto, err, c.want, out.String())
			}
			if got := f.Local(t); got != local {
				t.Errorf("declared %v, up %v, --onto %s: local trunk moved to %s before the refusal\n%s", declared, c.up, c.onto, git.ShortID(got, 7), out.String())
			}
		}
	}
}

// cherryPickedAndReverted has trunk pick commit sha and then revert it,
// fetched: trunk once carried the patch and no longer does.
func cherryPickedAndReverted(t *testing.T, ctx *kit.Context, sha string) {
	t.Helper()
	main := ctx.Repo.MainRoot
	testkit.GitOut(t, main, "cherry-pick", sha)
	testkit.GitOut(t, main, "revert", "--no-edit", "HEAD")
	testkit.GitOut(t, main, "fetch", "-q", "origin")
}

// A target that picked a commit and then reverted it does not carry it:
// git's rebase --onto replays it, and so does wt.
func TestOntoAboveAnUpstreamKeepsACommitTheTargetPickedAndReverted(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, bump := provisiontest.RunFixture(t, false)
	main := ctx.Repo.MainRoot
	gittest.WriteFile(t, filepath.Join(bump, "c.txt"), "c\n")
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "-m", "c")
	c := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	b := testkit.GitOut(t, bump, "rev-parse", "HEAD~1")
	cherryPickedAndReverted(t, ctx, c)
	// By hand: git rebase --onto main b on a copy of the branch.
	testkit.GitOut(t, main, "branch", "hand", c)
	testkit.GitOut(t, main, "rebase", "-q", "--onto", "main", b, "hand")
	testkit.GitOut(t, main, "checkout", "-q", "main")
	if n := testkit.GitOut(t, main, "rev-list", "--count", "main..hand"); n != "1" {
		t.Fatalf("git's own rebase replays %s commits, want c", n)
	}
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.Onto("main", b), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if got, want := testkit.GitOut(t, main, "rev-parse", "feat_wt/bump^{tree}"), testkit.GitOut(t, main, "rev-parse", "hand^{tree}"); got != want {
		t.Errorf("wt's tree %s, git's %s: c was dropped\n%s", got, want, out.String())
	}
	if n := testkit.GitOut(t, main, "rev-list", "--count", "main..feat_wt/bump"); n != "1" {
		t.Errorf("%s commits above main, want c\n%s", n, out.String())
	}
}

// Without --onto, a worktree stack's child on that history comes out as it
// always has: its commit, which trunk picked and reverted, is replayed.
func TestAWorktreeStackChildKeepsACommitTrunkPickedAndReverted(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, _ := provisiontest.RunFixture(t, false)
	child := provisiontest.StackFixture(t, ctx)
	cherryPickedAndReverted(t, ctx, testkit.GitOut(t, child, "rev-parse", "HEAD"))
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"child"}, synccmdtest.NoAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	main := ctx.Repo.MainRoot
	if n := testkit.GitOut(t, main, "rev-list", "--count", "feat_wt/bump..feat_wt/child"); n != "1" {
		t.Errorf("child has %s commits above bump, want its own one\n%s", n, out.String())
	}
	if _, err := os.Stat(filepath.Join(child, "c.txt")); err != nil {
		t.Errorf("child lost c.txt: %v", err)
	}
}
