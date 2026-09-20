package commands

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/github"
)

// PROpen opens a worktree's pull request in the browser. arg names the
// worktree the way `wt list` prints it; empty is the one you are standing in.
//
// The listing is the one `wt list` reads, fetched fresh and left in the cache
// for it. A branch it does not carry — an open pull request older than the 50
// most recent — costs one more call for that branch alone, so "no pull
// request" never just means the limit was reached.
func PROpen(ctx *Context, arg string, w io.Writer) error {
	gh, err := openGitHub(ctx, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(arg) == "" {
		arg = "."
	}
	wt, err := Locate(ctx, arg)
	if err != nil {
		return err
	}
	if wt.Branch == "" {
		return fmt.Errorf("%s is on no branch, so nothing here names a pull request", wt.Path)
	}
	pr, err := prOnBranch(ctx, gh, wt.Branch)
	if err != nil {
		return err
	}
	if pr.Number == 0 {
		return fmt.Errorf("no pull request in %s has %s as its head branch", gh.Remote.Slug, wt.Branch)
	}
	fmt.Fprintf(w, "Opening #%d %s · %s\n", pr.Number, pr.StateLabel(), pr.Title)
	return gh.CLI.OpenWeb(ctx.Repo.MainRoot, pr.Number)
}

// prOnBranch is the pull request whose head is branch, or a zero PR when
// there is none. The wide listing goes first: it is the one worth caching,
// and it matches a fork's branch under both spellings.
func prOnBranch(ctx *Context, gh gitHub, branch string) (github.PR, error) {
	o := listQuery(0)
	prs, err := gh.CLI.List(ctx.Repo.MainRoot, o)
	if err != nil {
		return github.PR{}, err
	}
	writePRCache(ctx, gh, o, prs, time.Now())
	if pr, ok := github.ByBranch(prs, ctx.Config.MainBranch)[branch]; ok {
		return pr, nil
	}
	prs, err = gh.CLI.List(ctx.Repo.MainRoot, github.ListOptions{State: "all", Limit: 5, Head: branch})
	if err != nil {
		return github.PR{}, err
	}
	return github.ByBranch(prs, ctx.Config.MainBranch)[branch], nil
}
