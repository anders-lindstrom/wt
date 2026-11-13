package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
)

func newUpCmd() *cobra.Command {
	var noFetch, noFFTrunk, yes, force, allowDiverged, allowOwed, asJSON bool
	var expect, onto, upstream string
	var push func() syncrun.PushMode
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
			"steps, push and wt sync undo, and the same --onto <ref> to rebase onto\n" +
			"a ref other than trunk, with --upstream <ref> to replay only the\n" +
			"commits above it (wt sync rebase --help); the declaration is still\n" +
			"read from trunk. It differs in one place: a repository whose trunk\n" +
			"declares no .wt-sync.yaml works here, where wt sync rebase\n" +
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
			"A step that declares needs: [docker] refuses the run before anything\n" +
			"moves when Docker does not answer docker info, as for wt sync rebase;\n" +
			"--allow-owed rebases anyway and owes the step, to run by hand later.\n\n" +
			"--json prints one result object on stdout and the progress on stderr,\n" +
			"for a tool driving wt; --expect <token> refuses the run, touching\n" +
			"nothing, when trunk, the configuration, the stack or a remote the\n" +
			"stack has diverged from is no longer what wt status --json reported.\n" +
			"That token knows no --onto, so --onto with --expect is refused: take\n" +
			"the token from wt sync <work> --onto <ref> --json and run\n" +
			"wt sync rebase <work> --onto <ref> --expect <token>.\n" +
			"wt schema up prints its JSON Schema; docs/json.md explains it.",
		Example: "  wt up                  # the worktree you are in, if it syncs cleanly\n" +
			"  wt up --push --no-ff-trunk  # and push it; local trunk stays put\n" +
			"  wt up -y --force --no-fetch  # yes to all, past a busy session, no fetch\n" +

			"  wt up --yes --no-push --json  # for a tool: one JSON object on stdout\n" +
			"  wt up --onto test_wt/integration  # onto an integration branch, not trunk\n" +
			"  wt up --json --expect 1:0123abcd --allow-diverged  # only as the plan showed",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			work := "."
			if len(args) == 1 {
				work = args[0]
			}
			f := syncVerbFlags{rebase: true, yes: yes, noFetch: noFetch, noFFTrunk: noFFTrunk, ifReady: true, force: force,
				allowDiverged: allowDiverged, allowOwed: allowOwed, push: push(), onto: onto, upstream: upstream}
			switch {
			case upstream != "" && onto == "":
				return usagef("--upstream needs --onto: it says where the commits rebased onto --onto's ref start")
			case onto != "" && expect != "":
				return usagef("wt up --expect takes the token of wt status --json, which knows no --onto: take it from " +
					"wt sync <work> --onto <ref> --json, and run wt sync rebase <work> --onto <ref> --expect <token>")
			}
			if !asJSON {
				return withContext(func(cmd *cobra.Command, _ []string, ctx *kit.Context) error {
					opts, _ := runOptions(cmd, f, false)
					return synccmd.Up(ctx, work, opts, cmd.OutOrStdout())
				})(cmd, args)
			}
			// stdout carries the one result object and nothing else: the
			// progress, and any question, go to stderr.
			journal := syncrun.NewRunJournal(cmd.OutOrStdout())
			cmd.SetOut(cmd.ErrOrStderr())
			ctx, err := openLenient(cmd.ErrOrStderr())
			if err != nil {
				journal.Fail(err)
				return err
			}
			switch {
			case ctx == nil:
				err = kit.ErrNotInRepo
			case ctx.ConfigError != nil:
				err = ctx.ConfigError
			}
			if err != nil {
				journal.Fail(err)
				return err
			}
			opts, _ := runOptions(cmd, f, false)
			opts.Journal, opts.Expect = journal, expect
			return synccmd.Up(ctx, work, opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "rebase onto origin/<trunk> as last fetched")
	cmd.Flags().BoolVar(&noFFTrunk, "no-ff-trunk", false, "leave local <trunk> where it is after the fetch")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "yes to every question, the push included (--no-push keeps it out)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "go ahead even with an agent session in the worktree")
	cmd.Flags().BoolVar(&allowDiverged, "allow-diverged", false, "rebase a branch that has diverged from its own remote, as it stands")
	cmd.Flags().BoolVar(&allowOwed, "allow-owed", false, "rebase now even when a step's need does not answer; the step is owed, to run later")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print one result object on stdout, the progress on stderr")
	cmd.Flags().StringVar(&expect, "expect", "", "refuse unless the plan still matches this token from wt status --json")
	addOntoFlags(cmd, &onto, &upstream, "rebase onto <ref> instead of trunk: a branch, origin/<branch>, a tag or a commit")
	push = addPushFlags(cmd, "push when it is done, without asking", "neither push nor ask; print the push command")
	return cmd
}
