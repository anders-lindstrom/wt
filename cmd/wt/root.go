package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "wt",
		Short: "Git worktree tooling: one implementation, per-repo configuration",
		Long: "wt manages git worktrees from a single implementation, reading each\n" +
			"repository's own bin/worktree/worktree.conf for how that repo works.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newVersionCmd(),
		newConfigCmd(),
		newPathCmd(),
		newBranchCmd(),
		newBranchStripCmd(),
		newListCmd(),
		newStatusCmd(),
		newSetupCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the wt version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version)
			return nil
		},
	}
}

// openContext resolves the repository and configuration for the current
// directory. Every subcommand that touches a repo starts here.
func openContext() (*commands.Context, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return commands.Open(cwd)
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "wt: %v\n", err)
		return 1
	}
	return 0
}
