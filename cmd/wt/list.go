package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

// pathWidthHelp ends the help of the commands that print a path column.
const pathWidthHelp = "On a terminal, paths are shown from ~ and shortened from the left to fit\n" +
	"its width. Piped, they are printed whole."

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every worktree of this repository",
		Long: "Print every worktree of this repository: its work name, its branch and\n" +
			"its path. A worktree not at the path this layout gives it is marked,\n" +
			"and the legend says what to do about it.\n\n" + pathWidthHelp,
		Example: "  wt list          # work name, branch and path for every worktree\n" +
			"  wt ls            # the same, for the impatient\n" +
			"  wt list | cat    # whole paths, however narrow the terminal",
		Args: cobra.NoArgs,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			out := cmd.OutOrStdout()
			return commands.List(ctx, out, terminalWidth(out))
		}),
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [<work>]",
		Short: "Show each worktree's state and standing against trunk",
		Long: "Print each worktree's branch, whether its checkout is clean, dirty or\n" +
			"unreadable, and where its branch stands against trunk: how many commits\n" +
			"behind and ahead of origin/<trunk> as last fetched, or of the local trunk\n" +
			"when origin/<trunk> is not here. The first line says which, and how old\n" +
			"the fetch is. Nothing is fetched; wt sync fetches.\n\n" +
			"With a worktree named — by anything `wt list` prints for it, or . for the\n" +
			"one you are in — that worktree alone, one fact per line: branch, path,\n" +
			"state, standing against trunk, the Claude sessions in it, then the verdict\n" +
			"wt sync would give it, simulated against trunk as last fetched: its class\n" +
			"in wt sync's words, and what to do about it.\n\n" + pathWidthHelp,
		Example: "  wt status               # state and standing for every worktree\n" +
			"  wt status | grep dirty  # only the ones with uncommitted changes\n" +
			"  wt status login-crash   # that worktree in full, with wt sync's verdict\n" +
			"  wt status .             # the one you are standing in",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			out := cmd.OutOrStdout()
			if len(args) == 1 {
				return commands.StatusWorktree(ctx, args[0], commands.StatusOptions{}, out)
			}
			return commands.Status(ctx, out, terminalWidth(out))
		}),
	}
}
