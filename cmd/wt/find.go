package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newFindCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "find <pattern>",
		Short: "Resolve a worktree by fuzzy name, across repositories",
		Long: "Resolve a fuzzy pattern to a worktree path. The current repository is\n" +
			"searched first and wins ties, but a weak local match still lets other\n" +
			"repositories under $WT_ROOTS compete.\n\n" +
			"Prints one path. When several candidates tie, exits non-zero and lists\n" +
			"them on stderr, so nothing runs in a worktree you did not mean.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Not being in a repository is fine: the search falls back to roots.
			// A repository with a broken config is also fine, but must not
			// silently lose repo-first — OpenLenient reports the problem.
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			ctx := commands.OpenLenient(cwd, os.Stderr)
			matches, err := commands.Find(ctx, args[0])
			if err != nil {
				return err
			}
			switch {
			case len(matches) == 0:
				return fmt.Errorf("no worktree matches %q", args[0])
			case all:
				for _, m := range matches {
					fmt.Fprintln(cmd.OutOrStdout(), m.Path)
				}
				return nil
			case len(matches) == 1:
				fmt.Fprintln(cmd.OutOrStdout(), matches[0].Path)
				return nil
			}
			fmt.Fprintf(os.Stderr, "wt: %q is ambiguous:\n", args[0])
			for _, m := range matches {
				fmt.Fprintf(os.Stderr, "  %-24s %-28s %s\n", m.Work, m.Repo, m.Path)
			}
			return fmt.Errorf("%d candidates", len(matches))
		},
	}
	cmd.Flags().BoolVar(&all, "candidates", false, "print every tied candidate, one per line")
	return cmd
}
