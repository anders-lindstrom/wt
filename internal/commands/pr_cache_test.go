package commands

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// listing is `wt list`'s stdout, which is the thing the cache must not change.
func listing(t *testing.T, ctx *kit.Context, opts ListOptions) string {
	t.Helper()
	var out bytes.Buffer
	if err := List(ctx, opts, &out, 0); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The second listing starts no process: the first one's answer is kept in
// this repository.
func TestListCachesThePullRequests(t *testing.T) {
	ctx, log := provisiontest.OneWorktreeWithAPR(t)

	first := listing(t, ctx, ListOptions{})
	if !strings.Contains(first, "#12 open") {
		t.Fatalf("no pull request in the first listing:\n%s", first)
	}
	calls := len(testkit.ArgvOf(t, log))

	if second := listing(t, ctx, ListOptions{}); second != first {
		t.Errorf("the cached listing differs:\n%s\nwant:\n%s", second, first)
	}
	if got := testkit.ArgvOf(t, log); len(got) != calls {
		t.Errorf("the second listing ran gh again: %q", got)
	}
}

// --refresh is the way to a fresh answer without waiting the cache out.
func TestListRefreshAsksGitHubAgain(t *testing.T) {
	ctx, log := provisiontest.OneWorktreeWithAPR(t)
	listing(t, ctx, ListOptions{})
	calls := len(testkit.ArgvOf(t, log))

	listing(t, ctx, ListOptions{Refresh: true})
	if got := testkit.ArgvOf(t, log); len(got) != calls+1 {
		t.Errorf("--refresh ran %d calls, want one more than %d", len(got), calls)
	}
}

// A cache wt can neither read nor write costs the cache, never the listing:
// here the file's name is taken by a directory, so both fail.
func TestListSurvivesACacheItCannotUse(t *testing.T) {
	ctx, log := provisiontest.OneWorktreeWithAPR(t)
	want := listing(t, ctx, ListOptions{})

	path := kit.PRCachePath(ctx)
	if path == "" {
		t.Fatal("no cache path")
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := len(testkit.ArgvOf(t, log))

	if got := listing(t, ctx, ListOptions{}); got != want {
		t.Errorf("the listing changed:\n%s\nwant:\n%s", got, want)
	}
	if got := testkit.ArgvOf(t, log); len(got) != calls+1 {
		t.Errorf("gh was not asked: %q", got)
	}
}

// Half a file is not an answer.
func TestListSurvivesACorruptCache(t *testing.T) {
	ctx, log := provisiontest.OneWorktreeWithAPR(t)
	want := listing(t, ctx, ListOptions{})
	testkit.MustWrite(t, kit.PRCachePath(ctx), `{"prs": [{"number`)
	calls := len(testkit.ArgvOf(t, log))

	if got := listing(t, ctx, ListOptions{}); got != want {
		t.Errorf("the listing changed:\n%s\nwant:\n%s", got, want)
	}
	if got := testkit.ArgvOf(t, log); len(got) != calls+1 {
		t.Errorf("gh was not asked: %q", got)
	}
}

// What the cache is keyed on: the repository gh resolves, the shape of the
// answers, and their age. A miss on any of them is a fetch, never a wrong
// column.
func TestPRCacheMisses(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pr := testkit.OpenPR(12, "fix_wt/login-crash", "Login crash")
	kit.WritePRCache(ctx, testkit.GhRepo, map[string]*github.PR{"fix_wt/login-crash": &pr}, now)

	if _, _, ok := kit.ReadPRCache(ctx, testkit.GhRepo).Fresh("fix_wt/login-crash", now.Add(kit.PRCacheTTL/2), false); !ok {
		t.Error("a young entry for the same repository must be a hit")
	}
	for name, tc := range map[string]struct {
		remote github.Remote
		branch string
		when   time.Time
	}{
		"another repository":             {github.Remote{Host: "github.com", Slug: "t/other"}, "fix_wt/login-crash", now},
		"another host":                   {github.Remote{Host: "ghe.example", Slug: "t/demo"}, "fix_wt/login-crash", now},
		"a branch it has never heard of": {testkit.GhRepo, "feat_wt/brand-new", now},
		"too old":                        {testkit.GhRepo, "fix_wt/login-crash", now.Add(kit.PRCacheTTL + time.Second)},
		"a clock gone back":              {testkit.GhRepo, "fix_wt/login-crash", now.Add(-time.Minute)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := kit.ReadPRCache(ctx, tc.remote).Fresh(tc.branch, tc.when, false); ok {
				t.Error("want a miss")
			}
		})
	}

	// A file written by a wt that asked GitHub something else is not an
	// answer to this wt's question.
	testkit.MustWrite(t, kit.PRCachePath(ctx), `{"version":0,"host":"github.com","slug":"t/demo",`+
		`"branches":{"fix_wt/login-crash":{"fetched":"`+now.Format(time.RFC3339)+`","pr":{"number":99}}}}`)
	if _, _, ok := kit.ReadPRCache(ctx, testkit.GhRepo).Fresh("fix_wt/login-crash", now, false); ok {
		t.Error("a cache of another version was read as an answer")
	}
}

// A branch that has no pull request is an answer worth keeping: without it
// every listing would ask again about every branch that has none.
func TestPRCacheRemembersThatThereIsNone(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	kit.WritePRCache(ctx, testkit.GhRepo, map[string]*github.PR{"feat_wt/alone": nil}, now)
	pr, _, ok := kit.ReadPRCache(ctx, testkit.GhRepo).Fresh("feat_wt/alone", now, false)
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
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first := testkit.OpenPR(12, "fix_wt/one", "One")
	kit.WritePRCache(ctx, testkit.GhRepo, map[string]*github.PR{"fix_wt/one": &first}, now)
	second := testkit.OpenPR(13, "fix_wt/two", "Two")
	kit.WritePRCache(ctx, testkit.GhRepo, map[string]*github.PR{"fix_wt/two": &second}, now)

	c := kit.ReadPRCache(ctx, testkit.GhRepo)
	for branch, want := range map[string]int{"fix_wt/one": 12, "fix_wt/two": 13} {
		if pr, _, ok := c.Fresh(branch, now, false); !ok || pr.Number != want {
			t.Errorf("%s = %+v, %v; want #%d", branch, pr, ok, want)
		}
	}
}

// An entry nothing has asked about for a month goes, so a repository's dead
// branches cannot grow the file for ever.
func TestPRCacheForgetsTheLongDead(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := testkit.OpenPR(1, "fix_wt/ancient", "Ancient")
	kit.WritePRCache(ctx, testkit.GhRepo, map[string]*github.PR{"fix_wt/ancient": &old},
		now.Add(-kit.PRCacheRetention-time.Hour))
	fresh := testkit.OpenPR(2, "fix_wt/today", "Today")
	kit.WritePRCache(ctx, testkit.GhRepo, map[string]*github.PR{"fix_wt/today": &fresh}, now)

	c := kit.ReadPRCache(ctx, testkit.GhRepo)
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
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-30 * kit.PRCacheTTL)
	merged := github.PR{Number: 34, HeadRefName: "fix_wt/done", BaseRefName: "main",
		HeadRefOid: "abc123", State: "MERGED"}
	open := testkit.OpenPR(35, "fix_wt/doing", "Doing")
	kit.WritePRCache(ctx, testkit.GhRepo, map[string]*github.PR{
		"fix_wt/done": &merged, "fix_wt/doing": &open}, long)

	c := kit.ReadPRCache(ctx, testkit.GhRepo)
	if _, _, ok := c.Fresh("fix_wt/done", time.Now(), false); ok {
		t.Error("an old entry is not fresh, merged or not")
	}
	got, ok := c.Merged("fix_wt/done")
	if !ok || got.Number != 34 {
		t.Errorf("merged() = %+v, %v; want #34 whatever its age", got, ok)
	}
	if _, ok := c.Merged("fix_wt/doing"); ok {
		t.Error("an open pull request was read as merged")
	}
}
