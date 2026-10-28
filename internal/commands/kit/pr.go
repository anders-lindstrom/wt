package kit

import (
	"fmt"
	"slices"
	"time"

	"github.com/anders-lindstrom/wt/internal/github"
)

// PRLabel is a pull request in a column: its number and state, and for one
// merged somewhere other than trunk, where. A stacked pull request merged into
// its parent has landed nothing on trunk; the row should not read as if it had.
func PRLabel(pr github.PR, trunks []string) string {
	label := fmt.Sprintf("#%d %s", pr.Number, pr.StateLabel())
	if pr.Merged() && pr.BaseRefName != "" && !slices.Contains(trunks, pr.BaseRefName) {
		label += " into " + pr.BaseRefName
	}
	return label
}

// Trunks is every name this repository's trunk goes by: the configured main
// branch, and origin's own HEAD where that differs. A pull request merged into
// one of these reached trunk. Both reads are local.
func Trunks(ctx *Context) []string {
	names := []string{ctx.Config.MainBranch}
	// A HEAD git cannot read adds no name: fewer trunks only means fewer
	// pull requests read as merged.
	if head, ok, _ := ctx.Repo.OriginHead(); ok && head != ctx.Config.MainBranch {
		names = append(names, head)
	}
	return names
}

// PRLookup is how a command asks GitHub about its branches.
type PRLookup struct {
	// Warn names, in the stderr line a failed call prints, what the caller is
	// going on to do without an answer. Empty leaves the failure to the
	// caller, which is what a command reporting its own errors wants.
	Warn string
	// Refresh asks about every branch, however young the cached answer.
	Refresh bool
	// Deadline bounds the call; 0 leaves it to the package.
	Deadline time.Duration
	// Checks asks for what the checks on each pull request say too. Only the
	// PR column and wt status <work> show them.
	Checks bool
}

// PRAnswers is what a lookup came back with.
type PRAnswers struct {
	// ByBranch is the pull request on each branch that has one.
	ByBranch map[string]github.PR
	// Cached is when the oldest answer taken from the cache was read, and is
	// zero when every answer came from this run's call.
	Cached time.Time
	// Err is what a gh that was supposed to answer said. The branches the
	// cache could answer are in byBranch either way.
	Err error
}

// BranchPRs is the pull request on each of branches, keyed by branch and
// missing the ones that have none.
//
// GitHub is asked about exactly these branches, so a pull request of any age
// is found. Answers are cached per branch: one younger than PRCacheTTL costs
// nothing, and a branch the cache has never heard of is fetched with the rest
// rather than waiting the cache out.
//
// Failure is partial — whatever the cache held is still returned. An
// integration that does not apply here (off, no gh, no GitHub remote, no
// login) says nothing.
func BranchPRs(ctx *Context, branches []string, opts PRLookup) PRAnswers {
	gh, err := OpenGitHub(ctx)
	if err != nil {
		return PRAnswers{}
	}
	now := time.Now()
	cache := ReadPRCache(ctx, gh.Remote)
	out := PRAnswers{ByBranch: map[string]github.PR{}}
	var ask []string
	for _, b := range branches {
		if b == "" {
			continue
		}
		if !opts.Refresh {
			if pr, fetched, ok := cache.Fresh(b, now, opts.Checks); ok {
				if pr.Number > 0 {
					out.ByBranch[b] = pr
				}
				if out.Cached.IsZero() || fetched.Before(out.Cached) {
					out.Cached = fetched
				}
				continue
			}
		}
		ask = append(ask, b)
	}
	if len(ask) == 0 {
		return out
	}
	heads := github.HeadRefNames(ask, ctx.Config.MainBranch)
	var prs []github.PR
	var refused error
	if opts.Checks {
		prs, refused, err = gh.CLI.PRsOnBranchesWithChecks(ctx.Repo.MainRoot, gh.Remote, heads, opts.Deadline)
	} else {
		prs, err = gh.CLI.PRsOnBranches(ctx.Repo.MainRoot, gh.Remote, heads, opts.Deadline)
	}
	if err != nil {
		out.Err = err
		if opts.Warn != "" {
			warnGitHub(ctx, opts.Warn, err)
		}
		return out
	}
	if refused != nil && opts.Warn != "" {
		warnGitHub(ctx, "no checks shown", refused)
	}
	byBranch := github.ByBranch(prs, ctx.Config.MainBranch)
	answers := make(map[string]*github.PR, len(ask))
	for _, b := range ask {
		pr, ok := byBranch[b]
		if !ok {
			answers[b] = nil
			continue
		}
		answers[b] = &pr
		out.ByBranch[b] = pr
	}
	WritePRCacheChecks(ctx, gh.Remote, answers, now, opts.Checks)
	return out
}

// LandedPR is the pull request that took a branch at tip onto trunk, read from
// the cache and nowhere else.
//
// This is `wt remove`'s only view of GitHub: it runs from git hooks, so it
// starts no process and makes no network call. The cache answers it because a
// merge is permanent and the commit it carried never moves, so the TTL does
// not apply. A branch with no entry leaves remove as it was.
func LandedPR(ctx *Context, branch, tip string) int {
	if branch == "" || tip == "" || !ctx.UserConfig().GitHub {
		return 0
	}
	remote, ok := ctx.gitHubRemote()
	if !ok {
		return 0
	}
	pr, ok := ReadPRCache(ctx, remote).Merged(branch)
	if !ok || !pr.Landed(tip, Trunks(ctx)...) {
		return 0
	}
	return pr.Number
}

// warnGitHub reports a gh call that was supposed to work and did not: one
// line on stderr, the command's own output and exit code untouched. subject
// is what this command goes on to do without the answer, so the line says
// what was lost rather than what was asked.
//
// A gh holding no login stays silent: nothing pays for a login check up
// front, so the failure it gets back is that check, not a fault.
func warnGitHub(ctx *Context, subject string, err error) {
	if github.NotLoggedIn(err) {
		return
	}
	ctx.Warnf(WarnGitHub, "%s: %v", subject, err)
}

// PRFact is the pull request line of `wt status <work>`: number, checks,
// state, title.
// Empty when there is none, or when GitHub cannot be reached. It reads the
// cache `wt list` fills, so a listing already paid for it; refresh asks
// GitHub regardless.
func PRFact(ctx *Context, branch string, refresh bool) string {
	a := BranchPRs(ctx, []string{branch}, PRLookup{
		Warn: "no pull requests shown", Refresh: refresh, Deadline: ctx.GitHubDeadline(), Checks: true,
	})
	pr, ok := a.ByBranch[branch]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s · %s", PRCell(pr, Trunks(ctx)), pr.Title)
}
