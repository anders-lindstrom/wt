package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands/admin"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
)

func newAdoptCmd() *cobra.Command {
	var relocate, skipBuild, noCache bool
	cmd := &cobra.Command{
		Use:   "adopt <path>",
		Short: "Provision a worktree that another tool created",
		Long: "Provision a worktree made outside wt — plain `git worktree add`, an\n" +
			"agent's own checkout, or one from before this repository used wt. It\n" +
			"has to be a worktree of this repository already; wt only provisions it.\n\n" +
			"It is left where it is unless --relocate is given, because a path\n" +
			"another tool stored is a path that tool still expects to find.",
		Example: "  wt adopt ../myrepo-login-crash             # provision it where it is\n" +
			"  wt adopt ../myrepo-login-crash --relocate  # and move it into place\n" +
			"  wt adopt . --no-build                      # this one, without a build\n" +
			"  wt adopt . --no-cache                      # without cloning the caches",
		Args: needArgs(1, "<path>", "wt adopt ../myrepo-login-crash"),
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *kit.Context) error {
			path, err := admin.Adopt(ctx, args[0], relocate,
				provision.SetupOptions{SkipBuild: skipBuild, NoCache: noCache}, cmd.ErrOrStderr())
			return printLine(cmd, path, err)
		}),
	}
	cmd.Flags().BoolVar(&relocate, "relocate", false, "also move it to the canonical path")
	addProvisionFlags(cmd, &skipBuild, &noCache, nil, nil)
	return cmd
}
