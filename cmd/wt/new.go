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
		Long: "Create the branch <type>_wt/<work> from the repository's trunk,\n" +
			"put its worktree at the canonical path, and provision it: developer\n" +
			"config copied in, the repository's provision.sh run, submodules\n" +
			"initialised, build initialised.\n\n" +
			"A bare <work> takes the repository's default type, and a name that\n" +
			"starts with a type carries it: `wt new fix_login-crash` also creates\n" +
			"fix_wt/login-crash.\n\n" +
			"The path alone goes to stdout, so `cd \"$(wt new fix/login-crash)\"`\n" +
			"works.\n\n" +
			"Once you have opted in with `wt config set superset true`, and this\n" +
			"repository is a Superset project with the app running, the new\n" +
			"worktree is also registered there as a workspace. `--no-superset`\n" +
			"skips that once; SUPERSET_REGISTER=off skips it for the repository;\n" +
			"and `--no-setup` skips it too, because registering a new workspace\n" +
			"makes Superset run the project's setup step.",
		Example: "  wt new fix/login-crash                # branch, worktree, provisioning\n" +
			"  wt new login-crash                    # the repository's default type\n" +
			"  wt new fix/login-crash --base v2.1    # cut from something else\n" +
			"  wt new spike/idea --no-setup          # the worktree, nothing else\n" +
			"  wt new spike/idea --no-build --no-superset  # neither build nor Superset",
		Args: needArgs(1, "<type>/<work>", "wt new fix/login-crash"),
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			path, err := commands.New(ctx, args[0], opts, cmd.ErrOrStderr())
			// The path alone goes to stdout so `cd "$(wt new ...)"` works.
			return printLine(cmd, path, err)
		}),
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "branch or commit to cut from (default: trunk)")
	addProvisionFlags(cmd, &opts.SkipBuild, &opts.NoSetup, &opts.NoSuperset)
	return cmd
}
