package main

import (
	"errors"
	"fmt"
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
			"A SESSION column appears when a worktree here has a Claude Code session\n" +
			"in it: the session's name and its state, as `claude agents` lists it.\n" +
			"A background session is working, needs input (it waits for you) or\n" +
			"done, and any other state is printed as claude spells it; one open in\n" +
			"a terminal is busy or idle there. With several in one worktree, the one\n" +
			"that needs input is shown, else the newest, and +N counts the rest.\n" +
			"`wt attach` opens one. On a terminal the column is shown only when\n" +
			"every row still fits the width; when one would not, the listing is\n" +
			"what it is without the column, and a line under it says so. --wide\n" +
			"prints the table as it is printed when piped: the column in it, paths\n" +
			"whole, rows as long as they are. The column costs a call to claude of\n" +
			"around a quarter of a second on every listing, and a claude that has\n" +
			"not answered in two seconds is one line under the table.\n" +
			"--no-sessions leaves the column out and does not ask.\n\n" +
			"--all, --roots or --profile list every repository they name, one\n" +
			"section each.\n\n" + pathWidthHelp,
		Example: "  wt ls                        # work name, branch and path for each worktree\n" +
			"  wt list --wide               # sessions and whole paths, however narrow\n" +
			"  wt list --all --no-pr        # every repository, no call to GitHub\n" +
			"  wt list --roots work --refresh  # one root's, asking GitHub again\n" +
			"  wt list --profile api --no-sessions  # a profile's, without asking claude",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if sel.selection().Any() {
				// One read of claude's sessions serves every repository,
				// and what it could not read is said once, at the end.
				if !opts.NoSessions {
					opts.Sessions = commands.ReadClaudeSessions(commands.SessionsDeadline)
				}
				err := commands.AcrossRepos(loadUserWarn(cmd.ErrOrStderr()), sel.selection(), out,
					func(ctx *commands.Context, w io.Writer) error { return commands.List(ctx, opts, w, terminalWidth(out)) })
				if opts.Sessions != nil {
					if note := opts.Sessions.Note(); note != "" {
						fmt.Fprintf(out, "\n%s\n", note)
					}
				}
				return err
			}
			return withContext(func(_ *cobra.Command, _ []string, ctx *commands.Context) error {
				return commands.List(ctx, opts, out, terminalWidth(out))
			})(cmd, args)
		},
	}
	sel.add(cmd, true)
	addPRFlags(cmd, &opts.NoPR, &opts.Refresh)
	cmd.Flags().BoolVar(&opts.NoSessions, "no-sessions", false, "do not ask claude which worktree has a session")
	cmd.Flags().BoolVar(&opts.Wide, "wide", false,
		"print the table as when piped, whatever the terminal's width: the SESSION column in it, paths whole")
	return cmd
}

// addPRFlags puts --no-pr and --refresh on a command that shows the pull
// requests `wt list` caches, spelled the same on each.
func addPRFlags(cmd *cobra.Command, noPR, refresh *bool) {
	cmd.Flags().BoolVar(noPR, "no-pr", false, "do not ask GitHub which worktree has a pull request")
	cmd.Flags().BoolVar(refresh, "refresh", false, "ask GitHub again instead of using the cached pull requests")
}

func newStatusCmd() *cobra.Command {
	var sel selectionFlags
	var opts commands.StatusOptions
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
			"agent sessions in it, then the verdict wt sync would give it, simulated\n" +
			"against trunk as last fetched: its class in wt sync's words, and what to\n" +
			"do about it.\n\n" +
			"A PR column appears when a worktree here is on a pull request. It and\n" +
			"the pull request line come from the same answers `wt list` caches, so\n" +
			"a listing has already paid for them. --refresh asks GitHub again,\n" +
			"--no-pr leaves them out.\n\n" +
			"--all, --roots or --profile show every repository they name, one\n" +
			"section each; they take no worktree.\n\n" +
			"--json prints the worktree's plan for wt up as one object, for a tool\n" +
			"driving wt: trunk, eligibility, the stack it would move, each of its\n" +
			"branches against its own remote (the ref a push would replace, as last\n" +
			"fetched), the sessions in it, and a token for wt up --expect. It writes\n" +
			"nothing — no fetch, no simulation. wt schema status prints its JSON\n" +
			"Schema; docs/json.md explains it.\n\n" + pathWidthHelp,
		Example: "  wt status --all | grep behind   # what is not on trunk, anywhere\n" +
			"  wt status --roots work --no-pr  # one root's, no call to GitHub\n" +
			"  wt status --profile api         # the repositories a profile names\n" +
			"  wt status login-crash --refresh # one worktree, its PR asked again\n" +
			"  wt status . --json              # this worktree's plan for wt up, as JSON",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if asJSON {
				if opts.Refresh {
					return errors.New("--json asks GitHub nothing, so there is nothing to --refresh")
				}
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
					func(ctx *commands.Context, w io.Writer) error {
						return commands.Status(ctx, opts, w, terminalWidth(out))
					})
			}
			return withContext(func(_ *cobra.Command, args []string, ctx *commands.Context) error {
				if len(args) == 1 {
					return commands.StatusWorktree(ctx, args[0], opts, out)
				}
				return commands.Status(ctx, opts, out, terminalWidth(out))
			})(cmd, args)
		},
	}
	sel.add(cmd, true)
	addPRFlags(cmd, &opts.NoPR, &opts.Refresh)
	cmd.Flags().BoolVar(&asJSON, "json", false, "the worktree's plan for wt up, as one JSON object; reads only")
	return cmd
}
