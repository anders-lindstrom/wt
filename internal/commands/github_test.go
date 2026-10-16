package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The PR column of `wt list`: a worktree whose branch is a pull request's
// head gets its number and state, and everything else gets a dash.
func TestListShowsThePullRequestColumn(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	if _, err := provision.New(ctx, "feat/api-tidy", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := List(ctx, ListOptions{}, &out, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PR") {
		t.Fatalf("no PR column:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "#12 open") {
		t.Errorf("the pull request is missing:\n%s", out.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "api-tidy") && !strings.Contains(line, " -  ") {
			t.Errorf("a worktree with no pull request is not dashed: %q", line)
		}
	}
}

// Every way GitHub can be out of play leaves `wt list` byte for byte what it
// was before the column existed. This is the whole contract for the column:
// `wt list` is read by scripts and by completion.
//
// Every case asks with Refresh, and the ones where gh is supposed to run say
// so: the subtests share one repository, so one that read a cached answer
// would pass without gh having been reached at all.
func TestListIsUnchangedWhenGitHubIsNotInPlay(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	// The listing without the column, taken with the integration off rather
	// than with whatever gh the machine running the tests happens to have.
	ctx.User.GitHub = false
	var before bytes.Buffer
	if err := List(ctx, ListOptions{}, &before, 0); err != nil {
		t.Fatal(err)
	}
	ctx.User.GitHub = true

	for name, tc := range map[string]struct {
		set      func(t *testing.T, ctx *kit.Context) string
		wantCall bool
	}{
		"off in the user config": {func(t *testing.T, ctx *kit.Context) string {
			log := testkit.FakeGitHub(t, testkit.OpenPR(12, "fix_wt/login-crash", "x"))
			ctx.User.GitHub = false
			return log
		}, false},
		"no gh": {func(t *testing.T, _ *kit.Context) string {
			testkit.StubGitHub(t, github.CLI{}, testkit.GhRepo, true)
			return ""
		}, false},
		"not GitHub": {func(t *testing.T, _ *kit.Context) string {
			testkit.StubGitHub(t, github.CLI{Exe: "/nope/gh"}, github.Remote{}, false)
			return ""
		}, false},
		"gh fails": {func(t *testing.T, _ *kit.Context) string {
			return testkit.FakeGitHubWith(t, `[ "$1 $2" = 'api graphql' ] && { echo offline >&2; exit 1; }`,
				testkit.OpenPR(12, "fix_wt/login-crash", "x"))
		}, true},
		"no pull request for any worktree": {func(t *testing.T, _ *kit.Context) string {
			return testkit.FakeGitHub(t, testkit.OpenPR(12, "somebody-elses-branch", "x"))
		}, true},
	} {
		t.Run(name, func(t *testing.T) {
			log := tc.set(t, ctx)
			defer func() { ctx.User.GitHub = true }()
			var out bytes.Buffer
			if err := List(ctx, ListOptions{Refresh: true}, &out, 0); err != nil {
				t.Fatal(err)
			}
			if out.String() != before.String() {
				t.Errorf("wt list changed:\n%s\nwant:\n%s", out.String(), before.String())
			}
			if tc.wantCall && len(testkit.ArgvOf(t, log)) == 0 {
				t.Error("gh was never run, so this proves nothing about what wt does with its answer")
			}
		})
	}
}

// The PR column is served from a cache, and a listing says so once the answer
// is a minute old. Under a minute, and with no cache behind the column at all,
// the listing is what it was.
func TestListSaysHowOldACachedColumnIs(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	var fresh bytes.Buffer
	if err := List(ctx, ListOptions{}, &fresh, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fresh.String(), "pull requests as of") {
		t.Errorf("a listing that asked GitHub itself dated its own answer:\n%s", fresh.String())
	}

	backdatePRCache(t, ctx, testkit.GhRepo, 3*time.Minute)
	var old bytes.Buffer
	if err := List(ctx, ListOptions{}, &old, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(old.String(), "pull requests as of 3m ago — `wt list --refresh` asks GitHub again") {
		t.Errorf("wt list said\n%s\nwant the age of the cached column", old.String())
	}

	// --no-pr has no column, so it has no legend either.
	var none bytes.Buffer
	if err := List(ctx, ListOptions{NoPR: true}, &none, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(none.String(), "pull requests as of") {
		t.Errorf("--no-pr printed the age of a column it did not print:\n%s", none.String())
	}
}

// backdatePRCache makes every answer in the cache by how old.
func backdatePRCache(t *testing.T, ctx *kit.Context, r github.Remote, by time.Duration) {
	t.Helper()
	c := kit.ReadPRCache(ctx, r)
	for _, checks := range []bool{false, true} {
		answers := map[string]*github.PR{}
		for branch, e := range c.Branches {
			if e.Checks == checks {
				answers[branch] = e.PR
			}
		}
		kit.WritePRCacheChecks(ctx, r, answers, time.Now().Add(-by), checks)
	}
}

// wt status of one worktree carries its pull request, when it has one and
// GitHub answers. One bounded call, restricted to that branch.
func TestStatusWorktreeCarriesThePullRequest(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := StatusWorktree(ctx, "login-crash", StatusOptions{Agents: []wtsync.Agent{}}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pr      #12 open · Login crash") {
		t.Errorf("wt status said\n%s", out.String())
	}
	if got := testkit.ArgvOf(t, log); len(got) != 1 || !strings.Contains(got[0], "b0=fix_wt/login-crash") {
		t.Errorf("argv = %q, want one call for that branch alone", got)
	}
}

// A worktree with no pull request, and a GitHub that cannot be reached, both
// leave wt status with no pr line at all.
func TestStatusWorktreeIsSilentWithoutOne(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	for name, set := range map[string]func(t *testing.T){
		"no pull request": func(t *testing.T) { testkit.FakeGitHub(t) },
		"no gh":           func(t *testing.T) { testkit.StubGitHub(t, github.CLI{}, testkit.GhRepo, true) },
		"gh fails": func(t *testing.T) {
			testkit.FakeGitHubWith(t, `[ "$1 $2" = 'api graphql' ] && { echo offline >&2; exit 1; }`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			set(t)
			var out bytes.Buffer
			if err := StatusWorktree(ctx, "login-crash", StatusOptions{Agents: []wtsync.Agent{}}, &out); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "  pr ") {
				t.Errorf("wt status said\n%s", out.String())
			}
		})
	}
}

// --no-pr is the way out of the call the column costs: around a second,
// against 0.02s for the rest of the listing.
func TestListNoPRAsksGitHubNothing(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := List(ctx, ListOptions{NoPR: true}, &out, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "#12") {
		t.Errorf("--no-pr still printed the column:\n%s", out.String())
	}
	if got := testkit.ArgvOf(t, log); got != nil {
		t.Errorf("gh was run: %q", got)
	}
}

// wt status has wt list's PR column, from the same cache, dated the same way
// once the answer is a minute old; --no-pr leaves it out and asks nothing.
func TestStatusShowsThePullRequestColumn(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	if _, err := provision.New(ctx, "feat/api-tidy", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	var none bytes.Buffer
	if err := Status(ctx, StatusOptions{NoPR: true}, &none, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(none.String(), "PR") || strings.Contains(none.String(), "#12") {
		t.Errorf("--no-pr still printed the column:\n%s", none.String())
	}
	if got := testkit.ArgvOf(t, log); got != nil {
		t.Errorf("--no-pr ran gh: %q", got)
	}

	var out bytes.Buffer
	if err := Status(ctx, StatusOptions{}, &out, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PR") || !strings.Contains(out.String(), "#12 open") {
		t.Errorf("no PR column:\n%s", out.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "api-tidy") && !strings.Contains(line, " -  ") {
			t.Errorf("a worktree with no pull request is not dashed: %q", line)
		}
	}
	if strings.Contains(out.String(), "pull requests as of") {
		t.Errorf("a status that asked GitHub itself dated its own answer:\n%s", out.String())
	}

	backdatePRCache(t, ctx, testkit.GhRepo, 3*time.Minute)
	var old bytes.Buffer
	if err := Status(ctx, StatusOptions{}, &old, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(old.String(), "pull requests as of 3m ago — `wt status --refresh` asks GitHub again") {
		t.Errorf("wt status said\n%s\nwant the age of the cached column", old.String())
	}
	if got := testkit.ArgvOf(t, log); len(got) != 1 {
		t.Errorf("gh ran %d times, want once: the cache answers the second status", len(got))
	}
}

// wt status <work> takes --refresh and --no-pr as wt list does: the one asks
// GitHub past the cache, the other asks nothing and prints no pr line.
func TestStatusWorktreeRefreshAndNoPR(t *testing.T) {
	// Not parallel: swaps kit.FindGitHub, GitHubRemote and LookupDeadline.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	status := func(opts StatusOptions) string {
		t.Helper()
		opts.Agents = []wtsync.Agent{}
		var out bytes.Buffer
		if err := StatusWorktree(ctx, "login-crash", opts, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	calls := func() int { return len(testkit.ArgvOf(t, log)) }

	status(StatusOptions{})
	status(StatusOptions{})
	if calls() != 1 {
		t.Fatalf("gh ran %d times, want once: the cache answers the second status", calls())
	}
	if out := status(StatusOptions{Refresh: true}); !strings.Contains(out, "#12 open") {
		t.Errorf("--refresh lost the pull request:\n%s", out)
	}
	if calls() != 2 {
		t.Errorf("--refresh did not ask GitHub again: %d calls", calls())
	}
	if out := status(StatusOptions{NoPR: true}); strings.Contains(out, "  pr ") {
		t.Errorf("--no-pr printed a pr line:\n%s", out)
	}
	if calls() != 2 {
		t.Errorf("--no-pr ran gh: %d calls", calls())
	}
}
