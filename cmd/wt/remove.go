package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newRemoveCmd() *cobra.Command {
	var me bool
	var meAt string
	var yes, force bool
	cmd := &cobra.Command{
		Use:     "remove <work>",
		Aliases: []string{"rm"},
		Short:   "Remove a worktree, deleting its branch only when merged",
		Long: "Remove the worktree and decide what happens to its branch. A branch\n" +
			"merged into trunk is deleted, whoever created it: merged means nothing\n" +
			"is lost. An unmerged branch wt made is renamed out of the <type>_wt/\n" +
			"prefix, so the work survives its worktree; an unmerged branch wt did\n" +
			"not make is left exactly as it is.\n\n" +
			"Merged means the branch's tip is reachable from origin/<trunk> as last\n" +
			"fetched, or from the local trunk, so a pull request merged on the\n" +
			"remote counts even while the main checkout's trunk is behind. Remove\n" +
			"never fetches. A branch that moves after the plan is kept.\n\n" +
			"A squash or rebase merge leaves no such tip, so git reads the branch as\n" +
			"unmerged for ever. Where `wt list` or `wt sweep` has already recorded\n" +
			"that GitHub merged this branch into trunk at exactly this commit, remove\n" +
			"deletes it too, and says which pull request it believed. It reads that\n" +
			"from the file they left behind and never from the network: remove runs\n" +
			"from git hooks. With nothing recorded, the branch is kept as it always\n" +
			"was.\n\n" +
			"The plan says where the branch stands either way — merged, or how many\n" +
			"commits ahead of origin/<trunk> (or trunk, without it) it is — because\n" +
			"that is the fact the whole decision turns on. \"clean\" is about the\n" +
			"checkout, not the branch: it means nothing is uncommitted.\n\n" +
			"The worktree can be named by anything `wt list` prints — the work name,\n" +
			"the branch, or the path — or by . for the one you are standing in, which\n" +
			"--me also means. Matching is exact and stays inside this repository; a\n" +
			"work name used under two types has to be named by its type as well.\n\n" +
			"What the removal will do is printed before it does it. In a terminal you\n" +
			"are then asked to confirm; --yes skips the question, and a script or hook\n" +
			"with no terminal is never asked.\n\n" +
			"A worktree can carry a git lock — an agent session takes one for the\n" +
			"directory it works in. A lock whose process has exited is released and\n" +
			"the removal goes ahead; one whose holder is still running stops it before\n" +
			"the question is asked, and --force is how you say you mean it anyway.",
		Example: "  wt remove login-crash          # say where the branch stands, then ask\n" +
			"  wt remove fix/login-crash      # when two types share a work name\n" +
			"  wt remove login-crash --yes    # do not ask (scripts, hooks)\n" +
			"  wt remove login-crash --force  # break a lock a session still holds\n" +
			"  wt remove --me                 # the one you are standing in; or wt remove .",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !me && meAt == "" && len(args) != 1 {
				return fmt.Errorf("needs the worktree to remove, or --me to remove the " +
					"one you are in — for example: wt remove login-crash")
			}
			ctx, err := openContext()
			if err != nil {
				return err
			}
			opts := commands.RemoveOptions{Force: force}
			if !yes && canAsk(cmd) {
				opts.Confirm = confirmRemoval(newPrompter(cmd.InOrStdin(), cmd.OutOrStdout()))
			}
			if meAt != "" {
				return commands.RemoveAt(ctx, meAt, opts, cmd.OutOrStdout())
			}
			if me {
				return commands.RemoveAt(ctx, ctx.Cwd, opts, cmd.OutOrStdout())
			}
			return commands.Remove(ctx, args[0], opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&me, "me", false, "remove the worktree you are standing in")
	cmd.Flags().BoolVarP(&force, "force", "f", false,
		"break a git worktree lock whose holder is still running")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().StringVar(&meAt, "me-at", "", "remove the worktree at this path (used by wt_rm_me)")
	_ = cmd.Flags().MarkHidden("me-at")
	return cmd
}

// confirmRemoval asks the question, defaulting to no. The plan has already been
// printed by the time this runs, so the prompt itself stays one line.
func confirmRemoval(p *prompter) func(commands.Plan) (bool, error) {
	return func(commands.Plan) (bool, error) { return p.yesNo("Remove it?", false), nil }
}
