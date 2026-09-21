package commands

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// fullLimit is how many open pull requests the two wide views ask for,
// `wt pr list` and the checkout picker. 100 is one page; every page past the
// first is another round trip. Nothing on a deadline uses it: `wt list`,
// `wt status` and `wt sweep` ask about their own branches by name.
const fullLimit = 100

// titleWidth truncates a pull request title so the worktree column still
// fits beside it.
const titleWidth = 48

// PROptions controls `wt pr checkout`.
type PROptions struct {
	NewOptions
	// Choose picks one of the open pull requests when the caller named no
	// number. It is only ever called with a non-empty list. Nil means there
	// is nobody to ask.
	Choose func([]PRChoice) (github.PR, error)
}

// PRCheckout puts a worktree on a pull request's own head branch, set up by
// `gh pr checkout`, so a push from it updates the pull request — from a fork
// too, where the branch's remote has to be the fork's URL.
//
// It goes through the same tail as `wt new`, so provisioning and any other
// integration happen by their own rules.
func PRCheckout(ctx *Context, number int, opts PROptions, w io.Writer) (string, error) {
	gh, err := openGitHub(ctx)
	if err != nil {
		return "", err
	}
	pr, err := resolvePR(ctx, gh, number, opts.Choose)
	if err != nil {
		return "", err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return "", err
	}
	// git gives a branch one worktree, and the one that exists is the answer
	// anyway — unless it is the main checkout. A pull request whose head
	// branch is trunk lands there, and answering with that path would have
	// `cd "$(wt pr checkout 12)"` walk you into where you started.
	for _, branch := range pr.Branches(ctx.Config.MainBranch) {
		wt, ok := worktrees.ByBranch(branch)
		if !ok {
			continue
		}
		if wt.IsMain {
			return "", fmt.Errorf("#%d is on %s, which the main checkout at %s is using; "+
				"git gives a branch only one worktree", pr.Number, branch, wt.Path)
		}
		fmt.Fprintf(w, "#%d is already checked out at %s, on %s\n", pr.Number, wt.Path, branch)
		return wt.Path, nil
	}
	typ, work := prWorkName(ctx, pr)
	path := ctx.Scheme().Dir(typ, work)
	return addAndProvision(ctx, path, func() error {
		fmt.Fprintf(w, "Checking out #%d %s at %s\n", pr.Number, pr.HeadRefName, path)
		return checkoutPRInto(ctx, gh, pr, path, w)
	}, opts.NewOptions, w)
}

// PRChoice is one row of the picker: a pull request, whether GitHub is
// waiting on this person to review it, and the worktree here already on its
// branch, if any.
type PRChoice struct {
	PR              github.PR
	ReviewRequested bool
	Worktree        string
}

// resolvePR is the pull request to check out: the one named, whatever state
// it is in, or one chosen from the open ones. A merged pull request can be
// named but is never offered in the list.
func resolvePR(ctx *Context, gh gitHub, number int, choose func([]PRChoice) (github.PR, error)) (github.PR, error) {
	if number > 0 {
		pr, err := gh.CLI.View(ctx.Repo.MainRoot, number)
		if err != nil {
			return github.PR{}, gh.fail(err)
		}
		return pr, nil
	}
	viewer, prs, err := gh.CLI.OpenPRsAndViewer(ctx.Repo.MainRoot, gh.Remote, fullLimit, 0)
	if err != nil {
		return github.PR{}, gh.fail(err)
	}
	if len(prs) == 0 {
		return github.PR{}, fmt.Errorf("%s has no open pull requests", gh.Remote.Slug)
	}
	if choose == nil {
		return github.PR{}, errors.New("no pull request named, and nothing here to choose with")
	}
	return choose(prChoices(ctx, viewer, prs))
}

// prChoices turns the open pull requests into the picker's rows: the ones
// waiting on this person's review first, then the rest in GitHub's order,
// which is most recently updated first.
func prChoices(ctx *Context, viewer string, prs []github.PR) []PRChoice {
	worktrees, _ := ctx.Repo.Worktrees()
	rows := make([]PRChoice, 0, len(prs))
	for _, pr := range prs {
		rows = append(rows, PRChoice{
			PR:              pr,
			ReviewRequested: pr.WantsReviewFrom(viewer),
			Worktree:        prWorktree(worktrees, pr, ctx.Config.MainBranch),
		})
	}
	slices.SortStableFunc(rows, func(a, b PRChoice) int {
		switch {
		case a.ReviewRequested == b.ReviewRequested:
			return 0
		case a.ReviewRequested:
			return -1
		}
		return 1
	})
	return rows
}

// checkoutPRInto makes the worktree and hands it to gh. It is created detached
// and with no files, and gh switches it to the pull request's branch, which
// populates the tree once; gh runs inside that worktree, so the main checkout's
// HEAD is never touched.
//
// A pull request that is not open gets a second attempt from refs/pull/<n>/head,
// because gh fetches the head branch by name and GitHub deletes that branch on
// merge. A gh that fails leaves nothing behind, and its own complaint is what
// gets reported.
func checkoutPRInto(ctx *Context, gh gitHub, pr github.PR, path string, w io.Writer) error {
	if err := ctx.Repo.AddDetachedWorktree(path, "HEAD"); err != nil {
		return err
	}
	// gh can create the branch and then still fail, fetching it or configuring
	// its remote; what it made is litter once the worktree goes. Which
	// branches were here first is read now, while that is still true.
	had := map[string]bool{}
	for _, branch := range pr.Branches(ctx.Config.MainBranch) {
		_, had[branch] = ctx.Repo.ResolveRef("refs/heads/" + branch)
	}
	err := gh.CLI.CheckoutInto(path, pr.Number)
	if err != nil && !pr.Open() {
		if merged := checkoutPullRef(ctx, gh, pr, path); merged == nil {
			fmt.Fprintf(w, "- #%d is %s; checked out refs/pull/%d/head, which has no upstream\n",
				pr.Number, pr.StateLabel(), pr.Number)
			return nil
		}
	}
	if err != nil {
		_ = ctx.Repo.DiscardWorktree(path)
		_ = ctx.Repo.Prune()
		for branch, existed := range had {
			if tip, now := ctx.Repo.ResolveRef("refs/heads/" + branch); now && !existed {
				_ = ctx.Repo.DeleteBranchAt(branch, tip)
			}
		}
		return gh.fail(err)
	}
	return nil
}

// checkoutPullRef puts the worktree on the pull request's head commit, which
// GitHub keeps at refs/pull/<n>/head after the branch itself is gone. The
// branch it creates tracks nothing: there is nothing left to push to.
func checkoutPullRef(ctx *Context, gh gitHub, pr github.PR, path string) error {
	branch := pr.LocalBranch(ctx.Config.MainBranch)
	ref := fmt.Sprintf("refs/pull/%d/head:refs/heads/%s", pr.Number, branch)
	if _, err := git.Run(path, "fetch", "--no-tags", gh.Remote.Name, ref); err != nil {
		return err
	}
	_, err := git.Run(path, "checkout", branch)
	return err
}

// prWorkName is the type and work name a pull request's worktree takes. A
// head branch that follows this repository's convention keeps its own, so a
// branch wt made lands where `wt new` would have put it; any other branch is
// named for the pull request, under the type its branch suggests.
func prWorkName(ctx *Context, pr github.PR) (typ, work string) {
	if t, w, ok := ctx.Scheme().Parse(pr.HeadRefName); ok && slices.Contains(ctx.Config.Types, t) {
		return t, w
	}
	slug := shorten(WorkNameFromBranch(pr.HeadRefName, ctx.Scheme().Suffix))
	if slug == "" {
		slug = shorten(WorkNameFromBranch(pr.Title, ctx.Scheme().Suffix))
	}
	work = fmt.Sprintf("pr-%d", pr.Number)
	if slug != "" {
		work += "-" + slug
	}
	return prType(ctx, pr.HeadRefName), work
}

// prType reads a type out of a branch someone else named: a leading segment
// that is one of this repository's types, in either spelling a branch uses.
func prType(ctx *Context, branch string) string {
	if head, _, ok := strings.Cut(branch, "/"); ok && slices.Contains(ctx.Config.Types, head) {
		return head
	}
	if t, _, ok := naming.InferType(branch, ctx.Config.Types); ok {
		return t
	}
	return ctx.Config.DefaultType
}

// slugLimit is how much of a branch name a work name keeps: long enough to
// tell two pull requests apart, short enough to type and to read in a table.
const slugLimit = 30

// shorten keeps whole dash-separated pieces of a name up to slugLimit, so a
// truncated work name never ends mid-word.
func shorten(s string) string {
	if utf8.RuneCountInString(s) <= slugLimit {
		return s
	}
	kept := ""
	for _, part := range strings.Split(s, "-") {
		next := part
		if kept != "" {
			next = kept + "-" + part
		}
		if utf8.RuneCountInString(next) > slugLimit {
			break
		}
		kept = next
	}
	if kept == "" {
		kept = string([]rune(s)[:slugLimit])
	}
	return kept
}

// PRList prints the repository's open pull requests and, for each, the
// worktree handling it. This is the pull-request-first view: `wt list` is the
// same facts read from the other end.
func PRList(ctx *Context, w io.Writer, width int) error {
	gh, err := openGitHub(ctx)
	if err != nil {
		return err
	}
	prs, err := gh.CLI.OpenWithChecks(ctx.Repo.MainRoot, fullLimit)
	if err != nil {
		return gh.fail(err)
	}
	if len(prs) == 0 {
		fmt.Fprintf(w, "%s has no open pull requests.\n", gh.Remote.Slug)
		return nil
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s, %s\n\n", gh.Remote.Slug, count(len(prs), "open pull request"))
	rows := [][]string{{"PR", "STATE", "AUTHOR", "TITLE", "WORKTREE"}}
	for _, pr := range prs {
		rows = append(rows, []string{
			fmt.Sprintf("#%d", pr.Number),
			prState(pr),
			dash(pr.AuthorLogin()),
			elideRight(pr.Title, titleWidth),
			dash(prWorktree(worktrees, pr, ctx.Config.MainBranch)),
		})
	}
	return printPathTable(w, rows, width)
}

// prWorktree is the path of the worktree holding a pull request's branch, or
// "" when nothing here is on it.
func prWorktree(worktrees repo.Worktrees, pr github.PR, trunk string) string {
	for _, branch := range pr.Branches(trunk) {
		if wt, ok := worktrees.ByBranch(branch); ok {
			return wt.Path
		}
	}
	return ""
}

// prState is a pull request's state and, when they were asked for, what its
// checks say.
func prState(pr github.PR) string {
	state := pr.StateLabel()
	if checks := pr.ChecksLabel(); checks != "" {
		state += " · checks " + checks
	}
	return state
}

// prLabel is a pull request in a column: its number and state, and for one
// merged somewhere other than trunk, where. A stacked pull request merged into
// its parent has landed nothing on trunk; the row should not read as if it had.
func prLabel(pr github.PR, trunks []string) string {
	label := fmt.Sprintf("#%d %s", pr.Number, pr.StateLabel())
	if pr.Merged() && pr.BaseRefName != "" && !slices.Contains(trunks, pr.BaseRefName) {
		label += " into " + pr.BaseRefName
	}
	return label
}

// trunks is every name this repository's trunk goes by: the configured main
// branch, and origin's own HEAD where that differs. A pull request merged into
// one of these reached trunk. Both reads are local.
func trunks(ctx *Context) []string {
	names := []string{ctx.Config.MainBranch}
	if head, ok := ctx.Repo.OriginHead(); ok && head != ctx.Config.MainBranch {
		names = append(names, head)
	}
	return names
}

// prLookup is how a command asks GitHub about its branches.
type prLookup struct {
	// warn names, in the stderr line a failed call prints, what the caller is
	// going on to do without an answer. Empty leaves the failure to the
	// caller, which is what a command reporting its own errors wants.
	warn string
	// refresh asks about every branch, however young the cached answer.
	refresh bool
	// deadline bounds the call; 0 leaves it to the package.
	deadline time.Duration
}

// prAnswers is what a lookup came back with.
type prAnswers struct {
	// byBranch is the pull request on each branch that has one.
	byBranch map[string]github.PR
	// cached is when the oldest answer taken from the cache was read, and is
	// zero when every answer came from this run's call.
	cached time.Time
	// err is what a gh that was supposed to answer said. The branches the
	// cache could answer are in byBranch either way.
	err error
}

// branchPRs is the pull request on each of branches, keyed by branch and
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
func branchPRs(ctx *Context, branches []string, opts prLookup) prAnswers {
	gh, err := openGitHub(ctx)
	if err != nil {
		return prAnswers{}
	}
	now := time.Now()
	cache := readPRCache(ctx, gh.Remote)
	out := prAnswers{byBranch: map[string]github.PR{}}
	var ask []string
	for _, b := range branches {
		if b == "" {
			continue
		}
		if !opts.refresh {
			if pr, fetched, ok := cache.fresh(b, now); ok {
				if pr.Number > 0 {
					out.byBranch[b] = pr
				}
				if out.cached.IsZero() || fetched.Before(out.cached) {
					out.cached = fetched
				}
				continue
			}
		}
		ask = append(ask, b)
	}
	if len(ask) == 0 {
		return out
	}
	prs, err := gh.CLI.PRsOnBranches(ctx.Repo.MainRoot, gh.Remote,
		github.HeadRefNames(ask, ctx.Config.MainBranch), opts.deadline)
	if err != nil {
		out.err = err
		if opts.warn != "" {
			warnGitHub(ctx, opts.warn, err)
		}
		return out
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
		out.byBranch[b] = pr
	}
	writePRCache(ctx, gh.Remote, answers, now)
	return out
}

// landedPR is the pull request that took a branch at tip onto trunk, read from
// the cache and nowhere else.
//
// This is `wt remove`'s only view of GitHub: it runs from git hooks, so it
// starts no process and makes no network call. The cache answers it because a
// merge is permanent and the commit it carried never moves, so the TTL does
// not apply. A branch with no entry leaves remove as it was.
func landedPR(ctx *Context, branch, tip string) int {
	if branch == "" || tip == "" || !ctx.UserConfig().GitHub {
		return 0
	}
	remote, ok := gitHubRemote(ctx.Repo.MainRoot)
	if !ok {
		return 0
	}
	pr, ok := readPRCache(ctx, remote).merged(branch)
	if !ok || !pr.Landed(tip, trunks(ctx)...) {
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

// prFact is the pull request line of `wt status <work>`: number, state, title.
// Empty when there is none, or when GitHub cannot be reached. It reads the
// cache `wt list` fills, so a listing already paid for it.
func prFact(ctx *Context, branch string) string {
	a := branchPRs(ctx, []string{branch}, prLookup{warn: "no pull requests shown", deadline: github.ListDeadline})
	pr, ok := a.byBranch[branch]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s · %s", prLabel(pr, trunks(ctx)), pr.Title)
}

// count writes "1 open pull request" or "3 open pull requests".
func count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
