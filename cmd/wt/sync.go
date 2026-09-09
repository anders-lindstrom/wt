package main

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Show what rebasing each worktree onto trunk would do",
		Long: "Simulate rebasing every worktree onto origin/<trunk> in the object store\n" +
			"and print the outcome: the class, how far behind, the first commit a\n" +
			"rebase would stop at, and whether the repository's declared strategies\n" +
			"resolve it. Nothing is fetched and nothing is changed: run git fetch\n" +
			"first for a current picture.\n" +
			"\n" +
			"The flow is look, act, finish. This command is the look. Acting on a\n" +
			"row (wt sync run <work>), finishing a contested one (resume), backing\n" +
			"out (undo) and checking preconditions (doctor) are not built yet; until\n" +
			"they are, act on a row by rebasing that worktree by hand.\n" +
			"\n" +
			"Classes:\n" +
			"  clean      rebases without a conflict\n" +
			"  recipe     every conflict is claimed by a strategy in .wt-sync.yaml;\n" +
			"             a run would complete on its own\n" +
			"  contested  some conflict is nobody's; a run would stop there and\n" +
			"             hand you the files marked \u2717\n" +
			"  divergent  branch and trunk moved apart structurally; never rebased\n" +
			"             automatically\n" +
			"  stale      nothing ahead of trunk; skipped\n" +
			"  current    already on trunk; not printed\n" +
			"\n" +
			"STOP is the first commit a rebase would stop at and the files in\n" +
			"conflict there. WHO names an agent session sitting in the worktree:\n" +
			"leave those alone. NOTE is advisory and never changes the class.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Lenient: a repository without worktree.conf still has worktrees
			// worth reporting on, and the trunk name falls back to origin/HEAD.
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			ctx := commands.OpenLenient(cwd, cmd.ErrOrStderr())
			if ctx == nil {
				return errors.New("not inside a git repository")
			}
			return commands.Sync(ctx, cmd.OutOrStdout())
		},
	}
}
