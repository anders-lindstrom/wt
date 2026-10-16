package admin

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// Each gate says which one it was and what to do about it. This is the
// difference between `wt pr` and `wt list`: here an inactive integration is
// an error a person can act on.
func TestGitHubGateNamesTheGateThatFailed(t *testing.T) {
	for name, tc := range map[string]struct {
		set  func(t *testing.T, ctx *kit.Context)
		want string
	}{
		"off in the user config": {
			func(_ *testing.T, ctx *kit.Context) { ctx.User.GitHub = false },
			"GitHub is off in your wt config; turn it on with `wt config set github true`",
		},
		"no gh": {
			func(t *testing.T, _ *kit.Context) { testkit.StubGitHub(t, github.CLI{}, testkit.GhRepo, true) },
			"gh is not on your PATH",
		},
		"not a GitHub repository": {
			func(t *testing.T, _ *kit.Context) {
				testkit.StubGitHub(t, github.CLI{Exe: "/nope/gh"}, github.Remote{}, false)
			},
			"has no GitHub remote",
		},
		// No gate asks `gh auth status` any more: the real call is what
		// finds out, and gh's own complaint is turned into this line.
		"not logged in": {
			func(t *testing.T, _ *kit.Context) {
				testkit.FakeGitHubWith(t, `echo 'To get started with GitHub CLI, please run:  gh auth login' >&2; exit 1`)
			},
			"gh is not logged in to github.com; run `gh auth login --hostname github.com`",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
			if err != nil {
				t.Fatal(err)
			}
			tc.set(t, ctx)
			var out bytes.Buffer
			err = PRList(ctx, &out, 0)
			if err == nil {
				t.Fatalf("PRList succeeded; said %q", out.String())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// The happy path: somebody else's branch, so the worktree is named for the
// pull request, and gh is the one that puts it on the branch.
func TestPRCheckoutMakesAWorktreeOnThePullRequestsBranch(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t, testkit.OpenPR(12, "residential_fixes", "Residents no longer rewrite a door"))

	var errs bytes.Buffer
	path, err := PRCheckout(ctx, 12, PROptions{}, &errs)
	if err != nil {
		t.Fatalf("PRCheckout: %v", err)
	}
	want := ctx.Scheme().Dir("feat", "pr-12-residential_fixes")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if got := ctx.Repo.BranchAt(path); got != "residential_fixes" {
		t.Errorf("worktree is on %q, want the pull request's head branch", got)
	}
	// The pull request is read by number and gh does the checkout. Nothing
	// lists them, because a number means straight to it, and nothing asks
	// `gh auth status` first: the real call answers that question too.
	argv := testkit.ArgvOf(t, log)
	if len(argv) != 2 || !strings.HasPrefix(argv[0], "pr view 12 ") || argv[1] != "pr checkout 12" {
		t.Errorf("argv = %q", argv)
	}
}

// A head branch that already follows this repository's convention keeps its
// own name, so a branch wt made lands where `wt new` would have put it.
func TestPRCheckoutKeepsAWtBranchsOwnName(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t, testkit.OpenPR(7, "fix_wt/login-crash", "Login crash"))

	var errs bytes.Buffer
	path, err := PRCheckout(ctx, 7, PROptions{}, &errs)
	if err != nil {
		t.Fatal(err)
	}
	if want := ctx.Scheme().Dir("fix", "login-crash"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

// The branch is already in a worktree: that path is the answer, nothing is
// made, and the exit is a success so `cd "$(wt pr checkout 7)"` still works.
func TestPRCheckoutOnABranchAlreadyCheckedOutSaysWhere(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t, testkit.OpenPR(7, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	first, err := PRCheckout(ctx, 7, PROptions{}, &errs)
	if err != nil {
		t.Fatal(err)
	}

	errs.Reset()
	again, err := PRCheckout(ctx, 7, PROptions{}, &errs)
	if err != nil {
		t.Fatalf("a second checkout failed: %v", err)
	}
	if again != first {
		t.Errorf("path = %q, want the worktree that exists (%q)", again, first)
	}
	if !strings.Contains(errs.String(), "#7 is already checked out at "+first) {
		t.Errorf("stderr = %q", errs.String())
	}
}

// A gh that fails must leave no half-made worktree behind: the detached one
// wt created for it is removed, and the path is free for the next attempt.
//
// The repository has a committed file, which is what makes this bite: a
// worktree added with --no-checkout reads to git as one whose every tracked
// file was deleted, and an unforced remove refuses it.
func TestPRCheckoutLeavesNothingBehindWhenGhFails(t *testing.T) {
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main, "add", "README.md")
	testkit.GitIn(t, main, "commit", "-qm", "readme")
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHubWith(t, `[ "$1 $2" = 'pr checkout' ] && { echo 'fetch failed' >&2; exit 1; }`,
		testkit.OpenPR(12, "residential_fixes", "Fixes"))

	var errs bytes.Buffer
	_, err = PRCheckout(ctx, 12, PROptions{}, &errs)
	if err == nil {
		t.Fatal("PRCheckout succeeded with a gh that failed")
	}
	path := ctx.Scheme().Dir("feat", "pr-12-residential_fixes")
	want := "gh pr checkout 12 failed: fetch failed\n" +
		"  the half-made worktree at " + path + " was removed; a retry starts clean"
	if err.Error() != want {
		t.Errorf("err = %v\nwant  %s", err, want)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s was left behind", path)
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 1 {
		t.Errorf("worktrees = %+v, want the main checkout alone", worktrees)
	}
}

// Without a number the open pull requests are listed and handed to the
// picker, and what it picks is what gets checked out.
func TestPRCheckoutWithoutANumberAsks(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t,
		testkit.OpenPR(12, "residential_fixes", "Fixes"),
		testkit.OpenPR(7, "fix_wt/login-crash", "Login crash"))

	var offered []PRChoice
	opts := PROptions{Choose: func(rows []PRChoice) (github.PR, error) {
		offered = rows
		return rows[1].PR, nil
	}}
	var errs bytes.Buffer
	path, err := PRCheckout(ctx, 0, opts, &errs)
	if err != nil {
		t.Fatal(err)
	}
	if len(offered) != 2 || offered[0].PR.Number != 12 {
		t.Errorf("the picker was offered %+v", offered)
	}
	if want := ctx.Scheme().Dir("fix", "login-crash"); path != want {
		t.Errorf("path = %q, want the one that was picked (%q)", path, want)
	}
	if got := testkit.ArgvOf(t, log)[0]; !strings.Contains(got, "viewer{login}") ||
		!strings.Contains(got, "states:OPEN") {
		t.Errorf("the listing was %q, want one call for the open ones and the viewer", got)
	}
}

// With nobody to ask, `wt pr checkout` says so rather than choosing.
func TestPRCheckoutWithoutANumberOrAPickerRefuses(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t, testkit.OpenPR(12, "x", "X"))
	var errs bytes.Buffer
	if _, err := PRCheckout(ctx, 0, PROptions{}, &errs); err == nil {
		t.Fatal("PRCheckout chose for itself")
	}
}

// doctor's GitHub section: what it found, and never a problem — a machine
// without gh is an ordinary machine.
func TestDoctorReportsGitHubState(t *testing.T) {
	for name, tc := range map[string]struct {
		set  func(t *testing.T, ctx *kit.Context)
		want string
	}{
		"off": {
			func(_ *testing.T, ctx *kit.Context) { ctx.User.GitHub = false },
			"  - off in your wt config; turn it on with `wt config set github true`",
		},
		"no gh": {
			func(t *testing.T, _ *kit.Context) { testkit.StubGitHub(t, github.CLI{}, testkit.GhRepo, true) },
			"  - no gh on the PATH",
		},
		"not a GitHub repository": {
			func(t *testing.T, _ *kit.Context) {
				testkit.StubGitHub(t, github.CLI{Exe: "/nope/gh"}, github.Remote{}, false)
			},
			"  - no GitHub remote for",
		},
		"not logged in": {
			func(t *testing.T, _ *kit.Context) {
				testkit.FakeGitHubWith(t, `[ "$1 $2" = 'auth status' ] && `+
					`{ echo 'To get started with GitHub CLI, please run:  gh auth login' >&2; exit 1; }`)
			},
			"which gh is not logged in to; run `gh auth login --hostname github.com`",
		},
		// A check that failed for its own reasons is not a missing login, and
		// saying it is sends you off to log in again for nothing.
		"the login could not be checked": {
			func(t *testing.T, _ *kit.Context) {
				testkit.FakeGitHubWith(t, `[ "$1 $2" = 'auth status' ] && { echo 'dial tcp: no route to host' >&2; exit 1; }`)
			},
			"could not check gh's login for github.com: gh auth status failed: dial tcp: no route to host",
		},
		"usable": {
			func(t *testing.T, _ *kit.Context) { testkit.FakeGitHub(t) },
			"✓ t/demo on github.com — gh is logged in",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
			if err != nil {
				t.Fatal(err)
			}
			tc.set(t, ctx)
			var out bytes.Buffer
			problems, err := Doctor(ctx, &out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("doctor said\n%s\nwant a line %q", out.String(), tc.want)
			}
			if problems != 0 {
				t.Errorf("problems = %d; the GitHub section must never be one:\n%s", problems, out.String())
			}
		})
	}
}

// wt pr list is the same facts read from the pull request's end.
func TestPRListNamesTheWorktreeOnEachPullRequest(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t,
		testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"),
		testkit.OpenPR(13, "nobody-is-on-this", "Something else"))
	var errs bytes.Buffer
	path, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := PRList(ctx, &out, 0); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"t/demo, 2 open pull requests", "#12", path, "#13"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("wt pr list said\n%s\nwant %q", out.String(), want)
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "#13") && !strings.HasSuffix(line, "-") {
			t.Errorf("a pull request nothing is on is not dashed: %q", line)
		}
	}
	// The richer view is the one that pays for the checks.
	if got := testkit.ArgvOf(t, log)[0]; !strings.Contains(got, "statusCheckRollup") {
		t.Errorf("wt pr list did not ask for the checks: %q", got)
	}
}

func TestPRListSaysWhenThereAreNone(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t)
	var out bytes.Buffer
	if err := PRList(ctx, &out, 0); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "t/demo has no open pull requests.\n" {
		t.Errorf("wt pr list said %q", got)
	}
}

// The name a pull request's worktree takes: its own when the branch is one of
// this repository's, and pr-<number>-<branch> otherwise, under the type the
// branch suggests, cut to something a person can type.
func TestPRWorkName(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		pr             github.PR
		wantType, want string
	}{
		"a branch of this repository": {
			testkit.OpenPR(7, "fix_wt/login-crash", "x"), "fix", "login-crash",
		},
		"a type wt does not know is not one": {
			testkit.OpenPR(7, "banana_wt/thing", "x"), "feat", "pr-7-thing",
		},
		"somebody else's branch": {
			testkit.OpenPR(12, "residential_fixes", "x"), "feat", "pr-12-residential_fixes",
		},
		"a type in a path prefix": {
			testkit.OpenPR(12, "fix/login", "x"), "fix", "pr-12-fix-login",
		},
		"a type in a name prefix": {
			testkit.OpenPR(12, "fix-login", "x"), "fix", "pr-12-fix-login",
		},
		"a long branch is cut at a dash": {
			testkit.OpenPR(205, "dependabot/github_actions/actions/checkout-7", "x"),
			"feat", "pr-205-dependabot-github_actions",
		},
		"a branch of nothing falls back to the title": {
			github.PR{Number: 3, Title: "Tidy the API"}, "feat", "pr-3-Tidy-the-API",
		},
		"nothing at all is still the number": {
			github.PR{Number: 3}, "feat", "pr-3",
		},
	} {
		t.Run(name, func(t *testing.T) {
			typ, work := prWorkName(ctx, tc.pr)
			if typ != tc.wantType || work != tc.want {
				t.Errorf("prWorkName() = %q, %q; want %q, %q", typ, work, tc.wantType, tc.want)
			}
		})
	}
}

// GitHub deletes a head branch when the pull request is merged, and `gh pr
// checkout` fetches that branch by name — so a merged pull request is picked
// up from refs/pull/<n>/head instead. An open one gets no such second chance.
func TestPRCheckoutOfAMergedPullRequestUsesThePullRef(t *testing.T) {
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main, "add", "README.md")
	testkit.GitIn(t, main, "commit", "-qm", "readme")
	// The repository is its own remote, holding the pull request's head where
	// GitHub keeps it once the branch is gone.
	testkit.GitIn(t, main, "remote", "add", "origin", main)
	testkit.GitIn(t, main, "update-ref", "refs/pull/34/head", "HEAD")
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	merged := github.PR{Number: 34, Title: "Gone", HeadRefName: "fix_wt/ses-endpoint", State: "MERGED"}
	testkit.FakeGitHubWith(t, `[ "$1 $2" = 'pr checkout' ] && { echo "couldn't find remote ref" >&2; exit 1; }`, merged)

	var errs bytes.Buffer
	path, err := PRCheckout(ctx, 34, PROptions{}, &errs)
	if err != nil {
		t.Fatalf("PRCheckout: %v\n%s", err, errs.String())
	}
	if got := ctx.Repo.BranchAt(path); got != "fix_wt/ses-endpoint" {
		t.Errorf("worktree is on %q", got)
	}
	if !strings.Contains(errs.String(), "#34 is merged; checked out refs/pull/34/head") {
		t.Errorf("stderr = %q", errs.String())
	}
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Errorf("the tree was not populated: %v", err)
	}
}

// wt pr open hands gh the number and --web; nothing else about it is wt's
// business.
func TestPROpenAsksGhToOpenThePullRequest(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	errs.Reset()
	if err := PROpen(ctx, "login-crash", &errs); err != nil {
		t.Fatalf("PROpen: %v", err)
	}
	if !strings.Contains(errs.String(), "Opening #12 open · Login crash") {
		t.Errorf("stderr = %q", errs.String())
	}
	argv := testkit.ArgvOf(t, log)
	if got := argv[len(argv)-1]; got != "pr view 12 --web" {
		t.Errorf("argv = %q, want the last call to be `pr view 12 --web`", argv)
	}
	// The lookup is this worktree's branch and nothing else: one call, no
	// wide listing to pick a number out of.
	if len(argv) != 2 || !strings.Contains(argv[0], "b0=fix_wt/login-crash") {
		t.Errorf("argv = %q, want one branch lookup and the open", argv)
	}
}

// A worktree with no pull request is one line and a non-zero exit, and no
// browser window.
func TestPROpenSaysSoWhenThereIsNone(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t, testkit.OpenPR(12, "somebody-elses-branch", "Not yours"))
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	err = PROpen(ctx, "login-crash", &errs)
	if err == nil {
		t.Fatal("PROpen succeeded without a pull request")
	}
	if !strings.Contains(err.Error(), "no pull request in t/demo has fix_wt/login-crash as its head branch") {
		t.Errorf("err = %v", err)
	}
	for _, call := range testkit.ArgvOf(t, log) {
		if strings.Contains(call, "--web") {
			t.Errorf("a browser was opened anyway: %q", call)
		}
	}
}

// Standing in a worktree, `wt pr open` needs no argument.
func TestPROpenDefaultsToTheWorktreeYouAreIn(t *testing.T) {
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	path, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs)
	if err != nil {
		t.Fatal(err)
	}
	inside, err := kit.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	errs.Reset()
	if err := PROpen(inside, "", &errs); err != nil {
		t.Fatalf("PROpen: %v", err)
	}
	if !strings.Contains(errs.String(), "Opening #12") {
		t.Errorf("stderr = %q", errs.String())
	}
}

// The pull ref is the second chance, not a guarantee: a merged pull request
// GitHub will not hand over either way leaves nothing behind, and the error
// reported is gh's own.
func TestPRCheckoutOfAMergedPullRequestWithNoPullRefFails(t *testing.T) {
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	testkit.GitIn(t, main, "remote", "add", "origin", main)
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	merged := github.PR{Number: 34, Title: "Gone", HeadRefName: "fix_wt/ses-endpoint", State: "MERGED"}
	testkit.FakeGitHubWith(t, `[ "$1 $2" = 'pr checkout' ] && { echo "couldn't find remote ref" >&2; exit 1; }`, merged)

	var errs bytes.Buffer
	_, err = PRCheckout(ctx, 34, PROptions{}, &errs)
	if err == nil {
		t.Fatal("PRCheckout succeeded with neither the branch nor the pull ref")
	}
	if !strings.Contains(err.Error(), "couldn't find remote ref") {
		t.Errorf("err = %v, want gh's own complaint", err)
	}
	path := ctx.Scheme().Dir("fix", "ses-endpoint")
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s was left behind", path)
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 1 {
		t.Errorf("worktrees = %+v, want the main checkout alone", worktrees)
	}
	if _, ok := ctx.Repo.ResolveRef("refs/heads/fix_wt/ses-endpoint"); ok {
		t.Error("the branch gh was asked for was left behind")
	}
}

// A cache nothing can write is a listing that asks GitHub every single time,
// and nothing else says so. doctor does — as a remark, not a problem.
func TestDoctorReportsAPRCacheItCannotWrite(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t)
	dir, err := ctx.Repo.GitDir()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("cannot make %s read-only here: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, info.Mode()) })

	var out bytes.Buffer
	problems, err := Doctor(ctx, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "the pull requests cannot be cached, so every listing asks GitHub again") {
		t.Errorf("doctor said\n%s", out.String())
	}
	if problems != 0 {
		t.Errorf("problems = %d; the GitHub section is never one:\n%s", problems, out.String())
	}
}

// With a cache it can write, doctor says where it is.
func TestDoctorNamesTheCacheItCanWrite(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t)
	var out bytes.Buffer
	if _, err := Doctor(ctx, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "✓ pull requests cached in "+kit.PRCachePath(ctx)) {
		t.Errorf("doctor said\n%s", out.String())
	}
	// Asking the question leaves no cache behind where there was none.
	if _, err := os.Stat(kit.PRCachePath(ctx)); err == nil {
		t.Error("wt doctor created the cache file")
	}
}

// prHead makes a commit on top of base in a repository of its own, which then
// serves it the way GitHub does, at refs/pull/<n>/head, and is main's origin.
// main does not have the commit until it fetches.
func prHead(t *testing.T, main string, number int, base string) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote")
	testkit.GitIn(t, filepath.Dir(remote), "clone", "-q", main, remote)
	testkit.GitIn(t, remote, "config", "user.email", "t@example.com")
	testkit.GitIn(t, remote, "config", "user.name", "T")
	testkit.GitIn(t, remote, "checkout", "-q", "--detach", base)
	testkit.GitIn(t, remote, "commit", "-q", "--allow-empty", "-m", "the pull request's own")
	testkit.GitIn(t, remote, "update-ref", fmt.Sprintf("refs/pull/%d/head", number), "HEAD")
	testkit.GitIn(t, main, "remote", "add", "origin", remote)
	return testkit.GitOut(t, remote, "rev-parse", "HEAD")
}

// localCommits puts n commits of its own on a local branch that no worktree
// is on, starting from base, and returns its tip.
func localCommits(t *testing.T, main, branch, base string, n int) string {
	t.Helper()
	tip := testkit.GitOut(t, main, "rev-parse", base)
	for i := 0; i < n; i++ {
		tip = testkit.GitOut(t, main, "commit-tree", "-p", tip, "-m", fmt.Sprintf("local %d", i+1), tip+"^{tree}")
	}
	testkit.GitIn(t, main, "update-ref", "refs/heads/"+branch, tip)
	return tip
}

// The case gh fails on under a screen of git's advice: the pull request's
// branch is here already and has gone its own way. wt says so before anything
// is made, with the counts and the two ways out, and touches nothing.
func TestPRCheckoutRefusesALocalBranchThatDiverged(t *testing.T) {
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	head := prHead(t, main, 12, "HEAD")
	tip := localCommits(t, main, "residential_fixes", "HEAD", 2)
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	pr := testkit.OpenPR(12, "residential_fixes", "Fixes")
	pr.HeadRefOid = head
	log := testkit.FakeGitHub(t, pr)

	var errs bytes.Buffer
	_, err = PRCheckout(ctx, 12, PROptions{}, &errs)
	if err == nil {
		t.Fatal("PRCheckout went ahead on a branch gh cannot fast-forward")
	}
	want := "#12 is on residential_fixes, and a local branch of that name has diverged from it: " +
		"2 ahead, 1 behind\n" +
		"  gh can only fast-forward a branch that exists; nothing was made\n" +
		"  it carries 2 commits of its own: check that nothing is lost, then either\n" +
		"    rename it:  git branch -m residential_fixes residential_fixes-old\n" +
		"    delete it:  git branch -D residential_fixes\n" +
		"  and run `wt pr checkout 12` again"
	if err.Error() != want {
		t.Errorf("err = %v\nwant  %s", err, want)
	}
	for _, call := range testkit.ArgvOf(t, log) {
		if strings.HasPrefix(call, "pr checkout") {
			t.Errorf("gh was asked to check it out anyway: %q", call)
		}
	}
	if _, err := os.Stat(ctx.Scheme().Dir("feat", "pr-12-residential_fixes")); err == nil {
		t.Error("a worktree was made")
	}
	if got, _ := ctx.Repo.ResolveRef("refs/heads/residential_fixes"); got != tip {
		t.Errorf("the local branch moved: %s, want %s", got, tip)
	}
	// Looking brought the pull request's commits and nothing else.
	if refs := testkit.GitOut(t, main, "for-each-ref", "--format=%(refname)"); refs !=
		"refs/heads/main\nrefs/heads/residential_fixes" {
		t.Errorf("refs = %q", refs)
	}
	if _, err := os.Stat(filepath.Join(main, ".git", "FETCH_HEAD")); err == nil {
		t.Error("FETCH_HEAD was written")
	}
}

// A local branch gh can deal with is gh's to deal with: one the pull request
// is a fast-forward of, and one that is only ahead of it.
func TestPRCheckoutLeavesAFastForwardToGh(t *testing.T) {
	for name, local := range map[string]func(t *testing.T, main, head string){
		"behind": func(t *testing.T, main, _ string) { localCommits(t, main, "residential_fixes", "HEAD", 0) },
		"ahead": func(t *testing.T, main, head string) {
			testkit.GitIn(t, main, "fetch", "-q", "origin", "refs/pull/12/head")
			localCommits(t, main, "residential_fixes", head, 2)
		},
	} {
		t.Run(name, func(t *testing.T) {
			main := testkit.CommittedRepo(t, testkit.MinimalConf)
			head := prHead(t, main, 12, "HEAD")
			local(t, main, head)
			ctx, err := kit.Open(main)
			if err != nil {
				t.Fatal(err)
			}
			pr := testkit.OpenPR(12, "residential_fixes", "Fixes")
			pr.HeadRefOid = head
			log := testkit.FakeGitHubWith(t,
				`[ "$1 $2" = 'pr checkout' ] && { git checkout -q residential_fixes; exit; }`, pr)

			var errs bytes.Buffer
			path, err := PRCheckout(ctx, 12, PROptions{}, &errs)
			if err != nil {
				t.Fatalf("PRCheckout: %v", err)
			}
			if got := ctx.Repo.BranchAt(path); got != "residential_fixes" {
				t.Errorf("worktree is on %q", got)
			}
			if argv := testkit.ArgvOf(t, log); argv[len(argv)-1] != "pr checkout 12" {
				t.Errorf("argv = %q, want gh to have done the checkout", argv)
			}
		})
	}
}

// Where wt cannot be sure what gh will do, gh is asked. A fork's branch may go
// under the fork owner's name, and a finished pull request's branch may have
// moved on from the head GitHub recorded.
func TestPRCheckoutDoesNotSecondGuessWhatItCannotKnow(t *testing.T) {
	for name, change := range map[string]func(*github.PR){
		"a fork": func(pr *github.PR) {
			pr.IsCrossRepository = true
			pr.HeadRepositoryOwner = github.Account{Login: "stranger"}
		},
		"merged": func(pr *github.PR) { pr.State = "MERGED" },
	} {
		t.Run(name, func(t *testing.T) {
			main := testkit.CommittedRepo(t, testkit.MinimalConf)
			head := prHead(t, main, 12, "HEAD")
			localCommits(t, main, "residential_fixes", "HEAD", 2)
			ctx, err := kit.Open(main)
			if err != nil {
				t.Fatal(err)
			}
			pr := testkit.OpenPR(12, "residential_fixes", "Fixes")
			pr.HeadRefOid = head
			change(&pr)
			log := testkit.FakeGitHubWith(t,
				`[ "$1 $2" = 'pr checkout' ] && { git checkout -q residential_fixes; exit; }`, pr)

			var errs bytes.Buffer
			if _, err := PRCheckout(ctx, 12, PROptions{}, &errs); err != nil {
				t.Fatalf("PRCheckout: %v", err)
			}
			if argv := testkit.ArgvOf(t, log); argv[len(argv)-1] != "pr checkout 12" {
				t.Errorf("argv = %q, want gh to have done the checkout", argv)
			}
		})
	}
}

// A worktree that will not go is in the way of the retry, and the error says
// so rather than claiming a clean start. A locked worktree is one git refuses
// to remove on one --force.
func TestPRCheckoutSaysWhenTheHalfMadeWorktreeIsStillThere(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHubWith(t, `[ "$1 $2" = 'pr checkout' ] && { git worktree lock . ; echo 'fatal: no' >&2; exit 1; }`,
		testkit.OpenPR(12, "residential_fixes", "Fixes"))

	var errs bytes.Buffer
	_, err = PRCheckout(ctx, 12, PROptions{}, &errs)
	if err == nil {
		t.Fatal("PRCheckout succeeded with a gh that failed")
	}
	path := ctx.Scheme().Dir("feat", "pr-12-residential_fixes")
	for _, want := range []string{
		"gh pr checkout 12 failed: fatal: no\n",
		"  the half-made worktree at " + path + " could not be removed: ",
		"\n  remove it before a retry: git worktree remove --force --force " + path,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nwant it to say %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "was removed") {
		t.Errorf("err = %v, which claims a removal that did not happen", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the fixture did not keep the worktree: %v", err)
	}
	// The command the error names is one that works.
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "remove", "--force", "--force", path)
	if _, err := os.Stat(path); err == nil {
		t.Error("the advice did not remove the worktree")
	}
}
