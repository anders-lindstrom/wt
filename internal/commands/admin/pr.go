package admin

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
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
	provision.NewOptions
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
func PRCheckout(ctx *kit.Context, number int, opts PROptions, w io.Writer) (string, error) {
	gh, err := kit.OpenGitHub(ctx)
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
	if err := divergedFromPR(ctx, gh, pr); err != nil {
		return "", err
	}
	typ, work := prWorkName(ctx, pr)
	path := ctx.Scheme().Dir(typ, work)
	return provision.AddAndProvision(ctx, path, func() error {
		fmt.Fprintf(w, "Checking out #%d %s at %s\n", pr.Number, pr.HeadRefName, path)
		return checkoutPRInto(ctx, gh, pr, path, w)
	}, opts.NewOptions, w, nil)
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
func resolvePR(ctx *kit.Context, gh kit.GitHub, number int, choose func([]PRChoice) (github.PR, error)) (github.PR, error) {
	if number > 0 {
		pr, err := gh.CLI.View(ctx.Repo.MainRoot, number)
		if err != nil {
			return github.PR{}, gh.Fail(err)
		}
		return pr, nil
	}
	viewer, prs, err := gh.CLI.OpenPRsAndViewer(ctx.Repo.MainRoot, gh.Remote, fullLimit, 0)
	if err != nil {
		return github.PR{}, gh.Fail(err)
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
func prChoices(ctx *kit.Context, viewer string, prs []github.PR) []PRChoice {
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

// divergedFromPR refuses a checkout gh is going to fail: a local branch of the
// pull request's name that has commits the pull request lacks and lacks commits
// it has. gh only ever fast-forwards that branch, and says so under a screen of
// git's advice. Nothing is made, and the branch is left for its owner to rename
// or delete.
//
// A branch that is only behind is fast-forwarded by gh, and one only ahead is
// left alone by it, so both pass. So does anything wt cannot be sure of. A
// fork's branch may go under the fork owner's name, by a rule that turns on
// the base repository's default branch. A finished pull request's branch may
// have moved on since, and gh fetches the branch, not the recorded head. A
// head commit may not be here and not be fetchable.
func divergedFromPR(ctx *kit.Context, gh kit.GitHub, pr github.PR) error {
	branch := pr.HeadRefName
	if !pr.Open() || pr.IsCrossRepository || !commitID.MatchString(pr.HeadRefOid) {
		return nil
	}
	tip, ok, err := ctx.Repo.ResolveRef("refs/heads/" + branch)
	if err != nil {
		return err
	}
	if !ok || tip == pr.HeadRefOid {
		return nil
	}
	_, here, err := ctx.Repo.ResolveRef(pr.HeadRefOid)
	if err != nil {
		return err
	}
	if !here {
		// GitHub keeps every pull request's head at refs/pull/<n>/head. The
		// fetch names no destination and writes no FETCH_HEAD: it brings the
		// commits and changes nothing else.
		_, _ = git.RunTimeout(ctx.Repo.MainRoot, kit.NetworkTimeout, "fetch", "--quiet", "--no-tags",
			"--no-recurse-submodules", "--no-write-fetch-head", "--refmap=",
			gh.Remote.Name, fmt.Sprintf("refs/pull/%d/head", pr.Number))
		if _, here, err = ctx.Repo.ResolveRef(pr.HeadRefOid); err != nil {
			return err
		} else if !here {
			return nil
		}
	}
	ahead, okAhead := ctx.Repo.CommitsAhead(tip, pr.HeadRefOid)
	behind, okBehind := ctx.Repo.CommitsAhead(pr.HeadRefOid, tip)
	if !okAhead || !okBehind || ahead == 0 || behind == 0 {
		return nil
	}
	return fmt.Errorf("#%d is on %s, and a local branch of that name has diverged from it: "+
		"%d ahead, %d behind\n"+
		"  gh can only fast-forward a branch that exists; nothing was made\n"+
		"  it carries %s of its own: check that nothing is lost, then either\n"+
		"    rename it:  git branch -m %s %s-old\n"+
		"    delete it:  git branch -D %s\n"+
		"  and run `wt pr checkout %d` again",
		pr.Number, branch, ahead, behind, count(ahead, "commit"), branch, branch, branch, pr.Number)
}

// commitID is a whole commit id, as GitHub reports a pull request's head.
var commitID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// checkoutPRInto makes the worktree and hands it to gh. It is created detached
// and with no files, and gh switches it to the pull request's branch, which
// populates the tree once; gh runs inside that worktree, so the main checkout's
// HEAD is never touched.
//
// A pull request that is not open gets a second attempt from refs/pull/<n>/head,
// because gh fetches the head branch by name and GitHub deletes that branch on
// merge. A gh that fails leaves nothing behind, and what gets reported is its
// own complaint and what became of the worktree made for it.
func checkoutPRInto(ctx *kit.Context, gh kit.GitHub, pr github.PR, path string, w io.Writer) error {
	if err := ctx.Repo.AddDetachedWorktree(path, "HEAD"); err != nil {
		return err
	}
	// gh can create the branch and then still fail, fetching it or configuring
	// its remote; what it made is litter once the worktree goes. Which
	// branches were here first is read now, while that is still true.
	had := map[string]bool{}
	for _, branch := range pr.Branches(ctx.Config.MainBranch) {
		// One git cannot read counts as here first, so it is never deleted.
		_, here, err := ctx.Repo.ResolveRef("refs/heads/" + branch)
		had[branch] = here || err != nil
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
		discarded := ctx.Repo.DiscardWorktree(path)
		_ = ctx.Repo.Prune()
		for branch, existed := range had {
			if tip, now, _ := ctx.Repo.ResolveRef("refs/heads/" + branch); now && !existed {
				_ = ctx.Repo.DeleteBranchAt(branch, tip)
			}
		}
		return fmt.Errorf("%w\n  %s", gh.Fail(err), discardedLine(path, discarded))
	}
	return nil
}

// discardedLine says what became of the worktree a failed checkout was made
// in: gone, so the next attempt starts clean, or still there and in its way.
func discardedLine(path string, discarded error) string {
	if _, err := os.Lstat(path); discarded == nil && errors.Is(err, fs.ErrNotExist) {
		return fmt.Sprintf("the half-made worktree at %s was removed; a retry starts clean", path)
	}
	line := fmt.Sprintf("the half-made worktree at %s could not be removed", path)
	if discarded != nil {
		line += ": " + kit.OneLine(discarded.Error())
	}
	// Twice, because one --force does not remove a locked worktree.
	return line + "\n  remove it before a retry: git worktree remove --force --force " + path
}

// checkoutPullRef puts the worktree on the pull request's head commit, which
// GitHub keeps at refs/pull/<n>/head after the branch itself is gone. The
// branch it creates tracks nothing: there is nothing left to push to.
func checkoutPullRef(ctx *kit.Context, gh kit.GitHub, pr github.PR, path string) error {
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
func prWorkName(ctx *kit.Context, pr github.PR) (typ, work string) {
	if t, w, ok := ctx.Scheme().Parse(pr.HeadRefName); ok && slices.Contains(ctx.Config.Types, t) {
		return t, w
	}
	slug := shorten(provision.WorkNameFromBranch(pr.HeadRefName, ctx.Scheme().Suffix))
	if slug == "" {
		slug = shorten(provision.WorkNameFromBranch(pr.Title, ctx.Scheme().Suffix))
	}
	work = fmt.Sprintf("pr-%d", pr.Number)
	if slug != "" {
		work += "-" + slug
	}
	return prType(ctx, pr.HeadRefName), work
}

// prType reads a type out of a branch someone else named: a leading segment
// that is one of this repository's types, in either spelling a branch uses.
func prType(ctx *kit.Context, branch string) string {
	if head, _, found := strings.Cut(branch, "/"); found {
		if typ, ok := ctx.Vocab().Type(head); ok {
			return typ
		}
	}
	if t, _, ok := naming.InferType(branch, ctx.Vocab()); ok {
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
func PRList(ctx *kit.Context, w io.Writer, width int) error {
	gh, err := kit.OpenGitHub(ctx)
	if err != nil {
		return err
	}
	prs, err := gh.CLI.OpenWithChecks(ctx.Repo.MainRoot, fullLimit)
	if err != nil {
		return gh.Fail(err)
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
			Dash(pr.AuthorLogin()),
			kit.ElideRight(pr.Title, titleWidth),
			Dash(prWorktree(worktrees, pr, ctx.Config.MainBranch)),
		})
	}
	return kit.PrintPathTable(w, rows, width)
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

// count writes "1 open pull request" or "3 open pull requests".
func count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// Dash is s, or - for an empty cell.
func Dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
