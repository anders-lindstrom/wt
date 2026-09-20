package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

// pathWidthHelp ends the help of the commands that print a path column.
const pathWidthHelp = "On a terminal, paths are shown from ~ and shortened from the left to fit\n" +
	"its width. Piped, they are printed whole."

func newListCmd() *cobra.Command {
	var opts commands.ListOptions
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every worktree of this repository",
		Long: "Print every worktree of this repository: its work name, its branch and\n" +
			"its path. A worktree not at the path this layout gives it is marked,\n" +
			"and the legend says what to do about it.\n\n" +
			"A PR column appears when a worktree here is on a pull request. GitHub\n" +
			"is asked about those branches by name, so a pull request of any age is\n" +
			"found — however many have been opened since. Each branch's answer is\n" +
			"kept in this repository for five minutes, so only the first listing\n" +
			"pays the `gh` call of around a second, and a branch made since then is\n" +
			"asked about on its own rather than waiting the five minutes out.\n" +
			"--refresh asks about them all again, --no-pr leaves the column out, and\n" +
			"`wt config set github false` turns the whole thing off.\n\n" +
			"A pull request merged somewhere other than trunk says where: a stacked\n" +
			"one reads `#31 merged into feat_wt/its-parent`, because nothing of it\n" +
			"has reached trunk yet.\n\n" + pathWidthHelp,
		Example: "  wt list            # work name, branch and path for every worktree\n" +
			"  wt ls              # the same, for the impatient\n" +
			"  wt list | cat      # whole paths, however narrow the terminal\n" +
			"  wt list --no-pr    # no pull requests, and no call to GitHub\n" +
			"  wt list --refresh  # ask GitHub again rather than use the cache",
		Args: cobra.NoArgs,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			out := cmd.OutOrStdout()
			return commands.List(ctx, opts, out, terminalWidth(out))
		}),
	}
	cmd.Flags().BoolVar(&opts.NoPR, "no-pr", false, "do not ask GitHub which worktree has a pull request")
	cmd.Flags().BoolVar(&opts.Refresh, "refresh", false, "ask GitHub again instead of using the cached pull requests")
	return cmd
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
			"state, standing against trunk, its pull request where it has one, the\n" +
			"Claude sessions in it, then the verdict wt sync would give it, simulated\n" +
			"against trunk as last fetched: its class in wt sync's words, and what to\n" +
			"do about it.\n\n" +
			"The pull request line comes from the same answers `wt list` caches, so a\n" +
			"listing has already paid for it.\n\n" + pathWidthHelp,
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
