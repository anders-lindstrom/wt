package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newUpCmd() *cobra.Command {
	var noFetch, yes, force bool
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
			"fetched). At the end it asks whether to push, and Enter means no.\n" +
			"--yes (-y) answers yes to every question, the push included; --push\n" +
			"pushes without asking anything else; --no-push prints the push command\n" +
			"instead, with or without --yes.\n\n" +
			"A Claude session busy in the worktree refuses the run, since the files\n" +
			"it has read would change under it. --force (-f) goes ahead anyway and\n" +
			"names the session, so you can tell it; it lifts nothing else — dirt\n" +
			"and a conflict that is yours still refuse.",
		Example: "  wt up                  # the worktree you are in, if it syncs cleanly\n" +
			"  wt up --push           # and push it, without asking\n" +
			"  wt up -y --force       # yes to everything, past a session busy in it\n" +
			"  wt up --no-fetch --yes # onto trunk as last fetched, and push\n" +
			"  wt up --no-push        # rebase only; print the push command",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			work := "."
			if len(args) == 1 {
				work = args[0]
			}
			opts, _ := runOptions(cmd, syncVerbFlags{run: true, yes: yes, noFetch: noFetch, ifReady: true, force: force, push: push()}, false)
			return commands.Up(ctx, work, opts, cmd.OutOrStdout())
		}),
	}
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "rebase onto origin/<trunk> as last fetched")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "yes to every question, the push included (--no-push keeps it out)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "go ahead even with a Claude session in the worktree")
	push = addPushFlags(cmd, "push when it is done, without asking", "neither push nor ask; print the push command")
	return cmd
}
