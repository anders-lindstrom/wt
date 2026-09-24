package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

var version = "dev"

func newRootCmd() *cobra.Command {
	// Declaration order, not alphabetical: inside a group the order is the
	// order you would do the things in.
	cobra.EnableCommandSorting = false
	root := &cobra.Command{
		Use:   "wt",
		Short: "Git worktree tooling: one implementation, per-repo configuration",
		Long: "wt manages git worktrees from a single implementation, reading each\n" +
			"repository's own bin/worktree/worktree.conf for how that repo works.\n" +
			"\n" +
			"The day goes look, work, tidy:\n" +
			"  look    wt list, wt status, wt sync    what exists, and how it stands\n" +
			"  work    wt new, wt cd, wt up           make one, work in it, keep it on trunk\n" +
			"  tidy    wt sweep, wt remove            clear up what has landed\n" +
			"\n" +
			"<work> names a worktree of this repository exactly: any of the three\n" +
			"things `wt list` prints for it (the work name, the branch, or the path),\n" +
			". for the one you are in, or / for the main checkout. <pattern> — what\n" +
			"cd, exec and find take — is fuzzy, and searches every repository under\n" +
			"your roots too. The commands that change or delete take <work>, so a\n" +
			"guess never lands on the wrong worktree.\n" +
			"\n" +
			"--all, --roots <name> and --profile <name> take a command across the\n" +
			"repositories under your roots: every one, the ones under the roots\n" +
			"named, or the ones a profile names (wt repos lists them).\n" +
			"Run `wt <command> --help` for that command's own examples.",
		Example: "  wt new fix/login-crash    # branch, worktree and provisioning in one\n" +
			"  wt cd login-crash         # work in it (needs wt's shell layer)\n" +
			"  wt up                     # onto trunk, if that goes through cleanly\n" +
			"  wt list                   # everything this repository has, and where\n" +
			"  wt remove login-crash     # done with it; the branch is kept if unmerged",
		SilenceErrors: true,
		// Usage is silenced only once a command starts doing work. Cobra
		// validates arguments before PersistentPreRun, so an argument mistake
		// still prints usage — which is exactly when it helps — while a runtime
		// failure does not bury its message under it.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			cmd.SilenceUsage = true
		},
	}
	// The groups are the flow: make one, get to it, keep it current, put it
	// where it belongs, and the repository-wide commands last. Twenty
	// commands in one alphabetical list is a list nobody reads.
	for _, g := range []*cobra.Group{
		{ID: groupMake, Title: "Make a worktree:"},
		{ID: groupUse, Title: "Get to your work:"},
		{ID: groupTrunk, Title: "Keep up with trunk:"},
		{ID: groupTidy, Title: "Put worktrees in their place:"},
		{ID: groupRepo, Title: "This repository, and this build:"},
	} {
		root.AddGroup(g)
	}
	// A command's group is set where it is listed, so the listing is the only
	// place to look for what `wt --help` prints and in which order.
	add := func(id string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = id
		}
		root.AddCommand(cmds...)
	}
	add(groupMake, newNewCmd(), newCheckoutCmd(), newPrCmd())
	add(groupUse, newCdCmd(), newExecCmd(), newListCmd(), newStatusCmd(), newFindCmd(),
		newPathCmd(), newBranchCmd(), newReposCmd())
	add(groupTrunk, newUpCmd(), newSyncCmd())
	add(groupTidy, newMigrateCmd(), newAdoptCmd(), newSetupCmd(), newRemoveCmd(), newSweepCmd())
	add(groupRepo, newInitCmd(), newConfigCmd(), newDoctorCmd(), newAboutCmd(), newVersionCmd())
	root.AddCommand(newBranchStripCmd(), newHookCmd())
	return root
}

// The command groups `wt --help` prints under.
const (
	groupMake  = "make"
	groupUse   = "use"
	groupTrunk = "trunk"
	groupTidy  = "tidy"
	groupRepo  = "repo"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the wt version",
		Long: "Print the version alone, on one line, for scripts. `wt about` prints\n" +
			"the same version with the build date and what changed.",
		Example: "  wt version  # one bare line, nothing else",
		Args:    cobra.NoArgs,
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
func newShellCmd(use, short, long, example string) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Short:   short,
		Example: example,
		Long: long + "\n\n" +
			"Implemented in wt's shell layer: a program cannot change its caller's\n" +
			"directory, so this one has to run inside your shell. Enable it with:\n" +
			"    " + sourceShellLayer,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("`wt %s` needs wt's shell layer, which is not loaded.\n"+
				"  Add to your shell rc:  %s",
				cmd.Name(), sourceShellLayer)
		},
	}
}

// sourceShellLayer is the line that loads wt's shell layer.
const sourceShellLayer = "source ~/.local/share/wt/wt.sh"

func newCdCmd() *cobra.Command {
	c := newShellCmd("cd [<pattern>]", "Change directory to a worktree (shell)",
		"Change your shell's directory to a worktree. The pattern is matched the\n"+
			"way `wt find` matches, so a few letters of the work name are enough.\n\n"+
			"\".\" is the root of the worktree you are in, the main checkout included;\n"+
			"\"/\", or no pattern at all, is the repository's main checkout.",
		"  wt cd login-crash  # jump to that worktree, in this shell\n"+
			"  wt cd login        # a few letters are enough\n"+
			"  wt cd .            # the root of the worktree you are in\n"+
			"  wt cd /            # back to the repository's main checkout\n"+
			"  wt cd              # the same, with nothing to type")
	c.ValidArgsFunction = completeWork
	return c
}

func newExecCmd() *cobra.Command {
	c := newShellCmd("exec <pattern> <command> [args...]",
		"Run a command inside a worktree (shell)",
		"Run a command with a worktree as its working directory, in a subshell,\n"+
			"so your own shell stays where it is and the command's exit code is\n"+
			"what you get back.",
		"  wt exec login-crash git status     # run it there, stay here\n"+
			"  wt exec login-crash make test      # its exit code becomes yours\n"+
			"  wt exec / git log --oneline -5     # the main checkout\n"+
			"  wt exec . make test                # the worktree you are in")
	c.ValidArgsFunction = completeWorkThenCommand
	return c
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

// withContext is a RunE that opens the context first and hands it to fn.
// Stderr is where the context puts a warning: an integration that was
// supposed to work and did not, or settings it could not read.
func withContext(fn func(cmd *cobra.Command, args []string, ctx *commands.Context) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx, err := openContext()
		if err != nil {
			return err
		}
		ctx.WarnTo(cmd.ErrOrStderr())
		return fn(cmd, args, ctx)
	}
}

// openLenient opens the context even when the configuration does not load,
// saying so on w. The context is nil outside a git repository, and what that
// means is the caller's to say.
func openLenient(w io.Writer) (*commands.Context, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	ctx := commands.OpenLenient(cwd, w)
	if ctx != nil {
		ctx.WarnTo(w)
	}
	return ctx, nil
}

// printLine ends a command whose whole output is one path or name, printed
// alone on stdout so a shell can capture it. An error prints nothing.
func printLine(cmd *cobra.Command, s string, err error) error {
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), s)
	return nil
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "wt: %v\n", err)
		if errors.Is(err, commands.ErrNoLaunchd) {
			return 2
		}
		return 1
	}
	return 0
}
