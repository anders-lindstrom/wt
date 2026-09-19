package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// ghRepo is the remote every test in this file pretends the fixture has.
var ghRepo = github.Remote{Name: "origin", Host: "github.com", Slug: "t/demo"}

// fakeGitHub writes a `gh` that answers `pr list` and `pr view` with prs, and
// performs `pr checkout <n>` by creating that pull request's branch where it
// is run — which is what the real one does, and the only part of it wt
// depends on. It points findGitHub and gitHubRemote at the fake for one test
// and returns the file every invocation's argv is appended to.
func fakeGitHub(t *testing.T, prs ...github.PR) string {
	t.Helper()
	return fakeGitHubWith(t, "", prs...)
}

// fakeGitHubWith is fakeGitHub with extra shell run before the answers, for
// the tests that need gh to fail.
func fakeGitHubWith(t *testing.T, prelude string, prs ...github.PR) string {
	t.Helper()
	listed, err := json.Marshal(prs)
	if err != nil {
		t.Fatal(err)
	}
	branches := map[int]string{}
	for _, pr := range prs {
		branches[pr.Number] = pr.LocalBranch("main")
	}
	var cases strings.Builder
	for number, branch := range branches {
		fmt.Fprintf(&cases, "    %d) git checkout -q -b %q ;;\n", number, branch)
	}

	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	exe := filepath.Join(dir, "gh")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + log + "\n" +
		prelude + "\n" +
		"case \"$1 $2\" in\n" +
		"  'pr list') cat <<'JSON'\n" + string(listed) + "\nJSON\n    ;;\n" +
		"  'pr view') " + viewCase(t, prs) + " ;;\n" +
		"  'pr checkout') case \"$3\" in\n" + cases.String() + "    *) exit 1 ;;\n  esac ;;\n" +
		"  'auth status') exit 0 ;;\n" +
		"  '--version ') echo 'gh version 2.100.0 (2026-09-03)' ;;\n" +
		"esac\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stubGitHub(t, github.CLI{Exe: exe}, ghRepo, true)
	return log
}

// viewCase answers `pr view <n>` for each pull request by number.
func viewCase(t *testing.T, prs []github.PR) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("case \"$3\" in\n")
	for _, pr := range prs {
		one, err := json.Marshal(pr)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "    %d) cat <<'JSON'\n%s\nJSON\n    ;;\n", pr.Number, one)
	}
	b.WriteString("    *) echo 'no pull requests found' >&2; exit 1 ;;\n  esac")
	return b.String()
}

// stubGitHub replaces the located CLI and the repository's remote for one
// test. found false is a machine with no gh; hasRemote false a repository
// that is not on GitHub.
func stubGitHub(t *testing.T, cli github.CLI, remote github.Remote, hasRemote bool) {
	t.Helper()
	oldFind, oldRemote := findGitHub, gitHubRemote
	found := cli.Exe != ""
	findGitHub = func() (github.CLI, bool) { return cli, found }
	gitHubRemote = func(string) (github.Remote, bool) { return remote, hasRemote }
	t.Cleanup(func() { findGitHub, gitHubRemote = oldFind, oldRemote })
}

func openPR(number int, head, title string) github.PR {
	return github.PR{Number: number, Title: title, HeadRefName: head, State: "OPEN",
		Author: github.Account{Login: "anders"}}
}

// Each gate says which one it was and what to do about it. This is the
// difference between `wt pr` and `wt list`: here an inactive integration is
// an error a person can act on.
func TestGitHubGateNamesTheGateThatFailed(t *testing.T) {
	for name, tc := range map[string]struct {
		set  func(t *testing.T, ctx *Context)
		want string
	}{
		"off in the user config": {
			func(_ *testing.T, ctx *Context) { ctx.User.GitHub = false },
			"GitHub is off in your wt config; turn it on with `wt config set github true`",
		},
		"no gh": {
			func(t *testing.T, _ *Context) { stubGitHub(t, github.CLI{}, ghRepo, true) },
			"gh is not on your PATH",
		},
		"not a GitHub repository": {
			func(t *testing.T, _ *Context) { stubGitHub(t, github.CLI{Exe: "/nope/gh"}, github.Remote{}, false) },
			"has no GitHub remote",
		},
		"not logged in": {
			func(t *testing.T, _ *Context) {
				fakeGitHubWith(t, `[ "$1 $2" = 'auth status' ] && { echo 'not logged in' >&2; exit 1; }`)
			},
			"gh is not logged in to github.com; run `gh auth login --hostname github.com`",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := Open(committedRepo(t, minimalConf))
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
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := fakeGitHub(t, openPR(12, "residential_fixes", "Residents no longer rewrite a door"))

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
	// The login is checked, the pull request is read by number and gh does
	// the checkout — and nothing lists them, because a number means straight
	// to it.
	argv := argvOf(t, log)
	if len(argv) != 3 || !strings.HasPrefix(argv[0], "auth status") ||
		!strings.HasPrefix(argv[1], "pr view 12 ") || argv[2] != "pr checkout 12" {
		t.Errorf("argv = %q", argv)
	}
}

// A head branch that already follows this repository's convention keeps its
// own name, so a branch wt made lands where `wt new` would have put it.
func TestPRCheckoutKeepsAWtBranchsOwnName(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	fakeGitHub(t, openPR(7, "fix_wt/login-crash", "Login crash"))

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
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	fakeGitHub(t, openPR(7, "fix_wt/login-crash", "Login crash"))
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
	main := committedRepo(t, minimalConf)
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "add", "README.md")
	gitIn(t, main, "commit", "-qm", "readme")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	fakeGitHubWith(t, `[ "$1 $2" = 'pr checkout' ] && { echo 'fetch failed' >&2; exit 1; }`,
		openPR(12, "residential_fixes", "Fixes"))

	var errs bytes.Buffer
	if _, err := PRCheckout(ctx, 12, PROptions{}, &errs); err == nil {
		t.Fatal("PRCheckout succeeded with a gh that failed")
	}
	path := ctx.Scheme().Dir("feat", "pr-12-residential_fixes")
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
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := fakeGitHub(t,
		openPR(12, "residential_fixes", "Fixes"),
		openPR(7, "fix_wt/login-crash", "Login crash"))

	var offered []github.PR
	opts := PROptions{Choose: func(prs []github.PR) (github.PR, error) {
		offered = prs
		return prs[1], nil
	}}
	var errs bytes.Buffer
	path, err := PRCheckout(ctx, 0, opts, &errs)
	if err != nil {
		t.Fatal(err)
	}
	if len(offered) != 2 || offered[0].Number != 12 {
		t.Errorf("the picker was offered %+v", offered)
	}
	if want := ctx.Scheme().Dir("fix", "login-crash"); path != want {
		t.Errorf("path = %q, want the one that was picked (%q)", path, want)
	}
	if got := argvOf(t, log)[1]; !strings.HasPrefix(got, "pr list --state open") {
		t.Errorf("the listing was %q, want the open ones", got)
	}
}

// With nobody to ask, `wt pr checkout` says so rather than choosing.
func TestPRCheckoutWithoutANumberOrAPickerRefuses(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	fakeGitHub(t, openPR(12, "x", "X"))
	var errs bytes.Buffer
	if _, err := PRCheckout(ctx, 0, PROptions{}, &errs); err == nil {
		t.Fatal("PRCheckout chose for itself")
	}
}

// The PR column of `wt list`: a worktree whose branch is a pull request's
// head gets its number and state, and everything else gets a dash.
func TestListShowsThePullRequestColumn(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	fakeGitHub(t, openPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	if _, err := New(ctx, "feat/api-tidy", NewOptions{}, &errs); err != nil {
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
func TestListIsUnchangedWhenGitHubIsNotInPlay(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	var before bytes.Buffer
	if err := List(ctx, ListOptions{}, &before, 0); err != nil {
		t.Fatal(err)
	}

	for name, set := range map[string]func(t *testing.T, ctx *Context){
		"off in the user config": func(t *testing.T, ctx *Context) {
			fakeGitHub(t, openPR(12, "fix_wt/login-crash", "x"))
			ctx.User.GitHub = false
		},
		"no gh":      func(t *testing.T, _ *Context) { stubGitHub(t, github.CLI{}, ghRepo, true) },
		"not GitHub": func(t *testing.T, _ *Context) { stubGitHub(t, github.CLI{Exe: "/nope/gh"}, github.Remote{}, false) },
		"gh fails": func(t *testing.T, _ *Context) {
			fakeGitHubWith(t, `[ "$1 $2" = 'pr list' ] && { echo offline >&2; exit 1; }`,
				openPR(12, "fix_wt/login-crash", "x"))
		},
		"no pull request for any worktree": func(t *testing.T, _ *Context) {
			fakeGitHub(t, openPR(12, "somebody-elses-branch", "x"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			set(t, ctx)
			defer func() { ctx.User.GitHub = true }()
			var out bytes.Buffer
			if err := List(ctx, ListOptions{}, &out, 0); err != nil {
				t.Fatal(err)
			}
			if out.String() != before.String() {
				t.Errorf("wt list changed:\n%s\nwant:\n%s", out.String(), before.String())
			}
		})
	}
}

// doctor's GitHub section: what it found, and never a problem — a machine
// without gh is an ordinary machine.
func TestDoctorReportsGitHubState(t *testing.T) {
	for name, tc := range map[string]struct {
		set  func(t *testing.T, ctx *Context)
		want string
	}{
		"off": {
			func(_ *testing.T, ctx *Context) { ctx.User.GitHub = false },
			"  - off in your wt config; turn it on with `wt config set github true`",
		},
		"no gh": {
			func(t *testing.T, _ *Context) { stubGitHub(t, github.CLI{}, ghRepo, true) },
			"  - no gh on the PATH",
		},
		"not a GitHub repository": {
			func(t *testing.T, _ *Context) { stubGitHub(t, github.CLI{Exe: "/nope/gh"}, github.Remote{}, false) },
			"  - no GitHub remote for",
		},
		"not logged in": {
			func(t *testing.T, _ *Context) {
				fakeGitHubWith(t, `[ "$1 $2" = 'auth status' ] && { echo no >&2; exit 1; }`)
			},
			"which gh is not logged in to; run `gh auth login --hostname github.com`",
		},
		"usable": {
			func(t *testing.T, _ *Context) { fakeGitHub(t) },
			"✓ t/demo on github.com — `wt pr` reads its pull requests",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := Open(committedRepo(t, minimalConf))
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
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := fakeGitHub(t,
		openPR(12, "fix_wt/login-crash", "Login crash"),
		openPR(13, "nobody-is-on-this", "Something else"))
	var errs bytes.Buffer
	path, err := New(ctx, "fix/login-crash", NewOptions{}, &errs)
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
	if got := argvOf(t, log)[1]; !strings.Contains(got, "statusCheckRollup") {
		t.Errorf("wt pr list did not ask for the checks: %q", got)
	}
}

func TestPRListSaysWhenThereAreNone(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	fakeGitHub(t)
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
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		pr             github.PR
		wantType, want string
	}{
		"a branch of this repository": {
			openPR(7, "fix_wt/login-crash", "x"), "fix", "login-crash",
		},
		"a type wt does not know is not one": {
			openPR(7, "banana_wt/thing", "x"), "feat", "pr-7-thing",
		},
		"somebody else's branch": {
			openPR(12, "residential_fixes", "x"), "feat", "pr-12-residential_fixes",
		},
		"a type in a path prefix": {
			openPR(12, "fix/login", "x"), "fix", "pr-12-fix-login",
		},
		"a type in a name prefix": {
			openPR(12, "fix-login", "x"), "fix", "pr-12-fix-login",
		},
		"a long branch is cut at a dash": {
			openPR(205, "dependabot/github_actions/actions/checkout-7", "x"),
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

// wt status of one worktree carries its pull request, when it has one and
// GitHub answers. One bounded call, restricted to that branch.
func TestStatusWorktreeCarriesThePullRequest(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := fakeGitHub(t, openPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := StatusWorktree(ctx, "login-crash", StatusOptions{Agents: []wtsync.Agent{}}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pr      #12 open · Login crash") {
		t.Errorf("wt status said\n%s", out.String())
	}
	if got := argvOf(t, log); len(got) != 1 || !strings.Contains(got[0], "--head fix_wt/login-crash") {
		t.Errorf("argv = %q, want one call for that branch alone", got)
	}
}

// A worktree with no pull request, and a GitHub that cannot be reached, both
// leave wt status with no pr line at all.
func TestStatusWorktreeIsSilentWithoutOne(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	for name, set := range map[string]func(t *testing.T){
		"no pull request": func(t *testing.T) { fakeGitHub(t) },
		"no gh":           func(t *testing.T) { stubGitHub(t, github.CLI{}, ghRepo, true) },
		"gh fails": func(t *testing.T) {
			fakeGitHubWith(t, `[ "$1 $2" = 'pr list' ] && { echo offline >&2; exit 1; }`)
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
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := fakeGitHub(t, openPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := List(ctx, ListOptions{NoPR: true}, &out, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "#12") {
		t.Errorf("--no-pr still printed the column:\n%s", out.String())
	}
	if got := argvOf(t, log); got != nil {
		t.Errorf("gh was run: %q", got)
	}
}

// GitHub deletes a head branch when the pull request is merged, and `gh pr
// checkout` fetches that branch by name — so a merged pull request is picked
// up from refs/pull/<n>/head instead. An open one gets no such second chance.
func TestPRCheckoutOfAMergedPullRequestUsesThePullRef(t *testing.T) {
	main := committedRepo(t, minimalConf)
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "add", "README.md")
	gitIn(t, main, "commit", "-qm", "readme")
	// The repository is its own remote, holding the pull request's head where
	// GitHub keeps it once the branch is gone.
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "update-ref", "refs/pull/34/head", "HEAD")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	merged := github.PR{Number: 34, Title: "Gone", HeadRefName: "fix_wt/ses-endpoint", State: "MERGED"}
	fakeGitHubWith(t, `[ "$1 $2" = 'pr checkout' ] && { echo "couldn't find remote ref" >&2; exit 1; }`, merged)

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
