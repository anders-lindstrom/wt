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
	var opts commands.MigrateOptions
	cmd := &cobra.Command{
		Use:   "migrate <type>/<work>",
		Short: "Move a worktree to the canonical path",
		Long: "Move a worktree to the canonical path. The move is git's own, so\n" +
			"commits, stashes, uncommitted changes and ignored files all travel\n" +
			"with it — but tools holding the old absolute path will not follow.\n\n" +
			"Use --dry-run first on a worktree carrying work that matters.",
		Args:              needArgs(1, "<type>/<work>", "wt migrate feat/webkey"),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			path, err := commands.Migrate(ctx, args[0], opts, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show what would happen, change nothing")
	return cmd
}
