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
		newExecCmd(),
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

// cd and exec are real commands, implemented in the shell layer because a child
// process cannot change its caller's directory. They are listed in help like
// any other command — hiding the two most-used ones would make them
// undiscoverable — and reaching these implementations means the shell layer was
// never sourced, so they say how to fix that.
func newShellCmd(use, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long: long + "\n\n" +
			"Implemented in wt's shell layer: a program cannot change its caller's\n" +
			"directory, so this one has to run inside your shell. Enable it with:\n" +
			"    source ~/.local/share/wt/wt.sh",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("`wt %s` needs wt's shell layer, which is not loaded.\n"+
				"  Add to your shell rc:  source ~/.local/share/wt/wt.sh",
				cmd.Name())
		},
	}
}

func newCdCmd() *cobra.Command {
	return newShellCmd("cd [pattern]", "Change directory to a worktree (shell)",
		"Change your shell's directory to a worktree.\n\n"+
			"With no pattern, or \".\", returns to the repository's main checkout.")
}

func newExecCmd() *cobra.Command {
	return newShellCmd("exec <pattern> <command> [args...]",
		"Run a command inside a worktree (shell)",
		"Run a command with a worktree as its working directory, in a subshell,\n"+
			"so your own shell stays where it is and the command's exit code is\n"+
			"what you get back.")
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
