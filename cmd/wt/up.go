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
			"After the fetch, local <trunk> is fast-forwarded to origin/<trunk>\n" +
			"when that is safe: checked out nowhere, or in a clean checkout with\n" +
			"nothing in progress and no session busy in it. Otherwise it is left,\n" +
			"and a line says why. --no-ff-trunk, or wt config set ff_trunk false,\n" +
			"leaves it alone.\n\n" +
			"Before it rebases, the branch is compared with its own remote, the ref\n" +
			"a push of it would replace (<branch>@{push}, else origin/<branch>;\n" +
			"never its upstream as such), fetched with trunk. Behind it, the branch\n" +
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
			f := syncVerbFlags{run: true, yes: yes, noFetch: noFetch, noFFTrunk: noFFTrunk, ifReady: true, force: force,
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
