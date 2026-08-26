package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newNewCmd() *cobra.Command {
	var opts commands.NewOptions
	cmd := &cobra.Command{
		Use:   "new <type>/<work>",
		Short: "Create a worktree and its branch, then provision it",
		Long: "Create <type>_wt/<work> from the repository's main branch, place the\n" +
			"worktree at the canonical path, and provision it. A bare <work> takes\n" +
			"the repository's default type.",
		Args: needArgs(1, "<type>/<work>", "wt new fix/login-crash"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			path, err := commands.New(ctx, args[0], opts, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			// The path alone goes to stdout so `cd "$(wt new ...)"` works.
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "branch to cut from (default: the configured main branch)")
	cmd.Flags().BoolVar(&opts.SkipBuild, "skip-build", false, "skip build initialisation")
	cmd.Flags().BoolVar(&opts.NoSetup, "no-setup", false, "create the worktree without provisioning it")
	return cmd
}
