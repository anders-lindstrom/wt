package main

import (
	"errors"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/anders-lindstrom/wt/internal/commands"
)

// resumeFlag is --resume: a switch that also notes how many words stood
// before it. What comes before it names the worktree and what comes after it
// is the conversation to resume, so `wt attach --resume <id>` can never take
// the id for a worktree. pflag adds each word to the flag set's arguments as
// it reads the line, which is what NArg counts at the moment the flag is set.
type resumeFlag struct {
	on     bool
	before int
	flags  *pflag.FlagSet
}

func (r *resumeFlag) String() string   { return strconv.FormatBool(r.on) }
func (r *resumeFlag) Type() string     { return "bool" }
func (r *resumeFlag) IsBoolFlag() bool { return true }
func (r *resumeFlag) Set(v string) (err error) {
	r.on, err = strconv.ParseBool(v)
	r.before = r.flags.NArg()
	return err
}

func newAttachCmd() *cobra.Command {
	var opts commands.AttachOptions
	resume := &resumeFlag{}
	cmd := &cobra.Command{
		Use:   "attach [<pattern>] [<session>]",
		Short: "Open the Claude session in a worktree (shell)",
		Long: "Open the Claude Code session that is in a worktree, in this terminal.\n" +
			"The worktree is matched the way `wt find` matches. With no pattern it is\n" +
			"the one you are in, and from the main checkout it is every worktree of\n" +
			"this repository. `wt list` shows the sessions in its SESSION column.\n\n" +
			"One background session is opened with `claude attach`; leaving it does\n" +
			"not stop it. Several are listed with a number each, and you are asked\n" +
			"which. <session> picks one without the question: its id, the start of\n" +
			"its full session id, or part of its name. A session open in a terminal\n" +
			"is listed with claude's process id there, and is used there: it cannot\n" +
			"be opened from another.\n\n" +
			"A session's id works in place of the pattern too, the short one or the\n" +
			"full one. When the same text also matches a worktree, wt opens neither\n" +
			"and says how to name each.\n\n" +
			"--resume continues a conversation in the worktree instead. What stands\n" +
			"before --resume names the worktree, as above; what stands after it is\n" +
			"the conversation, handed to claude as it is:\n" +
			"    wt attach <pattern> --resume        the most recent one there\n" +
			"    wt attach <pattern> --resume <id>   the one with that session id\n" +
			"The first runs `claude --continue`; where the worktree has no earlier\n" +
			"conversation, claude says so and starts nothing. The second runs\n" +
			"`claude --resume <id>`. Both refuse a conversation claude still lists as\n" +
			"a session, so that none is open twice: attach to that one. Your shell\n" +
			"stays where it is.\n\n" +
			"Implemented in wt's shell layer, so that a resumed session is started\n" +
			"by your shell's own `claude`. Without it, or with no terminal, nothing\n" +
			"is started and wt prints the command to run. Enable it with:\n" +
			"    " + sourceShellLayer,
		Example: "  wt attach login-crash           # the session there; asks when several\n" +
			"  wt attach login-crash review    # the one with \"review\" in its name\n" +
			"  wt attach 3f9a1c20              # a session by its id, wherever it is\n" +
			"  wt attach                       # this worktree's, or the repository's\n" +
			"  wt attach login-crash --resume  # continue its last conversation",
		Args:              cobra.MaximumNArgs(2),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Not being in a repository is fine: the pattern is searched for
			// under the roots, as wt find does.
			ctx, err := openLenient(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if opts.Resume = resume.on; opts.Resume {
				before, after := args[:min(resume.before, len(args))], args[min(resume.before, len(args)):]
				if len(before) > 1 || len(after) > 1 {
					return errors.New("with --resume, the worktree goes before it and the session id after it: " +
						"wt attach <pattern> --resume <id>")
				}
				args = []string{"", ""}
				copy(args, before)
				copy(args[1:], after)
			}
			if len(args) > 0 {
				opts.Pattern = args[0]
			}
			if len(args) > 1 {
				opts.Session = args[1]
			}
			opts.In, opts.Out, opts.Err = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			// The shell layer holds stdout, so the question goes to stderr.
			if f, ok := opts.Err.(*os.File); ok && opts.ForShell {
				opts.Ask = canAsk(cmd) && isTerminal(f)
			}
			if f, ok := opts.Out.(*os.File); ok && !opts.ForShell {
				opts.Terminal = canAsk(cmd) && isTerminal(f)
			}
			return commands.Attach(ctx, opts)
		},
	}
	resume.flags = cmd.Flags()
	cmd.Flags().Var(resume, "resume", "continue a conversation in the worktree instead of attaching to a session")
	cmd.Flags().Lookup("resume").NoOptDefVal = "true"
	cmd.Flags().BoolVar(&opts.ForShell, "for-shell", false, "print what the shell layer is to run")
	_ = cmd.Flags().MarkHidden("for-shell")
	return cmd
}
