package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newHookCmd() *cobra.Command {
	hook := &cobra.Command{
		Use:    "hook",
		Short:  "Event handlers for editors and agents",
		Hidden: true,
	}
	hook.AddCommand(
		&cobra.Command{
			Use:   "claude-create",
			Short: "Claude Code WorktreeCreate handler (JSON on stdin)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ctx, err := openContext()
				if err != nil {
					return err
				}
				return commands.HookCreate(ctx, cmd.InOrStdin(), cmd.OutOrStdout(), os.Stderr)
			},
		},
		&cobra.Command{
			Use:   "claude-remove",
			Short: "Claude Code WorktreeRemove handler (JSON on stdin)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				ctx, err := openContext()
				if err != nil {
					return err
				}
				return commands.HookRemove(ctx, cmd.InOrStdin(), os.Stderr)
			},
		},
	)
	return hook
}
