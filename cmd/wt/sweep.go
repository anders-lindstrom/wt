package main

import (
	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSweepCmd() *cobra.Command {
	var noFetch, yes bool
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Delete merged branches, and the worktrees nothing is using on them",
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
			"The plan has four parts:\n" +
			"  removed       merged, and its worktree is safe to remove: nothing\n" +
			"                uncommitted, no lock whose holder is still running, and\n" +
			"                no agent session in it, idle or busy. The worktree goes\n" +
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
			"was open, is kept and says so. Each deletion prints the commit the\n" +
			"branch was at: `git branch <name> <commit>` restores its commits, not\n" +
			"its upstream setting.\n\n" +
			"On a terminal, commit subjects are cut to fit its width. Piped, they\n" +
			"are printed whole.",
		Example: "  wt sweep             # fetch, show what is merged, then ask\n" +
			"  wt sweep --no-fetch  # compare with origin as last fetched\n" +
			"  wt sweep --yes       # delete and remove without asking (scripts)",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			opts := commands.SweepOptions{NoFetch: noFetch, Yes: yes,
				Width: terminalWidth(cmd.OutOrStdout())}
			if !yes && canAsk(cmd) {
				opts.Confirm = confirmSweep(newPrompter(cmd.InOrStdin(), cmd.OutOrStdout()))
			}
			return commands.Sweep(ctx, opts, cmd.OutOrStdout())
		}),
	}
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "compare with origin as last fetched")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "delete and remove without asking")
	return cmd
}

// confirmSweep asks once for the whole plan, defaulting to no.
func confirmSweep(p *prompter) func(commands.SweepPlan) (bool, error) {
	return func(plan commands.SweepPlan) (bool, error) {
		return p.yesNo(plan.Question(), false), nil
	}
}
