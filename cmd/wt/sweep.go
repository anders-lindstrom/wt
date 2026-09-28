package main

import (
	"errors"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSweepCmd() *cobra.Command {
	var noFetch, yes, dryRun, asJSON bool
	var expect, quarantineDir string
	var sel selectionFlags
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Delete merged branches, and the unused worktrees on them",
		Long: "Delete the local branches already merged into trunk, remove the\n" +
			"worktrees on such branches that nothing is using, and say what else is\n" +
			"left. A branch is merged when its tip is reachable from origin/<trunk>,\n" +
			"fetched first because merges happen on the remote, or from the local\n" +
			"trunk, so its commits are on trunk. A branch cut and never committed to\n" +
			"counts as merged.\n\n" +
			"A squash or rebase merge rewrites the commits, so git reads the branch\n" +
			"as unmerged for ever. The pull request answers that, and sweep reads it\n" +
			"through `gh`: every row that has one names it, and a worktree is swept\n" +
			"on its strength only when GitHub says merged, the pull request's base is\n" +
			"trunk, and the branch is still at exactly the commit it carried. A\n" +
			"stacked pull request merged into its parent has landed nothing on trunk,\n" +
			"so it never makes a branch deletable. GitHub is asked twice, once for\n" +
			"the plan and once before anything goes, each call bounded at 15\n" +
			"seconds; with GitHub off or unreachable sweep does what it did before,\n" +
			"on git's answer alone.\n\n" +
			"A branch whose upstream is gone or that a worktree has checked out also\n" +
			"counts as merged when every commit is on trunk under another id, found\n" +
			"by git cherry and confirmed byte for byte: a rebase merge whose pull\n" +
			"request carried the rebased tip, or a cherry-pick. A merge or empty\n" +
			"commit on it keeps it, and so does a change trunk has only with other\n" +
			"whitespace.\n\n" +
			"The plan has four parts:\n" +
			"  removed       merged, and its worktree is safe to remove: nothing\n" +
			"                uncommitted (submodules too), no operation in progress,\n" +
			"                no lock whose holder is still running, and no agent\n" +
			"                session in it, idle or busy. The worktree goes\n" +
			"                the way wt remove takes it, ignored files (.env,\n" +
			"                node_modules/) and all, and the branch with it\n" +
			"  deleted       merged, and in use in no worktree\n" +
			"  in use        merged, but its worktree is not safe to remove, and\n" +
			"                each row says why (dirty, a session in it, a held lock),\n" +
			"                or a bisect or rebase there holds it (finish or abort\n" +
			"                that, then sweep again). A worktree left detached at\n" +
			"                a merged tip is listed here too, with the wt remove\n" +
			"                <path> that takes it; one detached at a commit trunk\n" +
			"                lacks is passed over\n" +
			"  kept          its upstream is gone, but trunk lacks its commits\n\n" +
			"Trunk is MAIN_BRANCH, or origin's HEAD when that is not set; with\n" +
			"neither, sweep refuses. Trunk, the branch origin's HEAD names and the\n" +
			"long-lived branches (main, master, develop, development, staging,\n" +
			"production, release*) are never deleted. Remote branches are never\n" +
			"touched; the fetch prunes only origin's remote-tracking refs.\n\n" +
			"It runs only from the main checkout, because it acts on the whole\n" +
			"repository rather than on the worktree you are in. In a terminal it\n" +
			"asks once, for the whole plan; --yes skips the question; without a\n" +
			"terminal it prints the plan and changes nothing. Before anything goes,\n" +
			"everything is read again: a branch that moved or was checked out, and\n" +
			"a worktree that gained a change, a session or a lock while the question\n" +
			"was open, is kept and says so; each worktree is read once more right\n" +
			"before it goes. Each deletion prints the commit the branch was at:\n" +
			"`git branch <name> <commit>` restores its commits, not its upstream\n" +
			"setting.\n\n" +
			"On a terminal, commit subjects are cut to fit its width. Piped, they\n" +
			"are printed whole.\n\n" +
			"--all, --roots or --profile sweep many repositories from anywhere: each\n" +
			"is fetched and planned, a few at a time; every plan with something in\n" +
			"it is printed under its repository's name, the rest on one line; one\n" +
			"question covers them all; then each is swept as it would be alone,\n" +
			"read again first. A repository that cannot be swept is reported, the\n" +
			"rest go ahead, and the run exits non-zero.\n\n" +
			"--json is for a tool driving wt, one repository at a time. With\n" +
			"--dry-run, or without --yes, it prints the plan as one JSON object on\n" +
			"stdout: every row, why it is merged or why it is kept, and a token.\n" +
			"With --yes it sweeps and prints one result object, row by row; the\n" +
			"plan and the progress go to stderr. --expect <token> refuses the sweep,\n" +
			"touching nothing, unless the plan made now has that token. wt schema\n" +
			"sweep-plan and wt schema sweep print the schemas; docs/json.md\n" +
			"explains them.\n\n" +
			"--quarantine <dir> moves each worktree it removes into a folder of its\n" +
			"own in <dir>, named after its directory, instead of deleting it, the\n" +
			"way wt remove --quarantine does; wt restore <dir>/<name> puts one back.\n" +
			"<dir> is a new folder whose parent exists, on the worktrees' volume and\n" +
			"outside the repository; otherwise the sweep refuses before anything\n" +
			"goes. It sweeps one repository. Every sweep also drops the refs that\n" +
			"pinned a quarantine's commits once its folder has been deleted.\n\n" +
			"Each worktree it removes loses its Superset workspace, as with wt remove.\n\n" +
			selectionHelp,
		Example: "  wt sweep --quarantine ../trash/sw  # ask, then move worktrees aside\n" +
			"  wt sweep --all --dry-run    # every repository's plan; change nothing\n" +
			"  wt sweep --roots work --yes # one root's repositories, without asking\n" +
			"  wt sweep --profile api --no-fetch  # a profile's, as last fetched\n" +
			"  wt sweep --yes --json --expect 1:0123abcd  # only the plan a tool read",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			if expect != "" && (!asJSON || !yes) {
				return errors.New("--expect holds a sweep to the plan a tool read: pass it with --yes --json")
			}
			if quarantineDir != "" {
				if sel.selection().Any() {
					return errors.New("--quarantine sweeps one repository: run it in its main checkout")
				}
				abs, err := filepath.Abs(quarantineDir)
				if err != nil {
					return err
				}
				quarantineDir = abs
			}
			if asJSON {
				if sel.selection().Any() {
					return errors.New("--json sweeps one repository: run it in each repository's main checkout")
				}
				return sweepJSON(cmd, commands.SweepOptions{NoFetch: noFetch, Yes: yes, DryRun: dryRun, Expect: expect,
					Quarantine: quarantineDir})
			}
			opts := commands.SweepOptions{NoFetch: noFetch, Yes: yes, DryRun: dryRun,
				Width: terminalWidth(cmd.OutOrStdout()), Quarantine: quarantineDir}
			if sel.selection().Any() {
				all := commands.SweepAllOptions{SweepOptions: opts}
				if !yes && canAsk(cmd) {
					p := newPrompter(cmd.InOrStdin(), cmd.OutOrStdout())
					all.Ask = func(q string) (bool, error) { return p.yesNo(q, false), nil }
				}
				return commands.SweepAll(loadUserWarn(cmd.ErrOrStderr()), sel.selection(), all, cmd.OutOrStdout())
			}
			return withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
				if !yes && canAsk(cmd) {
					opts.Confirm = confirmSweep(newPrompter(cmd.InOrStdin(), cmd.OutOrStdout()))
				}
				return commands.Sweep(ctx, opts, cmd.OutOrStdout())
			})(cmd, args)
		},
	}
	sel.add(cmd, true)
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "compare with origin as last fetched")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "delete and remove without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan and change nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the plan, or with --yes the result, as JSON")
	cmd.Flags().StringVar(&expect, "expect", "", "sweep only if the plan still has this token")
	cmd.Flags().StringVar(&quarantineDir, "quarantine", "",
		"move each worktree into this new folder instead of deleting it")
	cmd.MarkFlagsMutuallyExclusive("yes", "dry-run")
	return cmd
}

// sweepJSON is wt sweep --json: stdout carries one object and nothing else,
// the plan or, with --yes, the result; the plan as wt prints it and the
// progress go to stderr. An error that stops it before it opens the
// repository is still that one object.
func sweepJSON(cmd *cobra.Command, opts commands.SweepOptions) error {
	out, progress := cmd.OutOrStdout(), cmd.ErrOrStderr()
	ctx, err := openContext()
	if !opts.Yes {
		if err != nil {
			commands.SweepPlanFailed(out, err)
			return err
		}
		ctx.WarnTo(progress)
		return commands.SweepPlanJSON(ctx, opts, out, progress)
	}
	journal := commands.NewSweepJournal(out)
	if err != nil {
		journal.Fail(err)
		return err
	}
	ctx.WarnTo(progress)
	opts.Journal = journal
	return commands.Sweep(ctx, opts, progress)
}

// confirmSweep asks once for the whole plan, defaulting to no.
func confirmSweep(p *prompter) func(commands.SweepPlan) (bool, error) {
	return func(plan commands.SweepPlan) (bool, error) {
		return p.yesNo(plan.Question(), false), nil
	}
}
