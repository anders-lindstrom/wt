package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newRestoreCmd() *cobra.Command {
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "restore <dir>",
		Short: "Put back a removed worktree from the folder it was moved to",
		Long: "Put back the worktree that wt remove --move-to <dir> or wt sweep\n" +
			"--move-to moved into <dir>, from the recovery.json there: its admin\n" +
			"dir goes back under .git/worktrees, then its checkout, then the\n" +
			"removal's git lock comes off and the refs that pinned its commits\n" +
			"go. Parent folders tidied away meanwhile are made again. It runs from\n" +
			"anywhere: the record names its repository. A worktree removed without\n" +
			"--move-to was deleted: there is no folder, and nothing to put back.\n\n" +
			"The branch comes back too, never under a name that has gone:\n" +
			"  still there at its commit    nothing to do\n" +
			"  kept under another name      renamed back, its config with it\n" +
			"  deleted, and the name free   made again at its commit, with the\n" +
			"                               config the removal recorded\n" +
			"  its name holds another       HEAD names the kept branch, if that is\n" +
			"  commit now                   still at the commit\n" +
			"  in use elsewhere, or moved   no branch is touched; HEAD is detached\n" +
			"                               at the commit it had\n" +
			"In use means another worktree has it checked out, is rebasing it, or\n" +
			"started a bisect from it; that is read again right before the worktree\n" +
			"is registered. A removal that stopped before the worktree was\n" +
			"unregistered left it in use: restore only unlocks it.\n\n" +
			"It refuses, changing nothing, when the checkout's path or the admin\n" +
			"dir's is taken by something else, when either would come back from\n" +
			"another volume, when a deleted branch's commit is no longer in the\n" +
			"repository, or when wt purge began deleting it. Every\n" +
			"step is written to recovery.json before and after it runs, so a\n" +
			"restore — or a removal — stopped anywhere is finished by running\n" +
			"wt restore <dir> again. A worktree locked for a removal into <dir>\n" +
			"with no recovery.json was stopped before anything moved: restore\n" +
			"unlocks it. The folder itself is left as it is, empty but for\n" +
			"recovery.json; wt purge <dir> deletes it. With the\n" +
			"Superset integration on, the worktree is registered there again,\n" +
			"the way wt new registers one.\n\n" +
			"--dry-run says what it would do and changes nothing. --json prints one\n" +
			"object on stdout: with --dry-run the plan, otherwise the result; the\n" +
			"human output goes to stderr. wt schema restore-plan and wt schema\n" +
			"restore print their schemas; docs/json.md explains them.",
		Example: "  wt restore ../trash/lc            # put it back, branch and all\n" +
			"  wt restore ../trash/lc --dry-run  # what it would do to the branch\n" +
			"  wt restore ../trash/lc --json     # for a tool: one object on stdout",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A repository is only needed for a folder with no record; one
			// with a record names its own.
			ctx, err := openContext()
			if err != nil {
				ctx = nil
			} else {
				ctx.WarnTo(cmd.ErrOrStderr())
			}
			opts := commands.RestoreOptions{DryRun: dryRun}
			if asJSON {
				return commands.RestoreJSON(ctx, args[0], opts, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			return commands.Restore(ctx, args[0], opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "say what it would do and change nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the plan, or the result, as JSON")
	return cmd
}
