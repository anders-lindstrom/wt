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
			"request, or picking up work someone else started. Never creates a\n" +
			"branch; use `wt new` for that.\n\n" +
			"Without a work name one is derived from the branch.",
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
