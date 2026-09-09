package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSyncCmd() *cobra.Command {
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Show what rebasing each worktree onto trunk would do",
		Long: "Simulate rebasing every worktree onto origin/<trunk> in the object store\n" +
			"and print the outcome: the class, how far behind, the first commit a\n" +
			"rebase would stop at, and whether the repository's declared strategies\n" +
			"resolve it. Nothing is fetched and nothing is changed: run git fetch\n" +
			"first for a current picture.\n" +
			"\n" +
			"The flow is look, act, finish. This command is the look. Acting on a\n" +
			"row (wt sync run <work>) and backing out (wt sync undo <work>) exist\n" +
			"now. Finishing a contested one (resume) and checking preconditions\n" +
			"(doctor) are not built yet; until they are, resolve a contested row by\n" +
			"rebasing that worktree by hand.\n" +
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

	var noFetch, yes bool
	run := &cobra.Command{
		Use:   "run <work>...",
		Short: "Rebase the named worktrees onto trunk with the declared strategies",
		Long: "Fetch trunk once, then for each named worktree (and the rest of any\n" +
			"stack it belongs to, parents first): pin the old tip under\n" +
			"refs/wt-sync/<branch>/<epoch>, rebase with --no-update-refs --no-gpg-sign,\n" +
			"apply the declared strategy at every stop, and run the deferred steps\n" +
			"once at the end, committing their output when it changes tracked files.\n" +
			"A stop no strategy resolves aborts the rebase and restores the worktree;\n" +
			"a failed deferred step is reported as owed and never undoes the rebase.\n\n" +
			"Refused, and never touched: a worktree with tracked changes, one a Claude\n" +
			"session is in (Codex sessions are not detected), class divergent, class\n" +
			"contested (rebase those by hand; resume is not built yet), and any\n" +
			"repository whose trunk declares no .wt-sync.yaml. When more than one\n" +
			"worktree would be rebased you are asked once; --yes skips that. Nothing\n" +
			"is pushed: the last line per worktree is the push command to run.",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			opts := commands.RunOptions{NoFetch: noFetch, Yes: yes}
			if !yes && isTerminal(os.Stdin) {
				opts.Confirm = confirmRun(cmd.InOrStdin(), cmd.OutOrStdout())
			}
			return commands.SyncRun(ctx, args, opts, cmd.OutOrStdout())
		},
	}
	run.Flags().BoolVar(&noFetch, "no-fetch", false, "rebase onto origin/<trunk> as last fetched")
	run.Flags().BoolVar(&yes, "yes", false, "do not ask before rebasing more than one worktree")
	sync.AddCommand(run)

	undo := &cobra.Command{
		Use:   "undo <work>",
		Short: "Put back every ref the last run on this worktree moved",
		Long: "Find the newest run that touched this worktree's branch and reset every\n" +
			"branch that run rewrote back to its safety ref, restoring a stack as a\n" +
			"whole rather than one branch at a time.\n\n" +
			"Refused, and nothing undone: a checkout involved is dirty, mid-rebase,\n" +
			"or has a Claude session in it. Running it again after it already\n" +
			"restored a branch reports that branch already at its old tip.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.SyncUndo(ctx, args[0], commands.UndoOptions{}, cmd.OutOrStdout())
		},
	}
	sync.AddCommand(undo)
	return sync
}

// confirmRun asks the one question a multi-worktree run gets, defaulting to
// no. The branches have already been listed by the time this runs.
func confirmRun(in io.Reader, out io.Writer) func([]string) (bool, error) {
	return func(works []string) (bool, error) {
		_, _ = fmt.Fprintf(out, "rebase these %d worktrees? [y/N] ", len(works))
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil {
			// EOF on a terminal is ^D: the user declined rather than answered.
			return false, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		}
		return false, nil
	}
}
