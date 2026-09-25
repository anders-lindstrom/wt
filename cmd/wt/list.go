package main

import (
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

// pathWidthHelp ends the help of the commands that print a path column.
const pathWidthHelp = "On a terminal, paths are shown from ~ and shortened from the left to fit\n" +
	"its width. Piped, they are printed whole."

func newListCmd() *cobra.Command {
	var opts commands.ListOptions
	var sel selectionFlags
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every worktree of this repository",
		Long: "Print every worktree of this repository: its work name, its branch and\n" +
			"its path. A worktree not at the path this layout gives it is marked,\n" +
			"and the legend says what to do about it.\n\n" +
			"A PR column appears when a worktree here is on a pull request. GitHub\n" +
			"is asked about those branches by name, so a pull request of any age is\n" +
			"found — however many have been opened since. Each branch's answer is\n" +
			"kept in this repository for five minutes, so only the first listing\n" +
			"pays the `gh` call of around a second, and a branch made since then is\n" +
			"asked about on its own rather than waiting the five minutes out.\n" +
			"--refresh asks about them all again, --no-pr leaves the column out, and\n" +
			"`wt config set github false` turns the whole thing off.\n\n" +
			"A pull request merged somewhere other than trunk says where: a stacked\n" +
			"one reads `#31 merged into feat_wt/its-parent`, because nothing of it\n" +
			"has reached trunk yet.\n\n" +
			"--all, --roots or --profile list every repository they name, one\n" +
			"section each.\n\n" + pathWidthHelp,
		Example: "  wt ls                        # work name, branch and path for each worktree\n" +
			"  wt list | cat                # whole paths, however narrow the terminal\n" +
			"  wt list --all --no-pr        # every repository, no call to GitHub\n" +
			"  wt list --roots work --refresh  # one root's, asking GitHub again\n" +
			"  wt list --profile api        # the repositories a profile names",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if sel.selection().Any() {
				return commands.AcrossRepos(loadUserWarn(cmd.ErrOrStderr()), sel.selection(), out,
					func(ctx *commands.Context, w io.Writer) error { return commands.List(ctx, opts, w, terminalWidth(out)) })
			}
			return withContext(func(_ *cobra.Command, _ []string, ctx *commands.Context) error {
				return commands.List(ctx, opts, out, terminalWidth(out))
			})(cmd, args)
		},
	}
	sel.add(cmd, true)
	cmd.Flags().BoolVar(&opts.NoPR, "no-pr", false, "do not ask GitHub which worktree has a pull request")
	cmd.Flags().BoolVar(&opts.Refresh, "refresh", false, "ask GitHub again instead of using the cached pull requests")
	return cmd
}

func newStatusCmd() *cobra.Command {
	var sel selectionFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [<work>]",
		Short: "Show each worktree's state and standing against trunk",
		Long: "Print each worktree's branch, whether its checkout is clean, dirty or\n" +
			"unreadable, and where its branch stands against trunk: how many commits\n" +
			"behind and ahead of origin/<trunk> as last fetched, or of the local trunk\n" +
			"when origin/<trunk> is not here. The first line says which, and how old\n" +
			"the fetch is. Nothing is fetched; wt sync fetches.\n\n" +
			"With a worktree named — by anything `wt list` prints for it, or . for the\n" +
			"one you are in — that worktree alone, one fact per line: branch, path,\n" +
			"state, standing against trunk, its pull request where it has one, the\n" +
			"Claude sessions in it, then the verdict wt sync would give it, simulated\n" +
			"against trunk as last fetched: its class in wt sync's words, and what to\n" +
			"do about it.\n\n" +
			"The pull request line comes from the same answers `wt list` caches, so a\n" +
			"listing has already paid for it.\n\n" +
			"--all, --roots or --profile show every repository they name, one\n" +
			"section each; they take no worktree.\n\n" +
			"--json prints the worktree's plan for wt up as one object, for a tool\n" +
			"driving wt: trunk, eligibility, the stack it would move, the sessions\n" +
			"in it, and a token for wt up --expect. It writes nothing — no fetch,\n" +
			"no simulation. wt schema status prints its JSON Schema; docs/json.md\n" +
			"explains it.\n\n" + pathWidthHelp,
		Example: "  wt status                     # state and standing for every worktree\n" +

			"  wt status --all | grep behind # what is not on trunk, anywhere\n" +
			"  wt status . --json            # this worktree's plan for wt up, as JSON\n" +
			"  wt status --roots work        # the repositories under one root\n" +
			"  wt status --profile api       # the ones a profile names",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if asJSON {
				if sel.selection().Any() {
					return errors.New("--json reports one worktree; name it, without --all, --roots or --profile")
				}
				work := "."
				if len(args) == 1 {
					work = args[0]
				}
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				// Lenient: a repository with no configuration is a plan
				// that says so, not an error.
				ctx := commands.OpenLenient(cwd, io.Discard)
				if ctx == nil {
					return commands.ErrNotInRepo
				}
				return commands.UpPlanJSON(ctx, work, out)
			}
			if sel.selection().Any() {
				if len(args) > 0 {
					return errors.New("--all, --roots and --profile cover whole repositories; name no worktree with them")
				}
				return commands.AcrossRepos(loadUserWarn(cmd.ErrOrStderr()), sel.selection(), out,
					func(ctx *commands.Context, w io.Writer) error { return commands.Status(ctx, w, terminalWidth(out)) })
			}
			return withContext(func(_ *cobra.Command, args []string, ctx *commands.Context) error {
				if len(args) == 1 {
					return commands.StatusWorktree(ctx, args[0], commands.StatusOptions{}, out)
				}
				return commands.Status(ctx, out, terminalWidth(out))
			})(cmd, args)
		},
	}
	sel.add(cmd, true)
	cmd.Flags().BoolVar(&asJSON, "json", false, "the worktree's plan for wt up, as one JSON object; reads only")
	return cmd
}
