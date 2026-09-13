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
	names, err := commands.WorkNames(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, n := range names {
		if !n.IsMain && n.Work != "" {
			out = append(out, n.Type+"/"+n.Work)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func newPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path <type>/<work>",
		Short: "Print the path of a worktree",
		Long: "Print where a piece of work lives. An existing worktree on that branch\n" +
			"wins whatever layout it is in, so this answers for worktrees other\n" +
			"tools made too; otherwise it prints the path `wt new` would use.\n\n" +
			"The path alone goes to stdout, so `cd \"$(wt path fix/login-crash)\"`\n" +
			"works — which is what `wt cd` does for you.",
		Example: "  wt path fix/login-crash # where that worktree is, or would go\n" +
			"  wt path login-crash     # bare name: the default type",
		Args:              needArgs(1, "<type>/<work>", "wt path fix/login-crash"),
		ValidArgsFunction: completeWork,
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			out, err := commands.Path(ctx, args[0])
			return printLine(cmd, out, err)
		}),
	}
}

func newBranchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "branch <type>/<work>",
		Short: "Print the branch name for a piece of work",
		Long: "Print the branch a piece of work gets: <type><suffix>/<work>, with the\n" +
			"type validated against the ones this repository declares.",
		Example: "  wt branch fix/login-crash  # prints fix_wt/login-crash\n" +
			"  wt branch login-crash      # bare name: the default type",
		Args:              needArgs(1, "<type>/<work>", "wt branch fix/login-crash"),
		ValidArgsFunction: completeWork,
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			out, err := commands.Branch(ctx, args[0])
			return printLine(cmd, out, err)
		}),
	}
}

func newBranchStripCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "branch-strip <branch>",
		Short:  "Strip the worktree type prefix from a branch name",
		Args:   needArgs(1, "<branch>", "wt branch-strip fix_wt/login-crash"),
		Hidden: true, // compat surface for strip_worktree_prefix
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			fmt.Fprintln(cmd.OutOrStdout(), naming.StripPrefix(args[0], ctx.Config.TypeSuffix))
			return nil
		}),
	}
}
