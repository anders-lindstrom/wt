package main

import (
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
			"works.\n\n" +
			"When the repository is a Superset project and the Superset app is\n" +
			"running, the new worktree is also registered there as a workspace.\n" +
			"`--no-superset` skips that once; SUPERSET_REGISTER=off skips it for\n" +
			"the repository.",
		Example: "  wt new fix/login-crash                # branch, worktree, provisioning\n" +
			"  wt new login-crash                    # the repository's default type\n" +
			"  wt new fix/login-crash --base v2.1    # cut from something else\n" +
			"  wt new spike/idea --no-setup          # the worktree, nothing else\n" +
			"  wt new spike/idea --skip-build --no-superset  # neither build nor Superset",
		Args: needArgs(1, "<type>/<work>", "wt new fix/login-crash"),
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			path, err := commands.New(ctx, args[0], opts, cmd.ErrOrStderr())
			// The path alone goes to stdout so `cd "$(wt new ...)"` works.
			return printLine(cmd, path, err)
		}),
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "branch to cut from (default: the configured main branch)")
	addProvisionFlags(cmd, &opts.SkipBuild, &opts.NoSetup, &opts.NoSuperset)
	return cmd
}
