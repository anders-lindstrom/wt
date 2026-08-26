package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newAdoptCmd() *cobra.Command {
	var relocate, skipBuild bool
	cmd := &cobra.Command{
		Use:   "adopt <path>",
		Short: "Provision a worktree that another tool created",
		Long: "Provision a worktree made outside wt — plain `git worktree add`, an\n" +
			"agent's own checkout, or one created before this repo was migrated.\n" +
			"With --relocate it is also moved to the canonical path.",
		Args: needArgs(1, "<path>", "wt adopt ../server-oldshape"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			path, err := commands.Adopt(ctx, args[0], relocate,
				commands.SetupOptions{SkipBuild: skipBuild}, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&relocate, "relocate", false, "also move it to the canonical path")
	cmd.Flags().BoolVar(&skipBuild, "skip-build", false, "skip build initialisation")
	return cmd
}

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "migrate <type>/<work>",
		Short:             "Move a worktree to the canonical path",
		Args:              needArgs(1, "<type>/<work>", "wt migrate feat/webkey"),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			path, err := commands.Migrate(ctx, args[0], cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
}
