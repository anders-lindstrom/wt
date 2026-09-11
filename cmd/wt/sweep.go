package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSweepCmd() *cobra.Command {
	var noFetch, yes bool
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Delete local branches trunk already contains",
		Long: "Delete the local branches already merged into trunk, and say what\n" +
			"else is left. A branch is merged when its tip is reachable from\n" +
			"origin/<trunk>, fetched first because merges happen on the remote, or\n" +
			"from the local trunk, so its commits are on trunk. A branch cut and\n" +
			"never committed to counts as merged. Squash merges are not recognised.\n\n" +
			"The plan has three parts:\n" +
			"  deleted       merged, and in use in no worktree\n" +
			"  in use        merged, but a worktree has it checked out (wt remove\n" +
			"                deletes it with the worktree), or a bisect or rebase there\n" +
			"                holds it (finish or abort that, then sweep again)\n" +
			"  kept          its upstream is gone, but trunk lacks its commits\n\n" +
			"Trunk is MAIN_BRANCH, or origin's HEAD when that is not set; with\n" +
			"neither, sweep refuses. Trunk, the branch origin's HEAD names and the\n" +
			"long-lived branches (main, master, develop, development, staging,\n" +
			"production, release*) are never deleted. Remote branches are never\n" +
			"touched; the fetch prunes only origin's remote-tracking refs.\n\n" +
			"It runs only from the main checkout, because it acts on the whole\n" +
			"repository rather than on the worktree you are in. In a terminal it\n" +
			"asks once; --yes skips the question; without a terminal it prints the\n" +
			"plan and deletes nothing. A branch that moves or is checked out while\n" +
			"the question is open is kept. Each deletion prints the commit the\n" +
			"branch was at: `git branch <name> <commit>` restores its commits, not\n" +
			"its upstream setting.\n\n" +
			"On a terminal, commit subjects are cut to fit its width. Piped, they\n" +
			"are printed whole.",
		Example: "  wt sweep             # fetch, show what is merged, then ask\n" +
			"  wt sweep --no-fetch  # compare with origin as last fetched\n" +
			"  wt sweep --yes       # delete without asking (scripts)",
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
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "delete without asking")
	return cmd
}

// confirmSweep asks once for the whole plan, defaulting to no.
func confirmSweep(p *prompter) func(commands.SweepPlan) (bool, error) {
	return func(plan commands.SweepPlan) (bool, error) {
		q := fmt.Sprintf("Delete these %d branches?", len(plan.Delete))
		if len(plan.Delete) == 1 {
			q = "Delete this branch?"
		}
		return p.yesNo(q, false), nil
	}
}
