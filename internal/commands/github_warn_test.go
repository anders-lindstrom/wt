package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// An integration that was never set up here says nothing; one that was
// supposed to work and did not says so on stderr, with stdout unchanged.
func TestListWarnsOnlyWhenGitHubWasSupposedToWork(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	pr := openPR(12, "fix_wt/login-crash", "Login crash")

	for name, tc := range map[string]struct {
		set  func(t *testing.T, ctx *Context)
		want string
	}{
		"gh failed": {
			func(t *testing.T, _ *Context) {
				fakeGitHubWith(t, `[ "$1 $2" = 'pr list' ] && { echo "dial tcp: no route to host" >&2; exit 1; }`, pr)
			},
			"no pull requests shown",
		},
		"gh is not logged in": {
			func(t *testing.T, _ *Context) {
				fakeGitHubWith(t, `[ "$1 $2" = 'pr list' ] && { echo "To get started with GitHub CLI, please run:  gh auth login" >&2; exit 1; }`, pr)
			},
			"",
		},
		"off in the user config": {
			func(t *testing.T, ctx *Context) { fakeGitHub(t, pr); ctx.User.GitHub = false },
			"",
		},
		"no gh": {
			func(t *testing.T, _ *Context) { stubGitHub(t, github.CLI{}, ghRepo, true) },
			"",
		},
		"not a GitHub repository": {
			func(t *testing.T, _ *Context) { stubGitHub(t, github.CLI{Exe: "/nope/gh"}, github.Remote{}, false) },
			"",
		},
	} {
		t.Run(name, func(t *testing.T) {
			tc.set(t, ctx)
			defer func() { ctx.User.GitHub = true }()
			var warnings, out bytes.Buffer
			ctx.warn, ctx.warned = &warnings, nil
			if err := List(ctx, ListOptions{Refresh: true}, &out, 0); err != nil {
				t.Fatalf("wt list failed: %v", err)
			}
			if strings.Contains(out.String(), "PR") || strings.Contains(out.String(), "wt:") {
				t.Errorf("stdout carries the failure:\n%s", out.String())
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
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	fakeGitHubWith(t, `[ "$1 $2" = 'pr list' ] && { echo offline >&2; exit 1; }`,
		openPR(12, "fix_wt/login-crash", "Login crash"))

	var warnings, out bytes.Buffer
	ctx.WarnTo(&warnings)
	if err := List(ctx, ListOptions{Refresh: true}, &out, 0); err != nil {
		t.Fatal(err)
	}
	if err := StatusWorktree(ctx, "login-crash", StatusOptions{Agents: []wtsync.Agent{}}, &out); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(warnings.String(), "no pull requests shown"); n != 1 {
		t.Errorf("warned %d times:\n%s", n, warnings.String())
	}

	// Both of them would have warned on their own; the count above is the
	// silencing, not one of the two staying quiet.
	var alone bytes.Buffer
	ctx.warn, ctx.warned = &alone, nil
	if err := StatusWorktree(ctx, "login-crash", StatusOptions{Agents: []wtsync.Agent{}}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(alone.String(), "no pull requests shown") {
		t.Errorf("wt status did not warn on its own: %q", alone.String())
	}
}
