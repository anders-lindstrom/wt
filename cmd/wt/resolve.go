package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/naming"
)

// completeWork offers the work names of existing worktrees. This is the
// ergonomic point of the tool: the names are never memorable, so the shell
// should supply them.
func completeWork(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, err := openContext()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, w := range worktrees {
		if w.IsMain || w.Branch == "" {
			continue
		}
		if typ, work, ok := naming.ParseBranch(w.Branch, ctx.Config.TypeSuffix); ok {
			names = append(names, typ+"/"+work)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func newPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "path <type>/<work>",
		Short:             "Print the path of a worktree",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			out, err := commands.Path(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

func newBranchCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "branch <type>/<work>",
		Short:             "Print the branch name for a piece of work",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			out, err := commands.Branch(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

func newBranchStripCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "branch-strip <branch>",
		Short:  "Strip the worktree type prefix from a branch name",
		Args:   cobra.ExactArgs(1),
		Hidden: true, // compat surface for strip_worktree_prefix
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), naming.StripPrefix(args[0], ctx.Config.TypeSuffix))
			return nil
		},
	}
}
