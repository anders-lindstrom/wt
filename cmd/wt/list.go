package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every worktree of this repository",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.List(ctx, cmd.OutOrStdout())
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show each worktree's branch and whether it is clean",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.Status(ctx, cmd.OutOrStdout())
		},
	}
}
