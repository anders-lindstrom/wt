package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newRemoveCmd() *cobra.Command {
	var me bool
	var meAt string
	var yes, force bool
	cmd := &cobra.Command{
		Use:     "remove <work>",
		Aliases: []string{"rm"},
		Short:   "Remove a worktree, deleting its branch only when merged",
		Long: "Remove the worktree and decide what happens to its branch. A branch\n" +
			"merged into the main branch is deleted, whoever created it: merged\n" +
			"means nothing is lost. An unmerged branch wt made is renamed out of\n" +
			"the <type>_wt/ prefix, so the work survives its worktree; an unmerged\n" +
			"branch wt did not make is left exactly as it is.\n\n" +
			"The plan says where the branch stands either way — merged, or how many\n" +
			"commits ahead of the main branch it is — because that is the fact the\n" +
			"whole decision turns on. \"clean\" is about the checkout, not the branch:\n" +
			"it means nothing is uncommitted.\n\n" +
			"The worktree can be named by anything `wt list` prints — the work name,\n" +
			"the branch, or the path. Matching is exact and stays inside this\n" +
			"repository; a work name used under two types has to be named by its\n" +
			"type as well.\n\n" +
			"What the removal will do is printed before it does it. In a terminal you\n" +
			"are then asked to confirm; --yes skips the question, and a script or hook\n" +
			"with no terminal is never asked.\n\n" +
			"A worktree can carry a git lock — an agent session takes one for the\n" +
			"directory it works in. A lock whose process has exited is released and\n" +
			"the removal goes ahead; one whose holder is still running stops it before\n" +
			"the question is asked, and --force is how you say you mean it anyway.",
		Example: "  wt remove login-crash          # say where the branch stands, then ask\n" +
			"  wt remove fix/login-crash      # when two types share a work name\n" +
			"  wt remove login-crash --yes    # do not ask (scripts, hooks)\n" +
			"  wt remove login-crash --force  # break a lock a session still holds\n" +
			"  wt remove --me                 # the worktree you are standing in",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !me && meAt == "" && len(args) != 1 {
				return fmt.Errorf("needs the worktree to remove, or --me to remove the " +
					"one you are in — for example: wt remove login-crash")
			}
			ctx, err := openContext()
			if err != nil {
				return err
			}
			opts := commands.RemoveOptions{Force: force}
			if !yes && isTerminal(os.Stdin) {
				opts.Confirm = confirmRemoval(cmd.InOrStdin(), cmd.OutOrStdout())
			}
			if meAt != "" {
				return commands.RemoveAt(ctx, meAt, opts, cmd.OutOrStdout())
			}
			if me {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				return commands.RemoveAt(ctx, cwd, opts, cmd.OutOrStdout())
			}
			return commands.Remove(ctx, args[0], opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&me, "me", false, "remove the worktree you are standing in")
	cmd.Flags().BoolVarP(&force, "force", "f", false,
		"break a git worktree lock whose holder is still running")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().StringVar(&meAt, "me-at", "", "remove the worktree at this path (used by wt_rm_me)")
	_ = cmd.Flags().MarkHidden("me-at")
	return cmd
}

// confirmRemoval asks the question, defaulting to no. The plan has already been
// printed by the time this runs, so the prompt itself stays one line.
func confirmRemoval(in io.Reader, out io.Writer) func(commands.Plan) (bool, error) {
	return func(commands.Plan) (bool, error) {
		return askYesNo(in, out, "Remove it? [y/N] "), nil
	}
}

// askYesNo prints question and reads one line, defaulting to no. EOF on a
// terminal is ^D: the user declined rather than answered.
func askYesNo(in io.Reader, out io.Writer, question string) bool {
	_, _ = fmt.Fprint(out, question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// isTerminal reports whether f is an interactive terminal, which is the whole
// of the question "is there anyone here to answer a prompt".
//
// This asks the kernel rather than reading the file mode. The usual
// ModeCharDevice test is wrong in exactly the case that matters: /dev/null is a
// character device, so a script or an agent redirecting stdin from it would be
// asked a question with nobody there to answer.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
