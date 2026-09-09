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
		Short: "Show what rebasing each worktree onto trunk would do; run, undo, doctor act",
		Long: "Simulate rebasing every worktree onto origin/<trunk> in the object store\n" +
			"and print the outcome: the class, how far behind, the first commit a\n" +
			"rebase would stop at, and whether the repository's declared strategies\n" +
			"resolve it. Nothing is fetched and nothing is changed: run git fetch\n" +
			"first for a current picture.\n" +
			"\n" +
			"The flow is look, act, finish.\n" +
			"  look    wt sync                 this table; read-only\n" +
			"  act     wt sync run <work>...   rebase; safety ref, strategies at each stop, deferred steps;\n" +
			"                                  asks once when more than one worktree is involved (--yes skips)\n" +
			"  finish  push with --force-with-lease; wt sync undo <work> puts every ref back\n" +
			"          wt sync doctor          what a run needs, and --fix / --prune\n" +
			"A contested worktree is refused by run until resume exists: rebase it by hand.\n" +
			"Only Claude sessions are detected in WHO; a Codex session is not seen.\n" +
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
			"is pushed: the last line per worktree is the push command to run.\n\n" +
			"Ctrl-C releases every lock the run holds and kills the step it was\n" +
			"running; a worktree caught mid-rebase is named along with the command\n" +
			"that puts it back.",
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

	var force bool
	undo := &cobra.Command{
		Use:   "undo <work>",
		Short: "Put back every ref the last run on this worktree moved",
		Long: "Find the newest run that touched this worktree's branch and reset every\n" +
			"branch that run rewrote back to its safety ref, restoring a stack as a\n" +
			"whole rather than one branch at a time.\n\n" +
			"Refused, and nothing undone: a checkout involved is dirty, mid-rebase,\n" +
			"or has a Claude session in it; a branch that has moved since the run,\n" +
			"whose commits the reset would discard (--force pins those at a fresh\n" +
			"safety ref and rewinds anyway); a branch with a later run, which has to\n" +
			"be undone first and which --force does not override. Running it again\n" +
			"after it already restored a branch reports that branch already at its\n" +
			"old tip.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.SyncUndo(ctx, args[0], commands.UndoOptions{Force: force}, cmd.OutOrStdout())
		},
	}
	undo.Flags().BoolVar(&force, "force", false, "undo a branch that has moved since the run, pinning its tip first")
	sync.AddCommand(undo)

	var fix, prune bool
	doctor := &cobra.Command{
		Use:   "doctor",
		Short: "Check what a run needs: trunk, declaration, scripts, rerere, hooks, submodules, LFS, Docker, safety refs, locks, rebases",
		Long: "Check what wt sync run needs before the first run in a repository and\n" +
			"after anything changes: origin/<trunk> is fetched, .wt-sync.yaml parses,\n" +
			"every script strategy's run exists and is executable on trunk, no\n" +
			"pre-rebase or post-rewrite hook is active, no submodules or LFS paths\n" +
			"are declared, Docker answers when a deferred step needs it, and nothing\n" +
			"is left behind by an earlier run: no stale safety ref, no expired lock,\n" +
			"no worktree stopped mid-rebase.\n" +
			"\n" +
			"Never fixes anything on its own. --fix turns on rerere.enabled and\n" +
			"removes expired locks; --prune deletes safety refs old runs no longer\n" +
			"need. A finding that would make a run refuse outright (a missing\n" +
			"trunk, declaration or script) makes this command exit non-zero; the\n" +
			"rest is advisory.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.SyncDoctor(ctx, commands.DoctorOptions{Fix: fix, Prune: prune}, cmd.OutOrStdout())
		},
	}
	doctor.Flags().BoolVar(&fix, "fix", false, "turn on rerere.enabled and remove expired locks")
	doctor.Flags().BoolVar(&prune, "prune", false, "delete prunable safety refs")
	sync.AddCommand(doctor)

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
