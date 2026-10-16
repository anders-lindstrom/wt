package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// checkCells is the PR cell each of fourWorktreesWithChecks' branches reads.
var checkCells = map[string]string{ //nolint:gosec // G101: "passing" is a check state, not a password
	"fix_wt/passing":   "#12 ✓ open",
	"fix_wt/failing":   "#13 ✗ open",
	"fix_wt/running":   "#14 ● open",
	"fix_wt/unchecked": "#15 open",
}

// The PR column marks what each pull request's checks say after its number,
// and marks nothing for one with no checks.
func TestListMarksThePullRequestsChecks(t *testing.T) {
	ctx, _ := provisiontest.FourWorktreesWithChecks(t)
	out := listing(t, ctx, ListOptions{NoSessions: true})
	for branch, cell := range checkCells {
		if line := lineContaining(t, out, branch+" "); !strings.Contains(line, cell+" ") {
			t.Errorf("want %q on the row of %s:\n%s", cell, branch, out)
		}
	}
}

// wt status has wt list's PR column, marks and all, and wt status <work> the
// same cell on its pr line.
func TestStatusMarksThePullRequestsChecks(t *testing.T) {
	ctx, _ := provisiontest.FourWorktreesWithChecks(t)
	var buf bytes.Buffer
	if err := Status(ctx, StatusOptions{}, &buf, 0); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for branch, cell := range checkCells {
		if line := lineContaining(t, out, branch+" "); !strings.Contains(line, cell+" ") {
			t.Errorf("want %q on the row of %s:\n%s", cell, branch, out)
		}
	}

	buf.Reset()
	if err := StatusWorktree(ctx, "failing", noStatusSessions(), &buf); err != nil {
		t.Fatal(err)
	}
	if want := "  pr      #13 ✗ open · Failing\n"; !strings.Contains(buf.String(), want) {
		t.Errorf("want %q in:\n%s", want, buf.String())
	}
}

// The cache keeps the checks with the pull request, so a listing it answers
// marks them as the one that asked GitHub did.
func TestPRCacheKeepsTheChecks(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	failing := provisiontest.WithChecks(testkit.OpenPR(13, "fix_wt/failing", "Failing"), "FAILURE")
	kit.WritePRCacheChecks(ctx, testkit.GhRepo, map[string]*github.PR{"fix_wt/failing": &failing}, now, true)

	pr, _, ok := kit.ReadPRCache(ctx, testkit.GhRepo).Fresh("fix_wt/failing", now, true)
	if !ok || pr.ChecksLabel() != "failing" {
		t.Errorf("cached #13 = %+v, %v; want its checks failing", pr, ok)
	}
}

// wt status --json is defined from local state alone: checks the cache holds
// change nothing in it, and it asks GitHub nothing.
func TestStatusJSONLeavesTheChecksOut(t *testing.T) {
	ctx, log := provisiontest.FourWorktreesWithChecks(t)
	plan := func() string {
		var buf bytes.Buffer
		if err := synccmd.UpPlanJSON(ctx, "failing", &buf); err != nil {
			t.Fatal(err)
		}
		testkit.ValidateJSON(t, "status", buf.Bytes())
		return buf.String()
	}
	before := plan()
	listing(t, ctx, ListOptions{NoSessions: true})
	calls := len(testkit.ArgvOf(t, log))
	if calls == 0 {
		t.Fatal("the listing never asked GitHub, so the cache holds no checks")
	}

	if after := plan(); after != before {
		t.Errorf("cached checks changed wt status --json:\n%s\nwant:\n%s", after, before)
	}
	if got := testkit.ArgvOf(t, log); len(got) != calls {
		t.Errorf("wt status --json ran gh: %q", got[calls:])
	}
}

// The mark goes after the number whatever the label after it says: a stacked
// pull request merged elsewhere and one with changes requested have two words,
// and a pull request with no checks is left as prLabel has it.
func TestPRCellMarksAfterTheNumberOfAMultiWordLabel(t *testing.T) {
	merged := provisiontest.WithChecks(testkit.OpenPR(31, "feat_wt/x", "X"), "SUCCESS")
	merged.State, merged.BaseRefName = "MERGED", "feat_wt/parent"
	changes := provisiontest.WithChecks(testkit.OpenPR(32, "feat_wt/y", "Y"), "FAILURE")
	changes.ReviewDecision = "CHANGES_REQUESTED"
	unchecked := testkit.OpenPR(33, "feat_wt/z", "Z")
	unchecked.ReviewDecision = "CHANGES_REQUESTED"
	for name, tc := range map[string]struct {
		pr   github.PR
		want string
	}{
		"merged into":       {merged, "#31 ✓ merged into feat_wt/parent"},
		"changes requested": {changes, "#32 ✗ changes requested"},
		"no checks":         {unchecked, "#33 changes requested"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := kit.PRCell(tc.pr, []string{"main"}); got != tc.want {
				t.Errorf("prCell = %q, want %q", got, tc.want)
			}
		})
	}
}

// An answer read without the checks, by wt sweep or wt pr open, answers them
// but not a listing, which asks again rather than show no marks; a branch
// with no pull request needs no checks and answers both.
func TestPRCacheWithoutChecksDoesNotAnswerAListing(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pr := testkit.OpenPR(12, "fix_wt/login-crash", "Login crash")
	kit.WritePRCache(ctx, testkit.GhRepo, map[string]*github.PR{"fix_wt/login-crash": &pr, "fix_wt/none": nil}, now)

	c := kit.ReadPRCache(ctx, testkit.GhRepo)
	if _, _, ok := c.Fresh("fix_wt/login-crash", now, false); !ok {
		t.Error("a lookup without checks missed an answer read without them")
	}
	if _, _, ok := c.Fresh("fix_wt/login-crash", now, true); ok {
		t.Error("a listing took an answer that has no checks")
	}
	if _, _, ok := c.Fresh("fix_wt/none", now, true); !ok {
		t.Error("a listing asked again about a branch with no pull request")
	}
}

// Only the PR column asks for the checks: a lookup for anything else asks
// GitHub exactly what it asked before they existed.
func TestOnlyTheListingAsksForTheChecks(t *testing.T) {
	ctx, log := provisiontest.FourWorktreesWithChecks(t)
	kit.BranchPRs(ctx, []string{"fix_wt/failing"}, kit.PRLookup{Refresh: true})
	got := testkit.ArgvOf(t, log)
	if len(got) == 0 || strings.Contains(got[len(got)-1], "headCommit") {
		t.Fatalf("a lookup without checks asked for them: %q", got)
	}

	out := listing(t, ctx, ListOptions{NoSessions: true})
	if line := lineContaining(t, out, "fix_wt/failing "); !strings.Contains(line, "#13 ✗ open ") {
		t.Errorf("the listing took the answer without checks:\n%s", out)
	}
	if got := testkit.ArgvOf(t, log); !strings.Contains(got[len(got)-1], "headCommit") {
		t.Errorf("the listing did not ask for the checks: %q", got[len(got)-1])
	}
}

// A token GitHub will not show the checks to keeps the PR column, without
// marks, says once why, and does not ask again until the answer is old.
func TestListShowsThePullRequestsWhenTheChecksAreRefused(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/failing", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHubWith(t, `case "$*" in *headCommit*)
  echo '{"data":{"repository":{"b0":{"nodes":[{"number":13,"headCommit":null}]}}},"errors":[{"type":"FORBIDDEN","path":["repository","b0","nodes",0,"headCommit"],"message":"Resource not accessible by personal access token"}]}'
  echo 'gh: Resource not accessible by personal access token' >&2
  exit 1 ;;
esac`, provisiontest.WithChecks(testkit.OpenPR(13, "fix_wt/failing", "Failing"), "FAILURE"))

	var warnings bytes.Buffer
	ctx.WarnTo(&warnings)
	out := listing(t, ctx, ListOptions{NoSessions: true})
	if line := lineContaining(t, out, "fix_wt/failing "); !strings.Contains(line, "#13 open ") {
		t.Errorf("want #13 with no mark:\n%s", out)
	}
	if !strings.Contains(warnings.String(), "no checks shown") ||
		!strings.Contains(warnings.String(), "Resource not accessible") {
		t.Errorf("warnings = %q, want why the checks are missing", warnings.String())
	}
	calls := len(testkit.ArgvOf(t, log))
	listing(t, ctx, ListOptions{NoSessions: true})
	if got := testkit.ArgvOf(t, log); len(got) != calls {
		t.Errorf("the second listing asked again: %q", got[calls:])
	}
}

// When the checks are refused and the call without them fails too, the
// warning is the failure that left the column out, not the refusal.
func TestListSaysWhyTheColumnIsMissingWhenTheRetryFailsToo(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/failing", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHubWith(t, `case "$*" in
*headCommit*)
  echo '{"errors":[{"type":"FORBIDDEN","path":["repository","b0","nodes",0,"headCommit"],"message":"Resource not accessible by personal access token"}]}'
  echo 'gh: Resource not accessible by personal access token' >&2
  exit 1 ;;
*graphql*)
  echo 'gh: Something broke on the server' >&2
  exit 1 ;;
esac`, provisiontest.WithChecks(testkit.OpenPR(13, "fix_wt/failing", "Failing"), "FAILURE"))

	var warnings bytes.Buffer
	ctx.WarnTo(&warnings)
	listing(t, ctx, ListOptions{NoSessions: true})
	if !strings.Contains(warnings.String(), "no pull requests shown") ||
		strings.Contains(warnings.String(), "no checks shown") {
		t.Errorf("warnings = %q, want the retry's failure", warnings.String())
	}
}
