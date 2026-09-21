package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/naming"
)

// completeWork offers the work names of existing worktrees. This is the
// ergonomic point of the tool: the names are never memorable, so the shell
// should supply them. "." and "/" are not offered: each is one key, quicker
// to type than to pick from a list.
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

// completeWorkThenCommand is completeWork for the worktree, then the shell's
// own completion for the command that follows it and its arguments: the
// Default directive with no candidates is how cobra hands the word back to
// the shell, which completes it as a file name. Even the shell layer's `wt`
// function ends up here, since the completion script calls `wt __complete`,
// and the function passes anything but cd and exec to the binary.
func completeWorkThenCommand(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	return completeWork(cmd, args, toComplete)
}

func newPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path <work>",
		Short: "Print the path of a worktree",
		Long: "Print where a piece of work lives. A worktree that already exists wins,\n" +
			"named by any of the things `wt list` prints for it — the work name, the\n" +
			"branch or the path — whatever layout it is in, so this answers for\n" +
			"worktrees other tools made too; . is the one you are standing in and\n" +
			"/ the main checkout. Otherwise it prints the path `wt new` would use,\n" +
			"with a bare name taking the default type.\n\n" +
			"The path alone goes to stdout, so `cd \"$(wt path fix/login-crash)\"`\n" +
			"works — which is what `wt cd` does for you.",
		Example: "  wt path fix/login-crash    # where that worktree is, or would go\n" +
			"  wt path login-crash        # the existing worktree, whatever its type\n" +
			"  wt path fix_wt/login-crash # by branch, as `wt list` prints it\n" +
			"  wt path /                  # the main checkout",
		Args:              needArgs(1, "<work>", "wt path fix/login-crash"),
		ValidArgsFunction: completeWork,
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			out, err := commands.Path(ctx, args[0])
			return printLine(cmd, out, err)
		}),
	}
}

func newBranchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "branch <work>",
		Short: "Print the branch name for a piece of work",
		Long: "Print the branch of a piece of work. A worktree that already exists wins,\n" +
			"named by any of the things `wt list` prints for it — the work name, the\n" +
			"branch or the path; . is the one you are standing in, and / is trunk.\n" +
			"Otherwise it prints the branch `wt new` would make,\n" +
			"<type><suffix>/<work>, with the type validated against the ones this\n" +
			"repository declares and a bare name taking the default type.",
		Example: "  wt branch fix/login-crash  # prints fix_wt/login-crash\n" +
			"  wt branch login-crash      # the existing worktree's, else the default type\n" +
			"  wt branch /                # trunk, as this repository configures it",
		Args:              needArgs(1, "<work>", "wt branch fix/login-crash"),
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
			fmt.Fprintln(cmd.OutOrStdout(), naming.StripPrefix(args[0], ctx.Scheme().Suffix))
			return nil
		}),
	}
}
