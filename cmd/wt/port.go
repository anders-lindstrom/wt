package main

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

func newPortCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "port [<work>]",
		Short: "Print the port a worktree's dev server should use",
		Long: "Print a port of the worktree's own, so dev servers in several\n" +
			"worktrees of one repository do not fight over one. It is a hash of\n" +
			"the repository's main checkout and the branch, in 10000–19999: the\n" +
			"same branch gets the same port every time, and another branch almost\n" +
			"always another. A detached worktree's path stands in for its branch.\n\n" +
			"<work> is named as for `wt path`, and may be work that does not exist\n" +
			"yet; with none, or ., it is the worktree you are in. provision.sh and\n" +
			"the build command get the same number as WT_PORT, with WT_BRANCH and\n" +
			"WT_WORK.\n\n" +
			"Nothing is reserved and nothing checks the port is free; that is\n" +
			"devports' job, and `devports list \"$(wt port)\"` shows who holds it.",
		Example: "  wt port                       # the worktree you are in\n" +
			"  wt port fix/login-crash       # that worktree's, or the one it would get\n" +
			"  wt port /                     # the main checkout's",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *kit.Context) error {
			spec := "."
			if len(args) == 1 {
				spec = args[0]
			}
			port, err := commands.Port(ctx, spec)
			return printLine(cmd, strconv.Itoa(port), err)
		}),
	}
}
