package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

func newSetupCmd() *cobra.Command {
	var skipBuild, noCache bool
	var source string
	cmd := &cobra.Command{
		Use:   "setup [<from-dir>]",
		Short: "Provision the current worktree from a source checkout",
		Long: "Provision the worktree you are standing in: copy the developer config\n" +
			"the repository declares, clone its WORKTREE_CACHE_DIRS copy-on-write,\n" +
			"run its bin/worktree/provision.sh if it has one, initialise\n" +
			"submodules, run build initialisation. provision.sh and the build get\n" +
			"WT_PORT (see wt port), WT_BRANCH and WT_WORK in their environment.\n\n" +
			"A cache such as node_modules is cloned only where the filesystem can\n" +
			"share its blocks (APFS, Btrfs, XFS); elsewhere it is skipped with a\n" +
			"line saying why, never copied. One already in the worktree is kept.\n\n" +
			"<from-dir> is where the developer config and caches come from, and\n" +
			"defaults to the repository's main checkout. Run this to finish a\n" +
			"worktree whose provisioning failed, or to provision one made by hand.\n\n" +
			"--source names what ran setup: superset today, and any tool that\n" +
			"provisions worktrees through wt tomorrow. Setup prints it and does\n" +
			"nothing else with it yet; it is where a source's own steps before or\n" +
			"after provisioning will hang.",
		Example: "  wt setup                    # provision the worktree you are in\n" +
			"  wt setup ../myrepo          # take developer config from there instead\n" +
			"  wt setup --no-build         # everything except build initialisation\n" +
			"  wt setup --no-cache         # without cloning the build caches\n" +
			"  wt setup --source superset  # name what ran it; changes nothing yet",
		Args: cobra.MaximumNArgs(1),
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *kit.Context) error {
			opts := commands.SetupOptions{SkipBuild: skipBuild, NoCache: noCache, Source: source}
			if len(args) == 1 {
				opts.SourceDir = args[0]
			}
			return commands.Setup(ctx, ctx.Cwd, opts, cmd.OutOrStdout())
		}),
	}
	addProvisionFlags(cmd, &skipBuild, &noCache, nil, nil)
	cmd.Flags().StringVar(&source, "source", "", "what ran setup, such as superset; printed, changes nothing yet")
	return cmd
}

// addProvisionFlags declares --no-build and --no-cache for every command that
// provisions a worktree, and --no-setup and --no-superset for the three that create one:
// `wt new`, `wt checkout` and `wt pr checkout`. --skip-build is the old
// spelling of --no-build, kept working and out of help.
func addProvisionFlags(cmd *cobra.Command, skipBuild, noCache, noSetup, noSuperset *bool) {
	cmd.Flags().BoolVar(skipBuild, "no-build", false, "provision, but skip build initialisation")
	cmd.Flags().BoolVar(noCache, "no-cache", false,
		"provision, but do not clone WORKTREE_CACHE_DIRS from the source checkout")
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
