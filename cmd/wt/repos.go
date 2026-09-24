package main

import (
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
			"--doctor runs wt doctor in every repository and shows each one's\n" +
			"problems under it, then checks the roots and profiles; it fails when it\n" +
			"finds anything.\n\n" +
			selectionHelp + "\n\n" +
			"--paths prints the main checkouts' paths alone, one per line, for a\n" +
			"loop over them. A profile entry that is no longer a repository wt\n" +
			"manages is shown with what is wrong, and makes the listing fail.",
		Example: "  wt repos                    # every repository, by root\n" +
			"  wt repos --roots work,oss   # the ones under two roots\n" +
			"  wt repos --profile api      # the ones a profile names\n" +
			"  wt repos --paths            # paths only, for a shell loop\n" +
			"  wt repos --doctor           # wt doctor in each, and the roots checked",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u := loadUserWarn(cmd.ErrOrStderr())
			return commands.Repos(u, sel.selection(), commands.ReposOptions{Paths: paths, Doctor: doctor}, cmd.OutOrStdout())
		},
	}
	sel.add(cmd, false)
	cmd.Flags().BoolVar(&paths, "paths", false, "print the main checkouts' paths alone, one per line")
	cmd.Flags().BoolVar(&doctor, "doctor", false, "run wt doctor in every repository and show what it found")
	cmd.MarkFlagsMutuallyExclusive("paths", "doctor")
	return cmd
}
