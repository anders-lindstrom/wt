package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSetupCmd() *cobra.Command {
	var skipBuild bool
	var source string
	cmd := &cobra.Command{
		Use:   "setup <source-dir>",
		Short: "Provision the current worktree from a source checkout",
		Long: "Provision the worktree you are standing in: copy the developer config\n" +
			"the repository declares, run its bin/worktree/provision.sh if it has\n" +
			"one, initialise submodules, run build initialisation.\n\n" +
			"<source-dir> is where the developer config is copied from, and\n" +
			"defaults to the repository's main checkout. Run this to finish a\n" +
			"worktree whose provisioning failed, or to provision one made by hand.\n\n" +
			"--source names what ran setup, such as superset. Setup prints it and\n" +
			"does nothing else with it yet.",
		Example: "  wt setup                    # provision the worktree you are in\n" +
			"  wt setup ../myrepo          # take developer config from there instead\n" +
			"  wt setup --skip-build       # everything except build initialisation\n" +
			"  wt setup --source superset  # name what ran it; changes nothing yet",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			opts := commands.SetupOptions{SkipBuild: skipBuild, Source: source}
			if len(args) == 1 {
				opts.SourceDir = args[0]
			}
			target, err := os.Getwd()
			if err != nil {
				return err
			}
			return commands.Setup(ctx, target, opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&skipBuild, "skip-build", false, "skip build initialisation")
	cmd.Flags().StringVar(&source, "source", "", "what ran setup, such as superset; printed, changes nothing yet")
	return cmd
}
