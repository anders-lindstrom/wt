package main

import (
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newNewCmd() *cobra.Command {
	var opts commands.NewOptions
	var asJSON, dryRun bool
	var expect string
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
			"makes Superset run the project's setup step.\n\n" +
			"--dry-run says what would be created, or why not, and creates nothing.\n" +
			"For a tool driving wt, --dry-run --json prints that plan as one JSON\n" +
			"object with a token; --json prints one result object on stdout, every\n" +
			"side effect with its own outcome, and the progress on stderr; and\n" +
			"--expect <token> refuses, creating nothing, when the base commit, the\n" +
			"name or the configuration is no longer what the plan showed. wt schema\n" +
			"new-plan and wt schema new print their JSON Schemas; docs/json.md\n" +
			"explains them.",
		Example: "  wt new fix/login-crash                # branch, worktree, provisioning\n" +
			"  wt new login-crash --base v2.1        # default type, cut from a tag\n" +
			"  wt new spike/idea --no-setup --dry-run  # say what it would make\n" +
			"  wt new spike/idea --no-build --no-superset  # neither build nor Superset\n" +
			"  wt new fix/x --json --expect 1:0123abcd  # only if the plan still holds",
		Args: needArgs(1, "<type>/<work>", "wt new fix/login-crash"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreate(cmd, args, "new", create{json: asJSON, dryRun: dryRun, expect: expect},
				func(ctx *commands.Context) (string, error) {
					return commands.New(ctx, args[0], opts, cmd.ErrOrStderr())
				},
				func(ctx *commands.Context, w io.Writer) error {
					return commands.NewDryRun(ctx, args[0], opts, w)
				},
				func(ctx *commands.Context, w io.Writer) error {
					return commands.NewPlanJSON(ctx, args[0], opts, w)
				},
				func(ctx *commands.Context, j *commands.CreateJournal, w io.Writer) error {
					return commands.NewJSON(ctx, args[0], opts, expect, j, w)
				})
		},
	}
	cmd.Flags().StringVar(&opts.Base, "base", "", "branch or commit to cut from (default: trunk)")
	addProvisionFlags(cmd, &opts.SkipBuild, &opts.NoSetup, &opts.NoSuperset)
	addCreateFlags(cmd, &asJSON, &dryRun, &expect)
	return cmd
}

// create is how a worktree-creating command was asked to report.
type create struct {
	json, dryRun bool
	expect       string
}

// addCreateFlags declares --dry-run, --json and --expect for `wt new` and
// `wt checkout`.
func addCreateFlags(cmd *cobra.Command, asJSON, dryRun *bool, expect *string) {
	cmd.Flags().BoolVar(dryRun, "dry-run", false, "say what would be created, and create nothing")
	cmd.Flags().BoolVar(asJSON, "json", false, "print one JSON object on stdout, the progress on stderr")
	cmd.Flags().StringVar(expect, "expect", "", "refuse unless the plan still matches this token from --dry-run --json")
}

// runCreate routes `wt new` and `wt checkout` by how they were asked to
// report. Without --json and --dry-run the run is exactly what it always was,
// and its path alone goes to stdout so `cd "$(wt new ...)"` works. With
// --json the repository is opened leniently, so a missing configuration is a
// problem the plan names rather than an error with no object.
func runCreate(cmd *cobra.Command, args []string, command string, how create,
	run func(*commands.Context) (string, error),
	dry func(*commands.Context, io.Writer) error,
	plan func(*commands.Context, io.Writer) error,
	result func(*commands.Context, *commands.CreateJournal, io.Writer) error,
) error {
	switch {
	case cmd.Flags().Changed("expect") && (!how.json || how.dryRun):
		return errors.New("--expect goes with --json, on the run that creates the worktree")
	case !how.json && how.dryRun:
		return withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			return dry(ctx, cmd.OutOrStdout())
		})(cmd, args)
	case !how.json:
		return withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			path, err := run(ctx)
			return printLine(cmd, path, err)
		})(cmd, args)
	}
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	var j *commands.CreateJournal
	if !how.dryRun {
		j = commands.NewCreateJournal(stdout, command)
		defer j.Watch(stderr)()
	}
	cwd, err := os.Getwd()
	if err == nil && cmd.Flags().Changed("expect") && how.expect == "" {
		err = errors.New("--expect needs the token from --dry-run --json")
	}
	var ctx *commands.Context
	if err == nil {
		if ctx = commands.OpenLenient(cwd, stderr); ctx == nil {
			err = commands.ErrNotInRepo
		}
	}
	if err != nil {
		if j != nil {
			j.Fail(err)
		}
		return err
	}
	ctx.WarnTo(stderr)
	if how.dryRun {
		return plan(ctx, stdout)
	}
	return result(ctx, j, stderr)
}
