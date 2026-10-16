package main

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
)

func newRemoveCmd() *cobra.Command {
	var me bool
	var meAt string
	var yes, dryRun, asJSON, keepSuperset, reap bool
	var force []string
	var moveTo, expect string
	cmd := &cobra.Command{
		Use:     "remove <work>",
		Aliases: []string{"rm"},
		Short:   "Remove a worktree, deleting its branch only when merged",
		Long: "Remove an exactly named worktree: a work name, branch, path, or . for\n" +
			"the current checkout. An ambiguous work name needs its type.\n" +
			"\n" +
			"The plan shows what will happen to the checkout and branch. Merged\n" +
			"branches are deleted; unmerged wt branches are renamed and kept.\n" +
			"A terminal asks for confirmation; --yes skips it. Hooks are not asked.\n" +
			"--dry-run shows the plan. --json plans unless --yes is also given;\n" +
			"--expect <token> with --yes --json applies only that plan.\n" +
			"\n" +
			"Commit or discard changes and finish git operations before removal.\n" +
			"--force bypasses only session, hidden-file and lock checks.\n" +
			"--move-to <dir> moves aside for wt restore; deletion has no restore.\n" +
			"--keep-superset leaves the workspace registration alone.\n" +
			"\n" +
			"Processes still working in it, a dev server or a watcher, are named\n" +
			"and left running. --reap stops them first: SIGTERM, then SIGKILL\n" +
			"after 5s; one that will not stop keeps the worktree. Never stopped, nor\n" +
			"named: agent sessions, anything with a terminal (a dev server started\n" +
			"in a terminal tab too), another user's processes.\n" +
			"\n" +
			"See docs/remove.md for branch decisions, refusal checks and recovery.",
		Example: "  wt remove login-crash --reap     # ask, then stop its dev server\n" +
			"  wt remove . --force=idle-sessions  # the one you are in, past idle ones\n" +
			"  wt remove login-crash --move-to ../trash/lc  # move it aside\n" +
			"  wt remove login-crash --dry-run --json --keep-superset  # a tool's plan\n" +
			"  wt remove login-crash --yes --json --expect 1:0123abcd  # only that plan",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !me && meAt == "" && len(args) != 1 {
				return usagef("needs the worktree to remove, or . for the one " +
					"you are in — for example: wt remove login-crash")
			}
			if (me || meAt != "") && len(args) > 0 {
				return usagef("--me and --me-at name the worktree, so wt remove takes no worktree "+
					"beside them, and %q would be ignored", args[0])
			}
			// pflag reads --force idle-sessions as a bare --force and a
			// worktree: a list after a space would force everything.
			if cmd.Flags().Changed("force") && len(args) == 1 && kit.IsForceList(args[0]) {
				return usagef("%q is --force's list, which goes after an =: --force=%s "+
					"(--force=<list>); a worktree of that name is named by its path or <type>/<work>",
					args[0], args[0])
			}
			if cmd.Flags().Changed("expect") && expect == "" {
				return usagef("--expect needs the token wt remove --dry-run --json printed")
			}
			if expect != "" && (!asJSON || !yes) {
				return usagef("--expect holds a removal to the plan a tool read: pass it with --yes --json")
			}
			forced, err := kit.ParseForce(force)
			if err != nil {
				return usageError{err}
			}
			ctx, err := openContext()
			if err != nil {
				if asJSON {
					retire.RemoveFailedJSON(cmd.OutOrStdout(), !yes, err)
				}
				return err
			}
			opts := retire.RemoveOptions{Force: forced, DryRun: dryRun, Expect: expect, KeepSuperset: keepSuperset,
				Reap: reap}
			if moveTo != "" {
				if opts.Quarantine, err = filepath.Abs(moveTo); err != nil {
					return err
				}
			}
			if asJSON {
				target := retire.RemoveTarget{Path: meAt}
				switch {
				case meAt != "":
				case me:
					target.Path = ctx.Cwd
				default:
					target.Arg = args[0]
				}
				out, progress := cmd.OutOrStdout(), cmd.ErrOrStderr()
				ctx.WarnTo(progress)
				if !yes {
					return retire.RemovePlanJSON(ctx, target, opts, out, progress)
				}
				return retire.RemoveJSON(ctx, target, opts, out, progress)
			}
			if !yes && canAsk(cmd) {
				opts.Confirm = confirmRemoval(newPrompter(cmd.InOrStdin(), cmd.OutOrStdout()))
			}
			if meAt != "" {
				return retire.RemoveAt(ctx, meAt, opts, cmd.OutOrStdout())
			}
			if me {
				return retire.RemoveAt(ctx, ctx.Cwd, opts, cmd.OutOrStdout())
			}
			return retire.Remove(ctx, args[0], opts, cmd.OutOrStdout())
		},
	}
	// . says the same and is what every other command takes; --me stays for
	// the lines already written with it.
	cmd.Flags().BoolVar(&me, "me", false, "remove the worktree you are standing in")
	_ = cmd.Flags().MarkHidden("me")
	cmd.Flags().StringSliceVarP(&force, "force", "f", nil,
		"go past `what`, or bare all: idle-sessions, busy-sessions, "+
			"sessions-unknown, hidden-files, lock")
	cmd.Flags().Lookup("force").NoOptDefVal = "all"
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what removal would do and change nothing")
	cmd.Flags().StringVar(&moveTo, "move-to", "",
		"move it into `dir`, a new folder, instead of deleting it; wt restore <dir> puts it back")
	cmd.Flags().BoolVar(&keepSuperset, "keep-superset", false,
		"leave its Superset workspace; do not ask Superset to delete it")
	cmd.Flags().BoolVar(&reap, "reap", false,
		"stop the processes still working in it before it goes")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the plan, or with --yes the result, as JSON")
	cmd.Flags().StringVar(&expect, "expect", "", "remove only if the plan still has this token")
	cmd.MarkFlagsMutuallyExclusive("yes", "dry-run")
	cmd.Flags().StringVar(&meAt, "me-at", "", "remove the worktree at this path (used by wt_rm_me)")
	_ = cmd.Flags().MarkHidden("me-at")
	return cmd
}

// confirmRemoval asks the question, defaulting to no. The plan has already been
// printed by the time this runs, so the prompt itself stays one line.
func confirmRemoval(p *prompter) func(retire.Plan) (bool, error) {
	return func(retire.Plan) (bool, error) { return p.yesNo("Remove it?", false), nil }
}
