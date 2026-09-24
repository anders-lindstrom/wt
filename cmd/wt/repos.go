package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newReposCmd() *cobra.Command {
	var sel selectionFlags
	var paths, doctor bool
	cmd := &cobra.Command{
		Use:   "repos",
		Short: "List the repositories wt manages under your roots",
		Long: "List every repository wt manages under your roots, grouped by root,\n" +
			"with how many worktrees each has and whether wt sync is set up there\n" +
			"(a .wt-sync.yaml on trunk as last fetched). It reads the disk only:\n" +
			"nothing is fetched and GitHub is not asked. `wt cd <repo>` goes to one.\n\n" +
			"wt doctor --all checks every one of them in full.\n\n" +
			selectionHelp + "\n\n" +
			"--paths prints the main checkouts' paths alone, one per line, for a\n" +
			"loop over them. A profile entry that is no longer a repository wt\n" +
			"manages is shown with what is wrong, and makes the listing fail.",
		Example: "  wt repos                    # every repository, by root\n" +
			"  wt repos --roots work,oss   # the ones under two roots\n" +
			"  wt repos --profile api      # the ones a profile names\n" +
			"  wt repos --all --paths      # paths only, for a shell loop",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if doctor {
				return errors.New("wt repos --doctor is wt doctor --all now (--roots and --profile work there too)")
			}
			u := loadUserWarn(cmd.ErrOrStderr())
			return commands.Repos(u, sel.selection(), commands.ReposOptions{Paths: paths}, cmd.OutOrStdout())
		},
	}
	// --all is what a bare wt repos lists already; it is taken so the flag
	// works the same on every command that has the other two.
	sel.add(cmd, true)
	cmd.Flags().BoolVar(&paths, "paths", false, "print the main checkouts' paths alone, one per line")
	// Where the old flag went, for the line already in someone's history.
	cmd.Flags().BoolVar(&doctor, "doctor", false, "moved to wt doctor --all")
	_ = cmd.Flags().MarkHidden("doctor")

	return cmd
}
