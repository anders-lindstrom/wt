package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newPurgeCmd() *cobra.Command {
	var yes, dryRun, asJSON bool
	var expect string
	cmd := &cobra.Command{
		Use:   "purge <dir>",
		Short: "Delete a removed worktree for good: its folder and its pins",
		Long: "Delete, for good, the worktree wt remove --move-to or wt sweep\n" +
			"--move-to moved into <dir>: the refs wt made to pin its commits, then\n" +
			"the folder with everything in it. Once the pins go, commits nothing\n" +
			"else holds are unreachable and git gc takes them; the plan counts\n" +
			"them. A symlink in the folder is deleted, never what it names. It runs\n" +
			"from anywhere: recovery.json names its repository.\n\n" +
			"<dir> is that folder. The branches and backups a sweep pinned under\n" +
			"refs/wt-swept/ are not in one: wt refs purge <run> deletes those.\n\n" +
			"It only ever deletes a folder a removal plainly made, and refuses,\n" +
			"deleting nothing, when <dir> has no recovery.json wt can read,\n" +
			"when the record was written for another folder, or what is in it is\n" +
			"not what the removal moved, or names pins that are not its own or\n" +
			"cannot be read, when a removal or a restore stopped mid-way (wt restore\n" +
			"<dir> settles it first), when git has a worktree registered inside it\n" +
			"or one locked for it, when it is inside a checkout or the git dir,\n" +
			"when it holds anything no removal leaves there (only checkout,\n" +
			"admin, the journal and .DS_Store) or another such folder anywhere in\n" +
			"it, and when it is empty: nothing then says what it was. A worktree\n" +
			"wt restore already put back left only recovery.json and perhaps a\n" +
			"pin: the plan says so, and those go. When the repository's git dir is\n" +
			"gone, the purge goes ahead on the record alone and says so: deleted,\n" +
			"the pins went with it; moved, wt sweep there drops them later.\n\n" +
			"Inside the checkout it deletes the way rm -rf does: into a volume\n" +
			"mounted there, and not past a file flagged immutable. The record is\n" +
			"trusted as wt wrote it; whoever can forge it can delete the folder.\n\n" +
			"The purge reads its plan again holding a lock wt restore takes too,\n" +
			"and goes ahead only while nothing changed. It first replaces\n" +
			"recovery.json with recovery.purging.json — from then on no wt restore\n" +
			"takes it — then deletes each pin only at the commit it read, then what\n" +
			"the plan listed in the folder, its journal last, then the folder.\n" +
			"Stopped anywhere, running it again finishes it.\n\n" +
			"What it would delete is printed first. In a terminal you are asked\n" +
			"once; --yes skips the question, and with no terminal and no --yes it\n" +
			"refuses. --dry-run prints the plan and stops.\n\n" +
			"--json is for a tool. With --dry-run, or without --yes, it prints the\n" +
			"plan as one object on stdout, with a token, and deletes nothing; with\n" +
			"--yes it purges and prints one result object, also when interrupted.\n" +
			"--expect <token> refuses the purge unless the plan made now has that\n" +
			"token. wt schema lists the plan's schema and the result's beside this\n" +
			"command line; docs/json.md explains them.",
		Example: "  wt purge ../trash/lc            # say what goes, then ask\n" +
			"  wt purge ../trash/lc --dry-run  # what it would delete\n" +
			"  wt purge ../trash/lc --yes      # no question\n" +
			"  wt purge ../trash/lc --json     # the plan, and its token\n" +
			"  wt purge ../trash/lc --yes --json --expect 1:0123abcd",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("expect") && expect == "" {
				return errors.New("--expect needs the token wt purge --json printed")
			}
			if expect != "" && (!asJSON || !yes) {
				return errors.New("--expect holds a purge to the plan a tool read: pass it with --yes --json")
			}
			if asJSON {
				out, progress := cmd.OutOrStdout(), cmd.ErrOrStderr()
				if !yes {
					return commands.QuarantinePurgePlanJSON(args[0], out, progress)
				}
				return commands.QuarantinePurgeJSON(args[0], commands.PurgeOptions{Expect: expect}, out, progress)
			}
			opts := commands.PurgeOptions{DryRun: dryRun}
			if !yes && !dryRun {
				if canAsk(cmd) {
					p := newPrompter(cmd.InOrStdin(), cmd.OutOrStdout())
					opts.Confirm = func() bool { return p.yesNo("Delete it for good?", false) }
				} else {
					opts.NoTerminal = true
				}
			}
			return commands.QuarantinePurge(args[0], opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what the purge would delete and delete nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the plan, or with --yes the result, as JSON")
	cmd.Flags().StringVar(&expect, "expect", "", "purge only if the plan still has this token")
	cmd.MarkFlagsMutuallyExclusive("yes", "dry-run")
	return cmd
}
