package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newCheckoutCmd() *cobra.Command {
	var opts commands.NewOptions
	cmd := &cobra.Command{
		Use:   "checkout <branch> [work-name]",
		Short: "Put a worktree on an existing branch",
		Long: "Create a worktree for a branch that already exists — reviewing a pull\n" +
			"request, or picking up work someone else started. The branch has to be\n" +
			"a local one: this never creates a branch, `wt new` does that.\n\n" +
			"Without a work name one is derived from the branch, with the type\n" +
			"prefix dropped and anything a directory cannot hold replaced.",
		Example: "  wt checkout fix_wt/login-crash        # worktree for an existing branch\n" +
			"  wt checkout release-2.1 rel21         # give the worktree its own name\n" +
			"  wt checkout release-2.1 --no-setup    # the worktree, nothing else\n" +
			"  wt checkout release-2.1 --skip-build  # provision, but do not build",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			work := ""
			if len(args) == 2 {
				work = args[1]
			}
			path, err := commands.Checkout(ctx, args[0], work, opts, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.SkipBuild, "skip-build", false, "skip build initialisation")
	cmd.Flags().BoolVar(&opts.NoSetup, "no-setup", false, "create the worktree without provisioning it")
	return cmd
}
