package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newNewCmd() *cobra.Command {
	var opts commands.NewOptions
	cmd := &cobra.Command{
		Use:   "new <type>/<work>",
		Short: "Create a worktree and its branch, then provision it",
		Long: "Create the branch <type>_wt/<work> from the repository's main branch,\n" +
			"put its worktree at the canonical path, and provision it: developer\n" +
			"config copied in, the repository's provision.sh run, submodules\n" +
			"initialised, build initialised.\n\n" +
			"A bare <work> takes the repository's default type, and a name that\n" +
			"starts with a type carries it: `wt new fix_login-crash` also creates\n" +
			"fix_wt/login-crash.\n\n" +
			"The path alone goes to stdout, so `cd \"$(wt new fix/login-crash)\"`\n" +
			"works.",
		Example: "  wt new fix/login-crash                # branch, worktree, provisioning\n" +
			"  wt new login-crash                    # the repository's default type\n" +
			"  wt new fix/login-crash --base v2.1    # cut from something else\n" +
			"  wt new spike/idea --no-setup          # the worktree, nothing else\n" +
			"  wt new fix/login-crash --skip-build   # provision, but do not build",
		Args: needArgs(1, "<type>/<work>", "wt new fix/login-crash"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			path, err := commands.New(ctx, args[0], opts, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			// The path alone goes to stdout so `cd "$(wt new ...)"` works.
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "branch to cut from (default: the configured main branch)")
	cmd.Flags().BoolVar(&opts.SkipBuild, "skip-build", false, "skip build initialisation")
	cmd.Flags().BoolVar(&opts.NoSetup, "no-setup", false, "create the worktree without provisioning it")
	return cmd
}
