package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newUpCmd() *cobra.Command {
	var noFetch, noFFTrunk, yes, force, allowDiverged, asJSON bool
	var expect string
	var push func() commands.PushMode
	cmd := &cobra.Command{
		Use:   "up [<work>]",
		Short: "Sync this worktree by rebasing, if it goes through without you",
		Long: "Sync the worktree you are in — or the one named — with trunk by\n" +
			"rebasing it, but only when nothing would be handed to you: the rebase is\n" +
			"conflict-free, or every stop is resolved by a strategy .wt-sync.yaml\n" +
			"declares and the simulation verified. Otherwise nothing is touched, the\n" +
			"reason is printed, and it exits non-zero: wt sync . shows the detail.\n\n" +
			"It is the short form of wt sync rebase . --if-ready, for the moment you\n" +
			"arrive in a worktree: the same fetch, safety ref, strategies, deferred\n" +
			"steps, push and wt sync undo. It differs in one place: a repository\n" +
			"whose trunk declares no .wt-sync.yaml works here, where wt sync rebase\n" +
			"refuses it. Nothing is declared there, so only a conflict-free rebase\n" +
			"goes, with no strategies and no deferred steps. For a tool, its --json\n" +
			"result and the token --expect takes are its own too (below).\n\n" +
			"Trunk is fetched first (--no-fetch rebases onto it as last fetched). At\n" +
			"the end it asks whether to push, and Enter means no. --yes (-y) answers\n" +
			"yes to every question, the push included; --push pushes without asking\n" +
			"anything else; --no-push prints the push command instead, with or\n" +
			"without --yes.\n\n" +
			"After the fetch, local <trunk> is fast-forwarded to origin/<trunk>\n" +
			"when that is safe: checked out nowhere, or in a clean checkout with\n" +
			"nothing in progress and no session busy in it. Otherwise it is left,\n" +
			"and a line says why. --no-ff-trunk, or wt config set ff_trunk false,\n" +
			"leaves it alone.\n\n" +
			"Before it rebases, the branch is compared with its own remote, the ref\n" +
			"a push of it would replace (what is recorded for it, else its own name\n" +
			"on the remote git pushes it to; never its upstream as such), fetched\n" +
			"with trunk. Behind it, the branch\n" +
			"is fast-forwarded first, where the worktree has no tracked changes,\n" +
			"nothing in progress and no session busy in it; with nothing to rebase\n" +
			"once it is there, that is all that happens. Diverged from it, with\n" +
			"commits there the branch never had, nothing is touched:\n" +
			"--allow-diverged rebases the branch as it stands. Rebased and not\n" +
			"pushed yet is neither, and goes on. wt sync undo takes back the\n" +
			"fast-forward with the rebase.\n\n" +
			"An agent session busy in the worktree refuses the run, since the files\n" +
			"it has read would change under it. --force (-f) goes ahead anyway and\n" +
			"names the session, so you can tell it; it lifts nothing else — dirt\n" +
			"and a conflict that is yours still refuse.\n\n" +
			"--json prints one result object on stdout and the progress on stderr,\n" +
			"for a tool driving wt; --expect <token> refuses the run, touching\n" +
			"nothing, when trunk, the configuration, the stack or a remote the\n" +
			"stack has diverged from is no longer what wt status --json reported.\n" +
			"wt schema up prints its JSON Schema; docs/json.md explains it.",
		Example: "  wt up                  # the worktree you are in, if it syncs cleanly\n" +
			"  wt up --push --no-ff-trunk  # and push it; local trunk stays put\n" +
			"  wt up -y --force --no-fetch  # yes to all, past a busy session, no fetch\n" +

			"  wt up --yes --no-push --json  # for a tool: one JSON object on stdout\n" +
			"  wt up --json --expect 1:0123abcd --allow-diverged  # only as the plan showed",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			work := "."
			if len(args) == 1 {
				work = args[0]
			}
			f := syncVerbFlags{rebase: true, yes: yes, noFetch: noFetch, noFFTrunk: noFFTrunk, ifReady: true, force: force,
				allowDiverged: allowDiverged, push: push()}
			if !asJSON {
				return withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
					opts, _ := runOptions(cmd, f, false)
					return commands.Up(ctx, work, opts, cmd.OutOrStdout())
				})(cmd, args)
			}
			// stdout carries the one result object and nothing else: the
			// progress, and any question, go to stderr.
			journal := commands.NewRunJournal(cmd.OutOrStdout())
			cmd.SetOut(cmd.ErrOrStderr())
			cwd, err := os.Getwd()
			if err != nil {
				journal.Fail(err)
				return err
			}
			ctx := commands.OpenLenient(cwd, cmd.ErrOrStderr())
			switch {
			case ctx == nil:
				err = commands.ErrNotInRepo
			case ctx.ConfigError != nil:
				err = ctx.ConfigError
			}
			if err != nil {
				journal.Fail(err)
				return err
			}
			opts, _ := runOptions(cmd, f, false)
			opts.Journal, opts.Expect = journal, expect
			return commands.Up(ctx, work, opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "rebase onto origin/<trunk> as last fetched")
	cmd.Flags().BoolVar(&noFFTrunk, "no-ff-trunk", false, "leave local <trunk> where it is after the fetch")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "yes to every question, the push included (--no-push keeps it out)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "go ahead even with an agent session in the worktree")
	cmd.Flags().BoolVar(&allowDiverged, "allow-diverged", false, "rebase a branch that has diverged from its own remote, as it stands")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print one result object on stdout, the progress on stderr")
	cmd.Flags().StringVar(&expect, "expect", "", "refuse unless the plan still matches this token from wt status --json")
	push = addPushFlags(cmd, "push when it is done, without asking", "neither push nor ask; print the push command")
	return cmd
}
