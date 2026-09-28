package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newRemoveCmd() *cobra.Command {
	var me bool
	var meAt string
	var yes, force, dryRun bool
	var quarantineDir string
	cmd := &cobra.Command{
		Use:     "remove <work>",
		Aliases: []string{"rm"},
		Short:   "Remove a worktree, deleting its branch only when merged",
		Long: "Remove the worktree and decide what happens to its branch. A branch\n" +
			"merged into trunk is deleted, whoever created it: merged means nothing\n" +
			"is lost. An unmerged branch wt made is renamed out of the <type>_wt/\n" +
			"prefix, so the work survives its worktree; an unmerged branch wt did\n" +
			"not make is left exactly as it is.\n\n" +
			"Merged means the branch's tip is reachable from origin/<trunk> as last\n" +
			"fetched, or from the local trunk, so a pull request merged on the\n" +
			"remote counts even while the main checkout's trunk is behind. Remove\n" +
			"never fetches. A branch that moves after the plan is kept.\n\n" +
			"A squash or rebase merge leaves no such tip, so git reads the branch as\n" +
			"unmerged for ever. Where `wt list` or `wt sweep` has already recorded\n" +
			"that GitHub merged this branch into trunk at exactly this commit, remove\n" +
			"deletes it too, and says which pull request it believed. It reads that\n" +
			"from the file they left behind and never from the network: remove runs\n" +
			"from git hooks. With nothing recorded, the branch is kept as it always\n" +
			"was.\n\n" +
			"A branch whose every commit is on trunk already under another id — a\n" +
			"rebase merge whose pull request carried the rebased tip, or a\n" +
			"cherry-pick — is deleted too, since nothing is lost: git cherry finds\n" +
			"each change on trunk, and each patch must match byte for byte, so one\n" +
			"trunk has only with other whitespace keeps it. A merge commit or an\n" +
			"empty commit on the branch keeps it, because neither can be found\n" +
			"that way.\n\n" +
			"The plan says where the branch stands either way — merged, or how many\n" +
			"commits ahead of origin/<trunk> (or trunk, without it) it is — because\n" +
			"that is the fact the whole decision turns on. \"clean\" is about the\n" +
			"checkout, not the branch: it means nothing is uncommitted.\n\n" +
			"The worktree can be named by anything `wt list` prints — the work name,\n" +
			"the branch, or the path — or by . for the one you are standing in.\n" +
			"Matching is exact and stays inside this repository; a work name used\n" +
			"under two types is named with its type, as in wt remove fix/login-crash.\n\n" +
			"What the removal will do is printed before it does it. In a terminal you\n" +
			"are then asked to confirm; --yes skips the question, and a script or hook\n" +
			"with no terminal is never asked: it named the worktree. --dry-run prints\n" +
			"the plan and stops.\n\n" +
			"Before the question, removal refuses a worktree with anything\n" +
			"uncommitted — in a submodule too, whatever git's config says — a status\n" +
			"that cannot be read, a rebase, merge, cherry-pick, revert or bisect in\n" +
			"progress, a HEAD holding commits no branch has, a submodule commit only\n" +
			"this worktree holds, or another worktree inside it. No flag goes past\n" +
			"those: commit, discard or finish first. The checkout is read once more\n" +
			"right before it is deleted.\n\n" +
			"It also refuses a worktree a Claude session is working in, idle or busy,\n" +
			"or whose sessions cannot be listed, and one with files git status is\n" +
			"told not to look at (assume-unchanged, skip-worktree). The session\n" +
			"running wt remove — the one a WorktreeRemove hook fires in — does not\n" +
			"count, and without claude on the PATH there are none. A git lock whose\n" +
			"process has exited is released; one whose holder is still running\n" +
			"refuses. --force goes past a session, a failed listing, a held lock,\n" +
			"and hidden files still as committed; an edited one is uncommitted work.\n\n" +
			"--quarantine <dir> moves the worktree aside instead of deleting it.\n" +
			"<dir> is a new folder on the worktree's volume — its parent must exist\n" +
			"— outside every checkout and the git dir; removal refuses, changing\n" +
			"nothing, when it is not: only renames, never a copy. After the last\n" +
			"check it writes <dir>/recovery.json, locks the worktree with the reason\n" +
			"\"wt quarantine <dir>\", pins its commits with refs under\n" +
			"refs/wt-quarantine/ so gc keeps them, moves the checkout to\n" +
			"<dir>/checkout and its admin dir, .git/worktrees/<id> with its\n" +
			"submodules' repositories, to <dir>/admin, which unregisters it; then\n" +
			"the branch goes as it would. A file written after the check moves with\n" +
			"the checkout. Each step is journalled in recovery.json; wt restore <dir>\n" +
			"puts it all back, and wt sweep drops the pins once <dir> is deleted.\n\n" +
			"With `wt config set superset true`, once the worktree has left its path\n" +
			"and git no longer lists it, its Superset workspace is deleted too.",
		Example: "  wt remove login-crash            # say where the branch stands, then ask\n" +
			"  wt remove login-crash --dry-run  # what it would do; change nothing\n" +
			"  wt remove login-crash --yes      # do not ask (scripts, hooks)\n" +
			"  wt remove . --force              # the one you are in, past a session\n" +
			"  wt remove login-crash --quarantine ../trash/lc  # move it aside",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !me && meAt == "" && len(args) != 1 {
				return fmt.Errorf("needs the worktree to remove, or . for the one " +
					"you are in — for example: wt remove login-crash")
			}
			ctx, err := openContext()
			if err != nil {
				return err
			}
			opts := commands.RemoveOptions{Force: force, DryRun: dryRun}
			if quarantineDir != "" {
				if opts.Quarantine, err = filepath.Abs(quarantineDir); err != nil {
					return err
				}
			}
			if !yes && canAsk(cmd) {
				opts.Confirm = confirmRemoval(newPrompter(cmd.InOrStdin(), cmd.OutOrStdout()))
			}
			if meAt != "" {
				return commands.RemoveAt(ctx, meAt, opts, cmd.OutOrStdout())
			}
			if me {
				return commands.RemoveAt(ctx, ctx.Cwd, opts, cmd.OutOrStdout())
			}
			return commands.Remove(ctx, args[0], opts, cmd.OutOrStdout())
		},
	}
	// . says the same and is what every other command takes; --me stays for
	// the lines already written with it.
	cmd.Flags().BoolVar(&me, "me", false, "remove the worktree you are standing in")
	_ = cmd.Flags().MarkHidden("me")
	cmd.Flags().BoolVarP(&force, "force", "f", false,
		"go past a session, a held lock, or files hidden from status")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what removal would do and change nothing")
	cmd.Flags().StringVar(&quarantineDir, "quarantine", "",
		"move it into this new folder instead of deleting it")
	cmd.MarkFlagsMutuallyExclusive("yes", "dry-run")
	cmd.Flags().StringVar(&meAt, "me-at", "", "remove the worktree at this path (used by wt_rm_me)")
	_ = cmd.Flags().MarkHidden("me-at")
	return cmd
}

// confirmRemoval asks the question, defaulting to no. The plan has already been
// printed by the time this runs, so the prompt itself stays one line.
func confirmRemoval(p *prompter) func(commands.Plan) (bool, error) {
	return func(commands.Plan) (bool, error) { return p.yesNo("Remove it?", false), nil }
}
