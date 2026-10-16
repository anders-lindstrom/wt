package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// PROpen opens a worktree's pull request in the browser. arg names the worktree
// the way `wt list` prints it; empty is the one you are standing in.
//
// The lookup asks GitHub about that one branch and leaves the answer in the
// cache `wt list` reads. One gh process, and "no pull request" means there is
// none rather than that a listing did not reach far enough back.
func PROpen(ctx *kit.Context, arg string, w io.Writer) error {
	gh, err := kit.OpenGitHub(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(arg) == "" {
		arg = "."
	}
	wt, err := kit.Locate(ctx, arg)
	if err != nil {
		return err
	}
	if wt.Branch == "" {
		return fmt.Errorf("%s is on no branch, so nothing here names a pull request", wt.Path)
	}
	// This command reports its own failures, so the lookup does not warn: a
	// gh that could not answer is an error here, not the absence of a pull
	// request.
	a := kit.BranchPRs(ctx, []string{wt.Branch}, kit.PRLookup{Refresh: true})
	if a.Err != nil {
		return gh.Fail(a.Err)
	}
	pr, ok := a.ByBranch[wt.Branch]
	if !ok {
		return fmt.Errorf("no pull request in %s has %s as its head branch", gh.Remote.Slug, wt.Branch)
	}
	fmt.Fprintf(w, "Opening %s · %s\n", kit.PRLabel(pr, kit.Trunks(ctx)), pr.Title)
	return gh.Fail(gh.CLI.OpenWeb(ctx.Repo.MainRoot, pr.Number))
}
