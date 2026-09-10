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
		Long: "Print every worktree of this repository: its work name, its branch and\n" +
			"its path. A worktree not at the path this layout gives it is marked,\n" +
			"and the legend says what to do about it.",
		Example: "  wt list   # work name, branch and path for every worktree\n" +
			"  wt ls     # the same, for the impatient",
		Args: cobra.NoArgs,
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
		Long: "Print each worktree's branch and whether its checkout is clean, dirty\n" +
			"or unreadable — the one question `wt list` does not answer.",
		Example: "  wt status               # branch and state for every worktree\n" +
			"  wt status | grep dirty  # only the ones with uncommitted changes",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.Status(ctx, cmd.OutOrStdout())
		},
	}
}
