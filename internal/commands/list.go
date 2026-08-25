package commands

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
)

// List prints every worktree of the repository, in whatever layout it is in.
// A worktree outside the canonical path is marked "!", because worktrees made
// by other tools — Superset's shape, or anything created before migration —
// stay valid but are candidates for `wt migrate`.
func List(ctx *Context, w io.Writer) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\tWORK\tBRANCH\tPATH")
	for _, wt := range worktrees {
		work, branch := "(main)", wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		mark := ""
		if !wt.IsMain {
			if typ, name, ok := naming.ParseBranch(wt.Branch, ctx.Config.TypeSuffix); ok {
				work = name
				if wt.Path != naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, name, ctx.Config.TypeSuffix) {
					mark = "!"
				}
			} else {
				work = "-"
				mark = "!"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", mark, work, branch, wt.Path)
	}
	return tw.Flush()
}

// Status prints each worktree's branch and whether its checkout is clean.
func Status(ctx *Context, w io.Writer) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tSTATE\tPATH")
	for _, wt := range worktrees {
		branch := wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		state := "clean"
		if out, err := git.Run(wt.Path, "status", "--porcelain"); err != nil {
			state = "unreadable"
		} else if out != "" {
			state = "dirty"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", branch, state, wt.Path)
	}
	return tw.Flush()
}
