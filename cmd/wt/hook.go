package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newHookCmd() *cobra.Command {
	hook := &cobra.Command{
		Use:   "hook",
		Short: "Event handlers for editors and agents",
		Long: "Handlers a harness calls, not a person: they read one JSON object on\n" +
			"stdin and go through the same New and Remove as the commands, so a\n" +
			"worktree an agent makes is provisioned and one it drops keeps its\n" +
			"unmerged branch.",
		Example: "  wt hook claude-create <<< '{\"name\":\"fix/login-crash\"}'\n" +
			"  wt hook claude-remove <<< '{\"name\":\"login-crash\"}'",
		Hidden: true,
	}
	hook.AddCommand(
		&cobra.Command{
			Use:   "claude-create",
			Short: "Claude Code WorktreeCreate handler (JSON on stdin)",
			Long: "Create and provision a worktree for a harness. Reads {\"name\": ...}\n" +
				"on stdin and prints the absolute path, and nothing else, on stdout.",
			Example: "  wt hook claude-create <<< '{\"name\":\"fix/login-crash\"}'",
			Args:    cobra.NoArgs,
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
			Long: "Remove a worktree for a harness, through the same merge check as\n" +
				"`wt remove`. Reads {\"path\": ...} or {\"name\": ...} on stdin, never\n" +
				"asks, and writes nothing to stdout.",
			Example: "  wt hook claude-remove <<< '{\"path\":\"/abs/path/to/worktree\"}'\n" +
				"  wt hook claude-remove <<< '{\"name\":\"login-crash\"}'",
			Args: cobra.NoArgs,
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
