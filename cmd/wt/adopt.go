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
			"agent's own checkout, or one from before this repository used wt. It\n" +
			"has to be a worktree of this repository already; wt only provisions it.\n\n" +
			"It is left where it is unless --relocate is given, because a path\n" +
			"another tool stored is a path that tool still expects to find.",
		Example: "  wt adopt ../myrepo-login-crash             # provision it where it is\n" +
			"  wt adopt ../myrepo-login-crash --relocate  # and move it into place\n" +
			"  wt adopt . --skip-build                    # this one, without a build",
		Args: needArgs(1, "<path>", "wt adopt ../myrepo-login-crash"),
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
