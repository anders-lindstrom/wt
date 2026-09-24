package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSetupCmd() *cobra.Command {
	var skipBuild bool
	var source string
	cmd := &cobra.Command{
		Use:   "setup [<from-dir>]",
		Short: "Provision the current worktree from a source checkout",
		Long: "Provision the worktree you are standing in: copy the developer config\n" +
			"the repository declares, run its bin/worktree/provision.sh if it has\n" +
			"one, initialise submodules, run build initialisation.\n\n" +
			"<from-dir> is where the developer config is copied from, and\n" +
			"defaults to the repository's main checkout. Run this to finish a\n" +
			"worktree whose provisioning failed, or to provision one made by hand.\n\n" +
			"--source names what ran setup: superset today, and any tool that\n" +
			"provisions worktrees through wt tomorrow. Setup prints it and does\n" +
			"nothing else with it yet; it is where a source's own steps before or\n" +
			"after provisioning will hang.",
		Example: "  wt setup                    # provision the worktree you are in\n" +
			"  wt setup ../myrepo          # take developer config from there instead\n" +
			"  wt setup --no-build         # everything except build initialisation\n" +
			"  wt setup --source superset  # name what ran it; changes nothing yet",
		Args: cobra.MaximumNArgs(1),
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			opts := commands.SetupOptions{SkipBuild: skipBuild, Source: source}
			if len(args) == 1 {
				opts.SourceDir = args[0]
			}
			return commands.Setup(ctx, ctx.Cwd, opts, cmd.OutOrStdout())
		}),
	}
	addProvisionFlags(cmd, &skipBuild, nil, nil)
	cmd.Flags().StringVar(&source, "source", "", "what ran setup, such as superset; printed, changes nothing yet")
	return cmd
}

// addProvisionFlags declares --no-build for every command that provisions a
// worktree, and --no-setup and --no-superset for the three that create one:
// `wt new`, `wt checkout` and `wt pr checkout`. --skip-build is the old
// spelling of --no-build, kept working and out of help.
func addProvisionFlags(cmd *cobra.Command, skipBuild, noSetup, noSuperset *bool) {
	cmd.Flags().BoolVar(skipBuild, "no-build", false, "provision, but skip build initialisation")
	cmd.Flags().BoolVar(skipBuild, "skip-build", false, "the old spelling of --no-build")
	_ = cmd.Flags().MarkHidden("skip-build")
	if noSetup != nil {
		cmd.Flags().BoolVar(noSetup, "no-setup", false, "create the worktree without provisioning it")
	}
	if noSuperset != nil {
		cmd.Flags().BoolVar(noSuperset, "no-superset", false,
			"do not register the worktree as a Superset workspace")
	}
}
