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

// What the cache is keyed on: the repository gh resolves, the shape of the
// answers, and their age. A miss on any of them is a fetch, never a wrong
// column.
func TestPRCacheMisses(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pr := openPR(12, "fix_wt/login-crash", "Login crash")
	writePRCache(ctx, ghRepo, map[string]*github.PR{"fix_wt/login-crash": &pr}, now)

	if _, _, ok := readPRCache(ctx, ghRepo).fresh("fix_wt/login-crash", now.Add(PRCacheTTL/2)); !ok {
		t.Error("a young entry for the same repository must be a hit")
	}
	for name, tc := range map[string]struct {
		remote github.Remote
		branch string
		when   time.Time
	}{
		"another repository":             {github.Remote{Host: "github.com", Slug: "t/other"}, "fix_wt/login-crash", now},
		"another host":                   {github.Remote{Host: "ghe.example", Slug: "t/demo"}, "fix_wt/login-crash", now},
		"a branch it has never heard of": {ghRepo, "feat_wt/brand-new", now},
		"too old":                        {ghRepo, "fix_wt/login-crash", now.Add(PRCacheTTL + time.Second)},
		"a clock gone back":              {ghRepo, "fix_wt/login-crash", now.Add(-time.Minute)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := readPRCache(ctx, tc.remote).fresh(tc.branch, tc.when); ok {
				t.Error("want a miss")
			}
		})
	}

	// A file written by a wt that asked GitHub something else is not an
	// answer to this wt's question.
	mustWrite(t, prCachePath(ctx), `{"version":0,"host":"github.com","slug":"t/demo",`+
		`"branches":{"fix_wt/login-crash":{"fetched":"`+now.Format(time.RFC3339)+`","pr":{"number":99}}}}`)
	if _, _, ok := readPRCache(ctx, ghRepo).fresh("fix_wt/login-crash", now); ok {
		t.Error("a cache of another version was read as an answer")
	}
}

// A branch that has no pull request is an answer worth keeping: without it
// every listing would ask again about every branch that has none.
func TestPRCacheRemembersThatThereIsNone(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	writePRCache(ctx, ghRepo, map[string]*github.PR{"feat_wt/alone": nil}, now)
	pr, _, ok := readPRCache(ctx, ghRepo).fresh("feat_wt/alone", now)
	if !ok {
		t.Fatal("a fetched answer of \"none\" was not remembered")
	}
	if pr.Number != 0 {
		t.Errorf("fresh() = %+v, want nothing", pr)
	}
}

// One branch's fetch must not throw away what is known about the others: a
// `wt pr open` refreshing its own branch would otherwise cost `wt list` the
// whole column.
func TestPRCacheWriteKeepsTheOtherBranches(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first := openPR(12, "fix_wt/one", "One")
	writePRCache(ctx, ghRepo, map[string]*github.PR{"fix_wt/one": &first}, now)
	second := openPR(13, "fix_wt/two", "Two")
	writePRCache(ctx, ghRepo, map[string]*github.PR{"fix_wt/two": &second}, now)

	c := readPRCache(ctx, ghRepo)
	for branch, want := range map[string]int{"fix_wt/one": 12, "fix_wt/two": 13} {
		if pr, _, ok := c.fresh(branch, now); !ok || pr.Number != want {
			t.Errorf("%s = %+v, %v; want #%d", branch, pr, ok, want)
		}
	}
}

// An entry nothing has asked about for a month goes, so a repository's dead
// branches cannot grow the file for ever.
func TestPRCacheForgetsTheLongDead(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := openPR(1, "fix_wt/ancient", "Ancient")
	writePRCache(ctx, ghRepo, map[string]*github.PR{"fix_wt/ancient": &old},
		now.Add(-prCacheRetention-time.Hour))
	fresh := openPR(2, "fix_wt/today", "Today")
	writePRCache(ctx, ghRepo, map[string]*github.PR{"fix_wt/today": &fresh}, now)

	c := readPRCache(ctx, ghRepo)
	if _, ok := c.Branches["fix_wt/ancient"]; ok {
		t.Error("a month-old entry was kept")
	}
	if _, ok := c.Branches["fix_wt/today"]; !ok {
		t.Error("today's entry went")
	}
}

// The merged answer outlives the TTL, because there is nothing left in it
// that can change. This is what `wt remove` reads.
func TestPRCacheMergedOutlivesTheTTL(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-30 * PRCacheTTL)
	merged := github.PR{Number: 34, HeadRefName: "fix_wt/done", BaseRefName: "main",
		HeadRefOid: "abc123", State: "MERGED"}
	open := openPR(35, "fix_wt/doing", "Doing")
	writePRCache(ctx, ghRepo, map[string]*github.PR{
		"fix_wt/done": &merged, "fix_wt/doing": &open}, long)

	c := readPRCache(ctx, ghRepo)
	if _, _, ok := c.fresh("fix_wt/done", time.Now()); ok {
		t.Error("an old entry is not fresh, merged or not")
	}
	got, ok := c.merged("fix_wt/done")
	if !ok || got.Number != 34 {
		t.Errorf("merged() = %+v, %v; want #34 whatever its age", got, ok)
	}
	if _, ok := c.merged("fix_wt/doing"); ok {
		t.Error("an open pull request was read as merged")
	}
}
