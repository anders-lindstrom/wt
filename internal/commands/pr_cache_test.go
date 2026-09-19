package commands

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/github"
)

// listing is `wt list`'s stdout, which is the thing the cache must not change.
func listing(t *testing.T, ctx *Context, opts ListOptions) string {
	t.Helper()
	var out bytes.Buffer
	if err := List(ctx, opts, &out, 0); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// oneWorktreeWithAPR is a repository with a worktree on a pull request's head
// branch, and the fake gh's argv log.
func oneWorktreeWithAPR(t *testing.T) (*Context, string) {
	t.Helper()
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := fakeGitHub(t, openPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	return ctx, log
}

// The second listing starts no process: the first one's answer is kept in
// this repository.
func TestListCachesThePullRequests(t *testing.T) {
	ctx, log := oneWorktreeWithAPR(t)

	first := listing(t, ctx, ListOptions{})
	if !strings.Contains(first, "#12 open") {
		t.Fatalf("no pull request in the first listing:\n%s", first)
	}
	calls := len(argvOf(t, log))

	if second := listing(t, ctx, ListOptions{}); second != first {
		t.Errorf("the cached listing differs:\n%s\nwant:\n%s", second, first)
	}
	if got := argvOf(t, log); len(got) != calls {
		t.Errorf("the second listing ran gh again: %q", got)
	}
}

// --refresh is the way to a fresh answer without waiting the cache out.
func TestListRefreshAsksGitHubAgain(t *testing.T) {
	ctx, log := oneWorktreeWithAPR(t)
	listing(t, ctx, ListOptions{})
	calls := len(argvOf(t, log))

	listing(t, ctx, ListOptions{Refresh: true})
	if got := argvOf(t, log); len(got) != calls+1 {
		t.Errorf("--refresh ran %d calls, want one more than %d", len(got), calls)
	}
}

// A cache wt can neither read nor write costs the cache, never the listing:
// here the file's name is taken by a directory, so both fail.
func TestListSurvivesACacheItCannotUse(t *testing.T) {
	ctx, log := oneWorktreeWithAPR(t)
	want := listing(t, ctx, ListOptions{})

	path := prCachePath(ctx)
	if path == "" {
		t.Fatal("no cache path")
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := len(argvOf(t, log))

	if got := listing(t, ctx, ListOptions{}); got != want {
		t.Errorf("the listing changed:\n%s\nwant:\n%s", got, want)
	}
	if got := argvOf(t, log); len(got) != calls+1 {
		t.Errorf("gh was not asked: %q", got)
	}
}

// Half a file is not an answer.
func TestListSurvivesACorruptCache(t *testing.T) {
	ctx, log := oneWorktreeWithAPR(t)
	want := listing(t, ctx, ListOptions{})
	mustWrite(t, prCachePath(ctx), `{"prs": [{"number`)
	calls := len(argvOf(t, log))

	if got := listing(t, ctx, ListOptions{}); got != want {
		t.Errorf("the listing changed:\n%s\nwant:\n%s", got, want)
	}
	if got := argvOf(t, log); len(got) != calls+1 {
		t.Errorf("gh was not asked: %q", got)
	}
}

// What the cache is keyed on: the repository, the question, and its age. A
// miss on any of them is a fetch, never a wrong column.
func TestPRCacheMisses(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	gh := gitHub{Remote: ghRepo}
	o := listQuery(0)
	now := time.Now()
	prs := []github.PR{openPR(12, "fix_wt/login-crash", "Login crash")}
	writePRCache(ctx, gh, o, prs, now)

	if _, ok := readPRCache(ctx, gh, o, now.Add(PRCacheTTL/2)); !ok {
		t.Error("a young cache for the same question must be a hit")
	}
	for name, tc := range map[string]struct {
		gh   gitHub
		o    github.ListOptions
		when time.Time
	}{
		"another repository": {gitHub{Remote: github.Remote{Host: "github.com", Slug: "t/other"}}, o, now},
		"another host":       {gitHub{Remote: github.Remote{Host: "ghe.example", Slug: "t/demo"}}, o, now},
		"another question":   {gh, github.ListOptions{State: "open", Limit: listLimit}, now},
		"too old":            {gh, o, now.Add(PRCacheTTL + time.Second)},
		"a clock gone back":  {gh, o, now.Add(-time.Minute)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := readPRCache(ctx, tc.gh, tc.o, tc.when); ok {
				t.Error("want a miss")
			}
		})
	}
}
