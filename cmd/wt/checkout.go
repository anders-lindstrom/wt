package main

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newCheckoutCmd() *cobra.Command {
	var opts commands.NewOptions
	var asJSON, dryRun bool
	var expect string
	cmd := &cobra.Command{
		Use:     "checkout <branch>|<remote>/<branch> [<work>]",
		Aliases: []string{"co"},
		Short:   "Put a worktree on an existing branch, local or remote",
		Long: "Create a worktree for a branch that already exists — reviewing a pull\n" +
			"request, or picking up work someone else started. This never invents\n" +
			"a branch no one has: `wt new` does that.\n\n" +
			"A local branch of exactly that name is used as it is. Otherwise\n" +
			"<remote>/<branch>, or a bare <branch> exactly one remote has, creates\n" +
			"the local branch at the remote-tracking commit with that upstream set,\n" +
			"as `git checkout` does. Several remotes having it is refused unless\n" +
			"checkout.defaultRemote names one. Nothing is fetched: remote branches\n" +
			"are as the last fetch left them, so `git fetch` first for a new one.\n\n" +
			"Without <work> a work name is derived from the branch, with the type\n" +
			"prefix dropped and anything a directory cannot hold replaced.\n\n" +
			"--dry-run, --json and --expect work as for wt new; the plan's token\n" +
			"also pins the branch's commit, or the remote-tracking ref's, so\n" +
			"--expect refuses a branch that has moved since. wt schema checkout-plan\n" +
			"and wt schema checkout print their JSON Schemas.",
		Example: "  wt checkout fix_wt/login-crash        # worktree for an existing branch\n" +
			"  wt checkout origin/release-2.1 rel21 --dry-run  # from origin, as rel21\n" +
			"  wt checkout release-2.1 --no-setup    # the worktree, nothing else\n" +
			"  wt checkout release-2.1 --no-build --no-superset  # no build, no Superset\n" +
			"  wt checkout release-2.1 --json --expect 1:0123abcd  # if the plan holds",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			work := ""
			if len(args) == 2 {
				work = args[1]
			}
			return runCreate(cmd, args, "checkout", create{json: asJSON, dryRun: dryRun, expect: expect},
				func(ctx *commands.Context) (string, error) {
					return commands.Checkout(ctx, args[0], work, opts, cmd.ErrOrStderr())
				},
				func(ctx *commands.Context, w io.Writer) error {
					return commands.CheckoutDryRun(ctx, args[0], work, opts, w)
				},
				func(ctx *commands.Context, w io.Writer) error {
					return commands.CheckoutPlanJSON(ctx, args[0], work, opts, w)
				},
				func(ctx *commands.Context, j *commands.CreateJournal, w io.Writer) error {
					return commands.CheckoutJSON(ctx, args[0], work, opts, expect, j, w)
				})
		},
	}
	addProvisionFlags(cmd, &opts.SkipBuild, &opts.NoSetup, &opts.NoSuperset)
	addCreateFlags(cmd, &asJSON, &dryRun, &expect)
	return cmd
}
