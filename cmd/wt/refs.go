package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newRefsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refs",
		Short: "Sweep backup branches and tags aside, and bring them back",
		Long: "Backup branches and tags pile up: git branch backup/x before a rebase,\n" +
			"a safety tag before a risky merge. wt refs sweep moves the ones nothing\n" +
			"needs any more to pins under refs/wt-swept/<run>/, one run at a time;\n" +
			"wt refs swept lists the runs, wt refs restore <run> puts one back, and\n" +
			"wt refs purge deletes a run's pins for good. Until then nothing is\n" +
			"lost: a pin holds the commits exactly as the ref did.",
		Example: "  wt refs sweep --dry-run       # what would go, and why the rest stays\n" +
			"  wt refs swept                 # the runs there are\n" +
			"  wt refs restore 20260930T091500Z-3f2a  # put one run back",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newRefsSweepCmd(), newRefsSweptCmd(), newRefsRestoreCmd(), newRefsPurgeCmd())
	return cmd
}

func newRefsSweepCmd() *cobra.Command {
	var remote, noFetch, yes, dryRun, asJSON bool
	var expect, runID string
	var only []string
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Move backup branches and tags nothing needs to pins",
		Long: "Move the backup branches and tags nothing needs any more out of the\n" +
			"way: each goes to a pin under refs/wt-swept/<run>/ and is deleted in\n" +
			"the same step, so it can be put back until wt refs purge deletes it.\n\n" +
			"A backup is a branch or tag whose name matches a pattern, by default\n" +
			"backup/* safe/* safety/* backup-* safe-* safety-* *-backup *-safe\n" +
			"*-safety (* matches slashes too). wt config set ref_sweep_patterns\n" +
			"\"...\" replaces them. Every other branch, tag and origin/* ref is a\n" +
			"container. The plan sorts each backup:\n" +
			"  contained  a container has its commit: nothing is only on it. Swept\n" +
			"  old        it has commits no container has, and is older than\n" +
			"             ref_sweep_age (14d unless set). Swept\n" +
			"  young      the same, and younger. Left, unless --only names it\n" +
			"  kept       checked out in a worktree, held by a rebase or bisect,\n" +
			"             named by a quarantine, trunk or a long-lived branch\n\n" +
			"Age is when the branch was made, from the first entry of its reflog:\n" +
			"a backup of a three-week-old branch taken this morning is young. An\n" +
			"annotated tag's age is its tagger date; anything else, its commit's.\n\n" +
			"--remote also sweeps origin's backups (from ls-remote), and only the\n" +
			"ones origin's own branches and tags contain. A branch with an open pull\n" +
			"request is kept, since deleting it closes the pull request, and so is\n" +
			"every branch when GitHub cannot be asked. Each is fetched into its pin\n" +
			"first and deleted with a push leased on the commit the plan read. All\n" +
			"of it goes through one URL: origin must fetch from and push to the\n" +
			"same one, with no password or token in it.\n\n" +
			"Before anything moves, the run records its id, time and origin in\n" +
			"refs/wt-swept/<run>/meta. --run-id <id> names the run instead of the\n" +
			"id wt would mint; one that has refs already refuses.\n\n" +
			"It fetches origin first; --no-fetch uses origin as last fetched. Each\n" +
			"ref is read again right before it moves, and one that moved is kept.\n" +
			"A branch's config goes with it, as with git branch -D. In a terminal\n" +
			"it asks once; --yes skips the question; without a terminal it prints\n" +
			"the plan and changes nothing.\n\n" +
			"--only <id> replaces the selection with the rows named, one id per\n" +
			"flag, as the plan names them: refs/heads/<name>, refs/tags/<name>, or\n" +
			"origin:refs/heads/<name> for origin's. A kept or unknown id refuses\n" +
			"the whole sweep.\n\n" +
			"--json is for a tool. With --dry-run, or without --yes, it prints the\n" +
			"plan as one object on stdout, with a token; with --yes it sweeps and\n" +
			"prints one result object, also when interrupted. --expect <token>\n" +
			"refuses the sweep unless the plan made now has that token; --only does\n" +
			"not change the token. wt schema refs-sweep-plan and wt schema\n" +
			"refs-sweep print the schemas; docs/json.md explains them.",
		Example: "  wt refs sweep --dry-run      # the plan; change nothing\n" +
			"  wt refs sweep --remote --no-fetch --yes  # origin's too, no question\n" +
			"  wt refs sweep --only refs/heads/backup/x --only refs/tags/safe-1\n" +
			"  wt refs sweep --yes --json --expect rs1-0a1b --run-id 20260930T091500Z-3f2a",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if expect != "" && (!asJSON || !yes) {
				return errors.New("--expect holds a sweep to the plan a tool read: pass it with --yes --json")
			}
			if runID != "" && !yes {
				return errors.New("--run-id names a run that moves refs: pass it with --yes")
			}
			opts := commands.RefsOptions{Remote: remote, NoFetch: noFetch, DryRun: dryRun, Yes: yes, Expect: expect,
				RunID: runID}
			if cmd.Flags().Changed("only") {
				opts.Only = append([]string{}, only...)
			}
			out, progress := cmd.OutOrStdout(), cmd.ErrOrStderr()
			ctx, err := openContext()
			if asJSON && !yes {
				if err != nil {
					commands.RefsSweepPlanFailed(out, err)
					return err
				}
				ctx.WarnTo(progress)
				return commands.RefsSweepPlanJSON(ctx, opts, out, progress)
			}
			if asJSON {
				opts.Journal = commands.NewRefsJournal(out)
				if err != nil {
					opts.Journal.Fail(err)
					return err
				}
				ctx.WarnTo(progress)
				return commands.RefsSweep(ctx, opts, progress)
			}
			if err != nil {
				return err
			}
			ctx.WarnTo(progress)
			if !yes && !dryRun && canAsk(cmd) {
				p := newPrompter(cmd.InOrStdin(), out)
				opts.Confirm = func(commands.RefsPlan) bool { return p.yesNo("Sweep these refs?", false) }
			}
			return commands.RefsSweep(ctx, opts, out)
		},
	}
	cmd.Flags().BoolVar(&remote, "remote", false, "sweep origin's backups too")
	cmd.Flags().StringArrayVar(&only, "only", nil, "sweep only this id (repeat for more)")
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "compare with origin as last fetched")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "sweep without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan and change nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the plan, or with --yes the result, as JSON")
	cmd.Flags().StringVar(&expect, "expect", "", "sweep only if the plan still has this token")
	cmd.Flags().StringVar(&runID, "run-id", "", "name the run this id instead of minting one")
	cmd.MarkFlagsMutuallyExclusive("yes", "dry-run")
	return cmd
}

func newRefsSweptCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "swept",
		Short: "List the runs of wt refs sweep whose pins are still there",
		Long: "List every run of wt refs sweep whose pins are still under\n" +
			"refs/wt-swept/, oldest first, with each ref it moved and the object\n" +
			"its pin holds. It changes nothing. --json prints one object; wt schema\n" +
			"refs-swept prints its schema.",
		Example: "  wt refs swept         # the runs, and what each moved\n" +
			"  wt refs swept --json  # the same, for a tool",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := openContext()
			if err != nil && !asJSON {
				return err
			}
			if ctx != nil {
				ctx.WarnTo(cmd.ErrOrStderr())
			}
			return commands.RefsSwept(ctx, asJSON, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the runs as JSON")
	return cmd
}

func newRefsRestoreCmd() *cobra.Command {
	var yes, dryRun, asJSON bool
	var expect string
	var only []string
	cmd := &cobra.Command{
		Use:   "restore <runId>",
		Short: "Put back what one run of wt refs sweep moved",
		Long: "Put back the refs one run of wt refs sweep moved: each is made again at\n" +
			"the object its pin holds, and the pin goes in the same step. A name\n" +
			"that holds something else now is left alone and the plan says so; one\n" +
			"already back at that object only loses its pin. origin's refs are\n" +
			"pushed back, leased on the name being free there, and the pin goes only\n" +
			"once origin is read back holding it; a remote ref goes back only while\n" +
			"origin resolves to the URL the run recorded. A branch comes back\n" +
			"without its upstream setting. Once every pin of a run is restored,\n" +
			"nothing of it is left under refs/wt-swept/.\n\n" +
			"--only <id> restores just the rows named, one id per flag; an id the\n" +
			"run does not have, or one whose name is taken, refuses the lot. In a\n" +
			"terminal it asks once; --yes skips the question.\n\n" +
			"--json is for a tool: without --yes it prints the plan and a token,\n" +
			"with --yes the result, one object on stdout; --expect <token> refuses\n" +
			"unless the plan made now has that token. wt schema refs-restore-plan\n" +
			"and wt schema refs-restore print the schemas.",
		Example: "  wt refs restore 20260930T091500Z-3f2a --dry-run  # what goes back\n" +
			"  wt refs restore 20260930T091500Z-3f2a --yes\n" +
			"  wt refs restore 20260930T091500Z-3f2a --only refs/heads/backup/x --yes\n" +
			"  wt refs restore 20260930T091500Z-3f2a --yes --json --expect rr1-0123abcd",
		Args: needArgs(1, "<runId>", "wt refs restore 20260930T091500Z-3f2a"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if expect != "" && (!asJSON || !yes) {
				return errors.New("--expect holds a restore to the plan a tool read: pass it with --yes --json")
			}
			opts := commands.RefsRestoreOptions{DryRun: dryRun, Yes: yes, Expect: expect}
			if cmd.Flags().Changed("only") {
				opts.Only = append([]string{}, only...)
			}
			out, progress := cmd.OutOrStdout(), cmd.ErrOrStderr()
			ctx, err := openContext()
			if asJSON && !yes {
				if err != nil {
					ctx = nil
				} else {
					ctx.WarnTo(progress)
				}
				return commands.RefsRestorePlanJSON(ctx, args[0], opts.Only, out, progress)
			}
			if asJSON {
				opts.Journal = commands.NewRefsRestoreJournal(out, args[0])
				if err != nil {
					opts.Journal.Fail(err)
					return err
				}
				ctx.WarnTo(progress)
				return commands.RefsRestore(ctx, args[0], opts, progress)
			}
			if err != nil {
				return err
			}
			ctx.WarnTo(progress)
			if !yes && !dryRun && canAsk(cmd) {
				p := newPrompter(cmd.InOrStdin(), out)
				opts.Confirm = func(commands.RefsRestorePlan) bool { return p.yesNo("Restore these refs?", false) }
			}
			return commands.RefsRestore(ctx, args[0], opts, out)
		},
	}
	cmd.Flags().StringArrayVar(&only, "only", nil, "restore only this id (repeat for more)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "restore without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan and change nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the plan, or with --yes the result, as JSON")
	cmd.Flags().StringVar(&expect, "expect", "", "restore only if the plan still has this token")
	cmd.MarkFlagsMutuallyExclusive("yes", "dry-run")
	return cmd
}

func newRefsPurgeCmd() *cobra.Command {
	var yes, dryRun, asJSON bool
	var expect, olderThan string
	cmd := &cobra.Command{
		Use:   "purge (<runId>... | --older-than <age>)",
		Short: "Delete the pins of runs of wt refs sweep for good",
		Long: "Delete, for good, the pins of the runs named, or of every run older\n" +
			"than --older-than (14d, 2w, 36h), by when the run began. Each\n" +
			"pin goes only while it holds the object the plan read. Once a pin is\n" +
			"gone, the commits nothing else holds are unreachable and git gc takes\n" +
			"them; the plan counts them.\n\n" +
			"What it would delete is printed first. In a terminal you are asked\n" +
			"once; --yes skips the question, and with no terminal and no --yes it\n" +
			"refuses. --dry-run prints the plan and stops.\n\n" +
			"--json is for a tool: without --yes it prints the plan and a token,\n" +
			"with --yes the result, one object on stdout; --expect <token> refuses\n" +
			"unless the plan made now has that token. wt schema refs-purge-plan and\n" +
			"wt schema refs-purge print the schemas.",
		Example: "  wt refs purge --older-than 30d --dry-run  # what a month's runs hold\n" +
			"  wt refs purge 20260930T091500Z-3f2a --yes\n" +
			"  wt refs purge --older-than 30d --yes --json --expect rp1-0123abcd",
		RunE: func(cmd *cobra.Command, args []string) error {
			if expect != "" && (!asJSON || !yes) {
				return errors.New("--expect holds a purge to the plan a tool read: pass it with --yes --json")
			}
			opts := commands.RefsPurgeOptions{RunIDs: args, OlderThan: olderThan, DryRun: dryRun, Yes: yes, Expect: expect}
			out, progress := cmd.OutOrStdout(), cmd.ErrOrStderr()
			ctx, err := openContext()
			if asJSON && !yes {
				if err != nil {
					ctx = nil
				} else {
					ctx.WarnTo(progress)
				}
				return commands.RefsPurgePlanJSON(ctx, opts, out, progress)
			}
			if asJSON {
				opts.Journal = commands.NewRefsPurgeJournal(out)
				if err != nil {
					opts.Journal.Fail(err)
					return err
				}
				ctx.WarnTo(progress)
				return commands.RefsPurge(ctx, opts, progress)
			}
			if err != nil {
				return err
			}
			ctx.WarnTo(progress)
			if !yes && !dryRun {
				if canAsk(cmd) {
					p := newPrompter(cmd.InOrStdin(), out)
					opts.Confirm = func(commands.RefsPurgePlan) bool { return p.yesNo("Delete them for good?", false) }
				} else {
					opts.NoTerminal = true
				}
			}
			return commands.RefsPurge(ctx, opts, out)
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "", "purge every run older than this age")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what the purge would delete and delete nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the plan, or with --yes the result, as JSON")
	cmd.Flags().StringVar(&expect, "expect", "", "purge only if the plan still has this token")
	cmd.MarkFlagsMutuallyExclusive("yes", "dry-run")
	return cmd
}
