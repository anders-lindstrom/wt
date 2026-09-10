package main

import (
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every worktree of this repository",
		Long: "Print every worktree of this repository: its work name, its branch and\n" +
			"its path. A worktree not at the path this layout gives it is marked,\n" +
			"and the legend says what to do about it.\n\n" +
			"On a terminal, paths are shown from ~ and shortened from the left to fit\n" +
			"its width. Piped, they are printed whole.",
		Example: "  wt list          # work name, branch and path for every worktree\n" +
			"  wt ls            # the same, for the impatient\n" +
			"  wt list | cat    # whole paths, however narrow the terminal",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			return commands.List(ctx, out, terminalWidth(out))
		},
	}
}

// terminalWidth is w's column count when w is a terminal, and 0 otherwise.
func terminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok || !isTerminal(f) {
		return 0
	}
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return width
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show each worktree's branch and whether it is clean",
		Long: "Print each worktree's branch and whether its checkout is clean, dirty\n" +
			"or unreadable — the one question `wt list` does not answer.\n\n" +
			"On a terminal, paths are shown from ~ and shortened from the left to fit\n" +
			"its width. Piped, they are printed whole.",
		Example: "  wt status               # branch and state for every worktree\n" +
			"  wt status | grep dirty  # only the ones with uncommitted changes",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			return commands.Status(ctx, out, terminalWidth(out))
		},
	}
}
