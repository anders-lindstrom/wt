package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newPrCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Work with this repository's pull requests",
		Long: "Read the repository's pull requests through the GitHub CLI, and put a\n" +
			"worktree on one. wt keeps no credentials of its own: whatever `gh` is\n" +
			"logged in as is what it sees, and nothing here writes to GitHub.\n\n" +
			"Turn the whole integration off with `wt config set github false`.",
		Example: "  wt pr list          # every open pull request, and the worktree on it\n" +
			"  wt pr checkout 12   # a worktree for that pull request",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newPrListCmd(), newPrCheckoutCmd())
	return cmd
}

func newPrListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List open pull requests and the worktrees on them",
		Long: "Print every open pull request: its number, its state and what its checks\n" +
			"say, who opened it, its title, and the worktree whose branch is its head\n" +
			"— or \"-\" when nothing here is on it.\n\n" +
			"`wt list` reads the same facts from the other end, a PR column beside\n" +
			"each worktree.\n\n" + pathWidthHelp,
		Example: "  wt pr list        # open pull requests, and where each is checked out\n" +
			"  wt pr ls          # the same, for the impatient\n" +
			"  wt pr list | cat  # whole paths, however narrow the terminal",
		Args: cobra.NoArgs,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			out := cmd.OutOrStdout()
			return commands.PRList(ctx, out, terminalWidth(out))
		}),
	}
}

func newPrCheckoutCmd() *cobra.Command {
	var opts commands.PROptions
	cmd := &cobra.Command{
		Use:     "checkout [<number>]",
		Aliases: []string{"co"},
		Short:   "Put a worktree on a pull request",
		Long: "Create a worktree for a pull request and provision it, the way `wt new`\n" +
			"does. The worktree goes on the pull request's own head branch, set up by\n" +
			"`gh pr checkout` itself, so a push from it updates the pull request —\n" +
			"including a pull request from a fork.\n\n" +
			"Without a number, the open pull requests are listed and you pick one.\n" +
			"With one, that pull request is checked out with no listing, and it may\n" +
			"be a merged or closed one: finished work is worth re-reading. GitHub\n" +
			"deletes the head branch on merge, so that one comes from\n" +
			"refs/pull/<number>/head, on a branch with no upstream.\n\n" +
			"The worktree is named for the pull request, pr-<number>-<branch>, under\n" +
			"the type its branch suggests. A head branch that already follows this\n" +
			"repository's convention keeps its own name instead, so a branch wt made\n" +
			"lands where `wt new` would have put it. A pull request whose branch is\n" +
			"already in a worktree prints that path and makes nothing.\n\n" +
			"The path alone goes to stdout, so `cd \"$(wt pr checkout 12)\"` works.",
		Example: "  wt pr checkout                  # pick one of the open pull requests\n" +
			"  wt pr checkout 12               # straight to that one, no listing\n" +
			"  wt pr checkout 12 --no-setup    # the worktree, nothing else\n" +
			"  wt pr checkout 12 --skip-build  # provision, but do not build\n" +
			"  wt pr checkout 12 --no-superset # keep it out of the Superset app",
		Args: cobra.MaximumNArgs(1),
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			number := 0
			if len(args) == 1 {
				n, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
				if err != nil || n <= 0 {
					return fmt.Errorf("%q is not a pull request number — for example: wt pr checkout 12", args[0])
				}
				number = n
			}
			opts.Choose = prChooser(cmd)
			path, err := commands.PRCheckout(ctx, number, opts, cmd.ErrOrStderr())
			// The path alone goes to stdout so `cd "$(wt pr checkout ...)"` works.
			return printLine(cmd, path, err)
		}),
	}
	addProvisionFlags(cmd, &opts.SkipBuild, &opts.NoSetup, &opts.NoSuperset)
	return cmd
}
