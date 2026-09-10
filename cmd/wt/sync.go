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
		Use:   "sync [<work>]",
		Short: "Show what a rebase onto trunk would do to each worktree",
		Long: "Simulate rebasing every worktree onto origin/<trunk> in the object store,\n" +
			"applying the repository's declared strategies at every stop, and print\n" +
			"the outcome grouped by what to do about it. Nothing is fetched and\n" +
			"nothing is changed: run git fetch first for a current picture.\n" +
			"\n" +
			"The flow is look, act, finish.\n" +
			"  look    wt sync                 every worktree, grouped; read-only\n" +
			"          wt sync <work>          one worktree in full: every stop, file and key\n" +
			"  act     wt sync run <work>...   rebase; safety ref, strategies at each stop, deferred steps;\n" +
			"                                  asks once when more than one worktree is involved or a\n" +
			"                                  session is idle in one (--yes skips)\n" +
			"  finish  wt sync resume <work>   continue a rebase run left at a conflict that is yours\n" +
			"          push with --force-with-lease; wt sync undo <work> puts every ref back\n" +
			"          wt sync doctor          what a run needs, and --fix / --prune\n" +
			"Only Claude sessions are detected; a Codex session is not seen.\n" +
			"\n" +
			"Groups:\n" +
			"  ready      clean or recipe, with nothing in the way: wt sync run <work>\n" +
			"  needs you  contested, divergent, dirty, handed over, or not assessed\n" +
			"  skipped    a busy session is in it, nothing is ahead of trunk, or no branch\n" +
			"\n" +
			"Classes:\n" +
			"  clean      rebases without a conflict\n" +
			"  recipe     every conflict at every stop is claimed by a strategy in\n" +
			"             .wt-sync.yaml; a run completes on its own. recipe? means a\n" +
			"             script owns a path and the replay could not be carried past\n" +
			"             it: a run may still stop later, and hands you a plan if it does\n" +
			"  contested  a conflict somewhere in the replay is nobody's; a run rebases\n" +
			"             up to it, stages what the strategies did resolve, and leaves a\n" +
			"             plan file \u2014 finish it and wt sync resume <work>. A branch\n" +
			"             with a stack above it is put back instead, so a stack is\n" +
			"             never half-applied\n" +
			"  divergent  branch and trunk moved apart structurally; never rebased\n" +
			"             automatically\n" +
			"  stale      nothing ahead of trunk; skipped\n" +
			"  current    already on trunk; not printed\n" +
			"\n" +
			"Under each worktree: the stop that decides the class \u2014 the first one\n" +
			"that is yours, or the first of a run that resolves throughout \u2014 with its\n" +
			"files marked \u2713 resolved or \u2717 yours, then anything else worth knowing,\n" +
			"every list cut to a count. wt sync <work> prints the lists, one item per\n" +
			"line. A session named on a row is a Claude session in that worktree: a busy\n" +
			"one keeps wt sync run off it. One marked (idle) is waiting for its person;\n" +
			"wt sync run names it, asks first, and ends with a wt: line to pass on to\n" +
			"it. +N counts the other sessions there. The lines under a row are advisory\n" +
			"and never change the class.",
		Example: "  wt sync                   # every worktree, grouped; reads, changes nothing\n" +
			"  wt sync login-crash       # that worktree in full\n" +
			"  wt sync run login-crash   # act on it\n" +
			"  wt sync undo login-crash  # put back every ref that run moved\n" +
			"  wt sync doctor            # what a run needs before the first one",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if len(args) == 1 {
				return commands.SyncWorktree(ctx, args[0], cmd.OutOrStdout())
			}
			return commands.Sync(ctx, cmd.OutOrStdout())
		},
	}

	var noFetch, yes, push, noPush bool
	run := &cobra.Command{
		Use:   "run <work>...",
		Short: "Rebase the named worktrees onto trunk with the declared strategies",
		Long: "Fetch trunk once, then for each named worktree (and the rest of any\n" +
			"stack it belongs to, parents first): pin the old tip under\n" +
			"refs/wt-sync/<branch>/<epoch>, rebase with --no-update-refs --no-gpg-sign,\n" +
			"apply the declared strategy at every stop, and run the deferred steps\n" +
			"once at the end, committing their output when it changes tracked files.\n" +
			"A stop no strategy resolves is left in place, with what the strategies did\n" +
			"resolve staged and a plan file naming what is yours, for wt sync resume to\n" +
			"finish. A branch with a stack above it in the same run is put back instead,\n" +
			"so a stack is never half-applied. A failed deferred step is reported as\n" +
			"owed and never undoes the rebase.\n\n" +
			"Refused, and never touched: a worktree with tracked changes, one a busy\n" +
			"Claude session is in (Codex sessions are not detected), class divergent,\n" +
			"one an earlier run already left waiting on you, and any repository whose\n" +
			"trunk declares no .wt-sync.yaml. You are asked once when more than one\n" +
			"worktree would be rebased or a Claude session is idle in one; --yes skips\n" +
			"that. The idle session is named before anything moves either way, and a\n" +
			"finished rebase ends with a wt: line to pass on to it. Sessions are listed\n" +
			"again at the lock: one busy by then, or new since, is refused. The session\n" +
			"wt itself runs under is not counted.\n\n" +
			"A worktree whose rebase finished with nothing owed is pushed at the end\n" +
			"with --force-with-lease --force-if-includes, which refuses to overwrite\n" +
			"commits on origin the branch has not seen. On a terminal you are asked\n" +
			"once and Enter pushes; --push pushes without asking, and --no-push, or a\n" +
			"run with no terminal, prints the push command instead.\n\n" +
			"Ctrl-C releases every lock the run holds and kills the step it was\n" +
			"running; a worktree caught mid-rebase is named along with the command\n" +
			"that puts it back.",
		Example: "  wt sync run login-crash                # fetch trunk, rebase, offer the push\n" +
			"  wt sync run login-crash api-tidy --yes # both, their stacks, not asked first\n" +
			"  wt sync run login-crash --no-fetch     # trunk as last fetched\n" +
			"  wt sync run login-crash --push         # push when done, without asking\n" +
			"  wt sync run login-crash --no-push      # print the push command instead",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			opts := commands.RunOptions{NoFetch: noFetch, Yes: yes, Push: pushMode(push, noPush)}
			if isTerminal(os.Stdin) {
				if !yes {
					opts.Confirm = confirmAsk(cmd.InOrStdin(), cmd.OutOrStdout(), "rebase")
				}
				opts.ConfirmPush = confirmPush(cmd.InOrStdin(), cmd.OutOrStdout())
			}
			return commands.SyncRun(ctx, args, opts, cmd.OutOrStdout())
		},
	}
	run.Flags().BoolVar(&noFetch, "no-fetch", false, "rebase onto origin/<trunk> as last fetched")
	run.Flags().BoolVar(&yes, "yes", false, "do not ask first: several worktrees, or an idle session in one")
	run.Flags().BoolVar(&push, "push", false, "push the worktrees that finish, without asking")
	run.Flags().BoolVar(&noPush, "no-push", false, "neither push nor ask; print the push command")
	run.MarkFlagsMutuallyExclusive("push", "no-push")
	sync.AddCommand(run)

	resume := &cobra.Command{
		Use:   "resume <work>",
		Short: "Continue the rebase a run left at a conflict that was yours",
		Long: "Pick up the rebase wt sync run left in this worktree. The plan file in\n" +
			".git/worktrees/<name>/wt-sync-plan.md says what landed, what the declared\n" +
			"strategies already resolved and must not be re-opened, and what is yours.\n" +
			"Resolve those, git add them, then run this.\n\n" +
			"Before continuing it checks that the rebase in progress is the one the run\n" +
			"left, that nothing is unmerged, that nothing tracked is changed but\n" +
			"unstaged, that no file a strategy resolved was hand-merged, and that no\n" +
			"Claude session is in the worktree. Any of those is a refusal that changes\n" +
			"nothing: this command never resets the worktree, because your own work is\n" +
			"in it, and a file a strategy owns is never yours to merge: that refusal\n" +
			"points at wt sync undo. Then it drives the rest of the rebase, runs the\n" +
			"deferred steps, pins the result ref and ends with the push, asked or not\n" +
			"as for wt sync run (--push, --no-push). A later conflict that is yours is\n" +
			"handed over again with a fresh plan file.\n\n" +
			"Carrying on yourself with git rebase --continue is fine: a later stop you\n" +
			"left it at goes through the strategies, and a rebase you finished runs\n" +
			"only what comes after it, naming the strategy-resolved files it could not\n" +
			"re-check. Ctrl-C leaves the rebase and its plan as they are; run this\n" +
			"again. wt sync undo <work> aborts a handed-over rebase and puts the branch\n" +
			"back instead.",
		Example: "  wt sync resume login-crash            # continue what the run handed you\n" +
			"  wt sync resume fix/login-crash        # the same worktree, by branch\n" +
			"  wt sync resume login-crash --push     # then push, without asking\n" +
			"  wt sync resume login-crash --no-push  # then print the push command",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			opts := commands.ResumeOptions{Push: pushMode(push, noPush)}
			if isTerminal(os.Stdin) {
				opts.ConfirmPush = confirmPush(cmd.InOrStdin(), cmd.OutOrStdout())
			}
			return commands.SyncResume(ctx, args[0], opts, cmd.OutOrStdout())
		},
	}
	// run's variables: only one of the two commands parses flags in any
	// one invocation.
	resume.Flags().BoolVar(&push, "push", false, "push when done, without asking")
	resume.Flags().BoolVar(&noPush, "no-push", false, "neither push nor ask; print the push command")
	resume.MarkFlagsMutuallyExclusive("push", "no-push")
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
			"does not override. Nothing is aborted until every branch has passed, and\n" +
			"every abort runs before any branch is reset. If it stops partway it reports\n" +
			"what it did put back; a handover aborted but not rewound says where the\n" +
			"branch still is.\n" +
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
			"no worktree stopped mid-rebase. A worktree a run handed over is not a\n" +
			"leftover: the plan row names it, with wt sync resume and wt sync undo.\n" +
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

// confirmAsk asks the one question an acting command gets before it changes
// anything, defaulting to no. What it is about has been printed by then.
func confirmAsk(in io.Reader, out io.Writer, verb string) func([]string) (bool, error) {
	return func(works []string) (bool, error) {
		_, _ = fmt.Fprintf(out, "%s %s? [y/N] ", verb, strings.Join(works, ", "))
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

// confirmPush asks the question a run or a resume ends with. Unlike the
// question before a rebase it defaults to yes: what it pushes finished with
// nothing owed, and the lease still refuses to overwrite commits on origin
// the branch never saw.
func confirmPush(in io.Reader, out io.Writer) func([]string) (bool, error) {
	return func(works []string) (bool, error) {
		_, _ = fmt.Fprintf(out, "push %s with --force-with-lease? [Y/n] ", strings.Join(works, ", "))
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil {
			// ^D declines, as it does for the rebase question.
			return false, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "y", "yes":
			return true, nil
		}
		return false, nil
	}
}

// pushMode is --push and --no-push as one choice; cobra has already refused
// the two together.
func pushMode(push, noPush bool) commands.PushMode {
	switch {
	case push:
		return commands.PushAlways
	case noPush:
		return commands.PushNever
	}
	return commands.PushAsk
}
