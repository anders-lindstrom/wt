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
		Short: "Show what a rebase onto trunk would do to each worktree",
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
			"  finish  wt sync resume <work>   continue a rebase run left at a conflict that is yours\n" +
			"          push with --force-with-lease; wt sync undo <work> puts every ref back\n" +
			"          wt sync doctor          what a run needs, and --fix / --prune\n" +
			"A contested worktree is rebased up to the conflict and handed to you with a\n" +
			"plan file; wt sync resume finishes it.\n" +
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
		Example: "  wt sync                   # the table above; reads, changes nothing\n" +
			"  wt sync run login-crash   # act on one of its rows\n" +
			"  wt sync undo login-crash  # put back every ref that run moved\n" +
			"  wt sync doctor            # what a run needs before the first one",
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
			"A stop no strategy resolves is left in place with a plan file naming what\n" +
			"is yours, for wt sync resume to finish; a failed deferred step is reported\n" +
			"as owed and never undoes the rebase.\n\n" +
			"Refused, and never touched: a worktree with tracked changes, one a Claude\n" +
			"session is in (Codex sessions are not detected), class divergent, one an\n" +
			"earlier run already left waiting on you, and any repository whose trunk\n" +
			"declares no .wt-sync.yaml. When more than one worktree would be rebased\n" +
			"you are asked once; --yes skips that. Nothing is pushed: the last line\n" +
			"per worktree is the push command to run.\n\n" +
			"Ctrl-C releases every lock the run holds and kills the step it was\n" +
			"running; a worktree caught mid-rebase is named along with the command\n" +
			"that puts it back.",
		Example: "  wt sync run login-crash                # fetch trunk, then rebase it\n" +
			"  wt sync run login-crash api-tidy       # both, and their stacks\n" +
			"  wt sync run login-crash --no-fetch     # trunk as last fetched\n" +
			"  wt sync run login-crash api-tidy --yes # do not ask first",
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

	resume := &cobra.Command{
		Use:   "resume <work>",
		Short: "Continue the rebase a run left at a conflict that was yours",
		Long: "Pick up the rebase wt sync run left in this worktree. The plan file in\n" +
			".git/worktrees/<name>/wt-sync-plan.md says what landed, what the declared\n" +
			"strategies already resolved and must not be re-opened, and what is yours.\n" +
			"Resolve those, git add them, then run this.\n\n" +
			"Before continuing it checks that nothing is unmerged, that nothing tracked\n" +
			"is changed but unstaged, and that no file a strategy resolved was\n" +
			"hand-merged. Any of those is a refusal that changes nothing: this command\n" +
			"never resets the worktree, because your own work is in it. Then it drives\n" +
			"the rest of the rebase, runs the deferred steps, pins the result ref and\n" +
			"prints the push command. A later conflict that is yours is handed over\n" +
			"again with a fresh plan file.\n\n" +
			"A rebase you finished yourself with git rebase --continue is fine: this\n" +
			"notices and runs only what comes after it. wt sync undo <work> aborts a\n" +
			"handed-over rebase and puts the branch back instead.",
		Example: "  wt sync resume login-crash      # continue what the run handed you\n" +
			"  wt sync resume fix/login-crash  # the same worktree, by branch",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			return commands.SyncResume(ctx, args[0], commands.ResumeOptions{}, cmd.OutOrStdout())
		},
	}
	sync.AddCommand(resume)

	var force bool
	undo := &cobra.Command{
		Use:   "undo <work>",
		Short: "Put back every ref the last run on this worktree moved",
		Long: "Find the newest run that touched this worktree's branch and reset every\n" +
			"branch that run rewrote back to its safety ref, restoring a stack as a\n" +
			"whole rather than one branch at a time. A rebase wt sync run handed over\n" +
			"is aborted and its plan file removed, which puts that branch back.\n\n" +
			"Refused, and nothing undone: a checkout involved is dirty, has a Claude\n" +
			"session in it, or is mid-rebase with no handover from wt sync run; a\n" +
			"branch that has moved since the run, whose commits the reset would\n" +
			"discard (--force pins those at a fresh safety ref and rewinds anyway); a\n" +
			"branch with a later run, which has to be undone first and which --force\n" +
			"does not override. Nothing is aborted until every branch has passed.\n" +
			"Running it again after it already restored a branch reports that branch\n" +
			"already at its old tip.",
		Example: "  wt sync undo login-crash          # back to the safety refs\n" +
			"  wt sync undo login-crash --force  # even if the branch moved since",
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
		Short: "Check what a run needs, and clear up what old runs left behind",
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
		Example: "  wt sync doctor          # what a run needs; changes nothing\n" +
			"  wt sync doctor --fix    # turn on rerere, remove expired locks\n" +
			"  wt sync doctor --prune  # delete safety refs no run needs now",
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
