package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSetupCmd() *cobra.Command {
	var skipBuild bool
	cmd := &cobra.Command{
		Use:   "setup <source-dir>",
		Short: "Provision the current worktree from a source checkout",
		Long: "Copy developer config from <source-dir>, run the repository's\n" +
			"bin/worktree/provision.sh if it has one, initialise submodules and\n" +
			"run build initialisation.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			opts := commands.SetupOptions{SkipBuild: skipBuild}
			if len(args) == 1 {
				opts.Source = args[0]
			}
			target, err := os.Getwd()
			if err != nil {
				return err
			}
			return commands.Setup(ctx, target, opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&skipBuild, "skip-build", false, "skip build initialisation")
	return cmd
}
