package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newRemoveCmd() *cobra.Command {
	var me bool
	cmd := &cobra.Command{
		Use:     "remove <type>/<work>",
		Aliases: []string{"rm"},
		Short:   "Remove a worktree, deleting its branch only when merged",
		Long: "Remove the worktree and decide what happens to its branch: delete it\n" +
			"when it is merged into the main branch, otherwise rename it out of the\n" +
			"<type>_wt/ prefix so unmerged work is never lost. A branch this tooling\n" +
			"did not create is never touched.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			if me {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				return commands.RemoveAt(ctx, cwd, cmd.OutOrStdout())
			}
			if len(args) != 1 {
				return cmd.Usage()
			}
			return commands.Remove(ctx, args[0], cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&me, "me", false, "remove the worktree you are standing in")
	return cmd
}
