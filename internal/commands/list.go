package commands

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
)

// List prints every worktree of the repository, in whatever layout it is in.
// Anything not at the canonical path is marked, and the two marks mean
// different things: "s" is Superset's layout, which is deliberate and must be
// left alone, while "!" is a layout nothing owns and `wt migrate` can move.
func List(ctx *Context, w io.Writer) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\tWORK\tBRANCH\tPATH")
	var seen [3]bool
	for _, wt := range worktrees {
		work, branch := "(main)", wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		mark := ""
		if !wt.IsMain {
			layout := naming.Foreign
			if typ, name, ok := naming.ParseBranch(wt.Branch, ctx.Config.TypeSuffix); ok {
				work = name
				layout = naming.Classify(wt.Path, ctx.Repo.Parent, ctx.Repo.Name,
					typ, name, ctx.Config.TypeSuffix)
			} else {
				work = "-"
			}
			seen[layout] = true
			mark = layoutMark(layout)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", mark, work, branch, wt.Path)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if seen[naming.Superset] || seen[naming.Foreign] {
		fmt.Fprintln(w, "")
	}
	if seen[naming.Superset] {
		fmt.Fprintln(w, "s  Superset's layout — its workspace holds this path; leave it where it is")
	}
	if seen[naming.Foreign] {
		fmt.Fprintln(w, "!  not a layout wt recognises — `wt migrate <work>` moves it to the canonical path")
	}
	return nil
}

func layoutMark(l naming.Layout) string {
	switch l {
	case naming.Superset:
		return "s"
	case naming.Foreign:
		return "!"
	default:
		return ""
	}
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
