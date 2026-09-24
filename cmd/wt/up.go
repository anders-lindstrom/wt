package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newUpCmd() *cobra.Command {
	var noFetch, yes bool
	var push func() commands.PushMode
	cmd := &cobra.Command{
		Use:   "up [<work>]",
		Short: "Bring this worktree onto trunk, if it goes through without you",
		Long: "Rebase the worktree you are in — or the one named — onto trunk, but\n" +
			"only when nothing would be handed to you: the rebase is conflict-free,\n" +
			"or every stop is resolved by a strategy .wt-sync.yaml declares and the\n" +
			"simulation verified. Otherwise nothing is touched, the reason is\n" +
			"printed, and it exits non-zero: wt sync . shows the detail.\n\n" +
			"A repository whose trunk declares no .wt-sync.yaml works too: nothing\n" +
			"is declared there, so only a conflict-free rebase goes, with no\n" +
			"strategies and no deferred steps.\n\n" +
			"It is wt sync <work> --run --if-ready, for the moment you arrive in a\n" +
			"worktree. Trunk is fetched first (--no-fetch rebases onto it as last\n" +
			"fetched). At the end it asks whether to push, and Enter means no;\n" +
			"--push pushes without asking, and --no-push or --yes prints the push\n" +
			"command instead. --yes also skips the question asked when a Claude\n" +
			"session is idle in the worktree.",
		Example: "  wt up                  # the worktree you are in, if it syncs cleanly\n" +
			"  wt up --push           # and push it, without asking\n" +
			"  wt up login-crash      # another worktree of this repository\n" +
			"  wt up --no-fetch --yes # onto trunk as last fetched, asking nothing\n" +
			"  wt up --no-push        # rebase only; print the push command",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			work := "."
			if len(args) == 1 {
				work = args[0]
			}
			opts, _ := runOptions(cmd, syncVerbFlags{run: true, yes: yes, noFetch: noFetch, ifReady: true, push: push()}, false)
			return commands.Up(ctx, work, opts, cmd.OutOrStdout())
		}),
	}
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "rebase onto origin/<trunk> as last fetched")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "ask nothing; push only with --push")
	push = addPushFlags(cmd, "push when it is done, without asking", "neither push nor ask; print the push command")
	return cmd
}
