package commands

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// How many pull requests each view asks for. Every page past the first is
// another round trip: against a repository with 206 pull requests, --state all
// took 0.96s at 50 and 2.87s at 100. listLimit is `wt list`'s, under a
// deadline of seconds; fullLimit is `wt pr list`'s, which can wait.
const (
	listLimit = 50
	fullLimit = 100
)

// titleWidth truncates a pull request title so the worktree column still
// fits beside it.
const titleWidth = 48

// PROptions controls `wt pr checkout`.
type PROptions struct {
	NewOptions
	// Choose picks one of the open pull requests when the caller named no
	// number. It is only ever called with a non-empty list. Nil means there
	// is nobody to ask.
	Choose func([]github.PR) (github.PR, error)
}

// PRCheckout puts a worktree on a pull request's own head branch, set up by
// `gh pr checkout`, so a push from it updates the pull request — from a fork
// too, where the branch's remote has to be the fork's URL.
//
// It goes through the same tail as `wt new`, so provisioning and any other
// integration happen by their own rules.
func PRCheckout(ctx *Context, number int, opts PROptions, w io.Writer) (string, error) {
	gh, err := openGitHub(ctx, true)
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
	// A second worktree on the same branch is not something git allows, and
	// the one that exists is the answer to the question anyway.
	for _, branch := range pr.Branches(ctx.Config.MainBranch) {
		if wt, ok := worktrees.ByBranch(branch); ok {
			fmt.Fprintf(w, "#%d is already checked out at %s, on %s\n", pr.Number, wt.Path, branch)
			return wt.Path, nil
		}
	}
	typ, work := prWorkName(ctx, pr)
	path := ctx.Scheme().Dir(typ, work)
	return addAndProvision(ctx, path, func() error {
		fmt.Fprintf(w, "Checking out #%d %s at %s\n", pr.Number, pr.HeadRefName, path)
		return checkoutPRInto(ctx, gh, pr, path, w)
	}, opts.NewOptions, w)
}

// resolvePR is the pull request to check out: the one named, whatever state
// it is in, or one chosen from the open ones. A merged pull request can be
// named but is never offered in the list.
func resolvePR(ctx *Context, gh gitHub, number int, choose func([]github.PR) (github.PR, error)) (github.PR, error) {
	if number > 0 {
		return gh.CLI.View(ctx.Repo.MainRoot, number)
	}
	prs, err := gh.CLI.List(ctx.Repo.MainRoot, github.ListOptions{State: "open", Limit: fullLimit})
	if err != nil {
		return github.PR{}, err
	}
	if len(prs) == 0 {
		return github.PR{}, fmt.Errorf("%s has no open pull requests", gh.Remote.Slug)
	}
	if choose == nil {
		return github.PR{}, errors.New("no pull request named, and nothing here to choose with")
	}
	return choose(prs)
}

// checkoutPRInto makes the worktree and hands it to gh. It is created
// detached and with no files, and gh switches it to the pull request's
// branch, which populates the tree once. gh runs inside that worktree, so the
// main checkout's HEAD is never touched, and a gh that fails leaves nothing
// behind.
//
// A pull request that is not open gets a second attempt from
// refs/pull/<n>/head, because gh fetches the head branch by name and GitHub
// deletes that branch on merge. gh's own failure is what gets reported if the
// second attempt fails too.
func checkoutPRInto(ctx *Context, gh gitHub, pr github.PR, path string, w io.Writer) error {
	if err := ctx.Repo.AddDetachedWorktree(path, "HEAD"); err != nil {
		return err
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
		return err
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
	slug := shorten(WorkNameFromBranch(pr.HeadRefName, ctx.Config.TypeSuffix))
	if slug == "" {
		slug = shorten(WorkNameFromBranch(pr.Title, ctx.Config.TypeSuffix))
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
	gh, err := openGitHub(ctx, true)
	if err != nil {
		return err
	}
	prs, err := gh.CLI.List(ctx.Repo.MainRoot, github.ListOptions{
		State: "open", Limit: fullLimit, Checks: true})
	if err != nil {
		return err
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
			prWorktree(worktrees, pr, ctx.Config.MainBranch),
		})
	}
	return printPathTable(w, rows, width)
}

// prWorktree is the path of the worktree holding a pull request's branch, or
// "-" when nothing here is on it.
func prWorktree(worktrees repo.Worktrees, pr github.PR, trunk string) string {
	for _, branch := range pr.Branches(trunk) {
		if wt, ok := worktrees.ByBranch(branch); ok {
			return wt.Path
		}
	}
	return "-"
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

// prLabel is a pull request in a column: its number and its state.
func prLabel(pr github.PR) string {
	return fmt.Sprintf("#%d %s", pr.Number, pr.StateLabel())
}

// worktreePRs is the pull request each worktree branch belongs to, for the PR
// column of `wt list`. One bounded call covers the whole listing, and every
// way it can fail — off, no gh, no remote, offline, not logged in, over the
// deadline — answers nil, which leaves `wt list` as it was.
func worktreePRs(ctx *Context) map[string]github.PR {
	gh, err := openGitHub(ctx, false)
	if err != nil {
		return nil
	}
	prs, err := gh.CLI.List(ctx.Repo.MainRoot, github.ListOptions{
		State: "all", Limit: listLimit, Deadline: github.ListDeadline})
	if err != nil {
		return nil
	}
	return github.ByBranch(prs, ctx.Config.MainBranch)
}

// prFact is the pull request line of `wt status <work>`: the number, the
// state and the title. Empty when there is none, or when GitHub cannot be
// reached — one worktree's detail is not the place to report that.
func prFact(ctx *Context, branch string) string {
	if branch == "" {
		return ""
	}
	gh, err := openGitHub(ctx, false)
	if err != nil {
		return ""
	}
	prs, err := gh.CLI.List(ctx.Repo.MainRoot, github.ListOptions{
		State: "all", Limit: 5, Head: branch, Deadline: github.ListDeadline})
	if err != nil || len(prs) == 0 {
		return ""
	}
	pr := github.ByBranch(prs, ctx.Config.MainBranch)[branch]
	if pr.Number == 0 {
		return ""
	}
	return fmt.Sprintf("%s · %s", prLabel(pr), pr.Title)
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
