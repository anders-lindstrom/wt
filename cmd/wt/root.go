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
		SilenceErrors: true,
		// Usage is silenced only once a command starts doing work. Cobra
		// validates arguments before PersistentPreRun, so an argument mistake
		// still prints usage — which is exactly when it helps — while a runtime
		// failure does not bury its message under it.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			cmd.SilenceUsage = true
		},
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
		newNewCmd(),
		newRemoveCmd(),
		newAdoptCmd(),
		newMigrateCmd(),
		newDoctorCmd(),
		newHookCmd(),
		newFindCmd(),
		newCheckoutCmd(),
		newCdCmd(),
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

// newCdCmd exists only to explain itself. Changing the caller's directory is
// impossible from a child process, so `wt cd` is a shell function; reaching the
// binary means the shell layer was never sourced.
func newCdCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "cd [pattern]",
		Short:  "Change directory to a worktree (needs the shell layer)",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("`wt cd` is a shell function, because a program cannot " +
				"change your shell's directory.\n  Add this to your shell rc: " +
				"source ~/.local/share/wt/wt.sh")
		},
	}
}

// needArgs validates argument count with a message that names what is missing
// and shows an example, rather than cobra's "accepts 1 arg(s), received 0".
// An argument mistake is the moment a user most needs telling what to type.
func needArgs(n int, what, example string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		switch {
		case len(args) == n:
			return nil
		case len(args) < n:
			return fmt.Errorf("needs %s — for example: %s", what, example)
		default:
			return fmt.Errorf("takes %d argument(s), got %d — for example: %s",
				n, len(args), example)
		}
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
