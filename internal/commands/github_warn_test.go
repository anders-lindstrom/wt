package commands

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// An integration that was never set up here says nothing; one that was
// supposed to work and did not says so on stderr, with stdout unchanged.
func TestListWarnsOnlyWhenGitHubWasSupposedToWork(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	pr := testkit.OpenPR(12, "fix_wt/login-crash", "Login crash")
	// The listing without the column, to compare each case against: "PR"
	// appears in a work name as readily as in a header.
	ctx.User.GitHub = false
	var noColumn bytes.Buffer
	if err := List(ctx, ListOptions{}, &noColumn, 0); err != nil {
		t.Fatal(err)
	}
	ctx.User.GitHub = true

	for name, tc := range map[string]struct {
		set  func(t *testing.T, ctx *kit.Context)
		want string
	}{
		"gh failed": {
			func(t *testing.T, ctx *kit.Context) {
				testkit.FakeGitHubWith(t, ctx, `[ "$1 $2" = 'api graphql' ] && { echo "dial tcp: no route to host" >&2; exit 1; }`, pr)
			},
			"no pull requests shown",
		},
		"gh is not logged in": {
			func(t *testing.T, ctx *kit.Context) {
				testkit.FakeGitHubWith(t, ctx, `[ "$1 $2" = 'api graphql' ] && { echo "To get started with GitHub CLI, please run:  gh auth login" >&2; exit 1; }`, pr)
			},
			"",
		},
		"off in the user config": {
			func(t *testing.T, ctx *kit.Context) { testkit.FakeGitHub(t, ctx, pr); ctx.User.GitHub = false },
			"",
		},
		"no gh": {
			func(_ *testing.T, ctx *kit.Context) { testkit.StubGitHub(ctx, github.CLI{}, testkit.GhRepo, true) },
			"",
		},
		"not a GitHub repository": {
			func(_ *testing.T, ctx *kit.Context) {
				testkit.StubGitHub(ctx, github.CLI{Exe: "/nope/gh"}, github.Remote{}, false)
			},
			"",
		},
	} {
		t.Run(name, func(t *testing.T) {
			tc.set(t, ctx)
			defer func() { ctx.User.GitHub, ctx.GitHubFake = true, nil }()
			var warnings, out bytes.Buffer
			ctx.Warn, ctx.Warned = &warnings, nil
			if err := List(ctx, ListOptions{Refresh: true}, &out, 0); err != nil {
				t.Fatalf("wt list failed: %v", err)
			}
			if out.String() != noColumn.String() {
				t.Errorf("stdout carries the failure:\n%s\nwant:\n%s", out.String(), noColumn.String())
			}
			switch {
			case tc.want == "" && warnings.Len() > 0:
				t.Errorf("stderr = %q, want silence", warnings.String())
			case tc.want != "" && !strings.Contains(warnings.String(), tc.want):
				t.Errorf("stderr = %q, want %q", warnings.String(), tc.want)
			}
		})
	}
}

// A command that asks GitHub twice complains once.
func TestGitHubWarnsOncePerInvocation(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHubWith(t, ctx, `[ "$1 $2" = 'api graphql' ] && { echo offline >&2; exit 1; }`,
		testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))

	var warnings, out bytes.Buffer
	ctx.WarnTo(&warnings)
	if err := List(ctx, ListOptions{Refresh: true}, &out, 0); err != nil {
		t.Fatal(err)
	}
	if err := StatusWorktree(ctx, "login-crash", StatusOptions{Agents: []agents.Agent{}}, &out); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(warnings.String(), "no pull requests shown"); n != 1 {
		t.Errorf("warned %d times:\n%s", n, warnings.String())
	}

	// Both of them would have warned on their own; the count above is the
	// silencing, not one of the two staying quiet.
	var alone bytes.Buffer
	ctx.Warn, ctx.Warned = &alone, nil
	if err := StatusWorktree(ctx, "login-crash", StatusOptions{Agents: []agents.Agent{}}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(alone.String(), "no pull requests shown") {
		t.Errorf("wt status did not warn on its own: %q", alone.String())
	}
}
