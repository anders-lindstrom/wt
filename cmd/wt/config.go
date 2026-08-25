package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newConfigCmd() *cobra.Command {
	var shell bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print the repository's resolved worktree configuration",
		Long: "Print the resolved configuration. With --shell, emit eval-able\n" +
			"assignments using the legacy variable names that the Herdr skills\n" +
			"and plugin expect from load_worktree_config.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.Config(ctx, shell, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&shell, "shell", false, "emit eval-able shell assignments")
	return cmd
}
