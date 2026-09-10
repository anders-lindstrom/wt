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
		Long: "Print the configuration as every command sees it: the file's values,\n" +
			"the defaults it did not set, and the main branch detected from origin.\n\n" +
			"With --shell, emit eval-able assignments using the legacy variable\n" +
			"names that the Herdr skills and plugin expect from\n" +
			"load_worktree_config.",
		Example: "  wt config          # the resolved configuration, typed\n" +
			"  wt config --shell  # the same as shell assignments, for eval",
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
