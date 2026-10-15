package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "List a worktree's Claude sessions, and send one a message",
		Long: "Find the Claude sessions in a worktree and send one of them a message,\n" +
			"for a tool or a person who wants to ask the session working there\n" +
			"something. wt session list says which sessions there are and whether\n" +
			"each has an inbox; wt session send writes one message to one inbox.\n\n" +
			"A message is read between steps by a running session. A session with no\n" +
			"process, a parked background one, has no inbox: wt does not send to it\n" +
			"and does not wake it.",
		Example: "  wt session list login-crash       # the sessions there, and their inboxes\n" +
			"  wt session list --json            # the same, for a tool\n" +
			"  wt session send login-crash < question.txt  # a message to the session",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newSessionListCmd(), newSessionSendCmd())
	return cmd
}

func newSessionListCmd() *cobra.Command {
	var asJSON, noInbox bool
	var sel selectionFlags
	cmd := &cobra.Command{
		Use:   "list [<work>]",
		Short: "List the Claude sessions in a worktree, with their inboxes",
		Long: "List the Claude sessions in a worktree, the way wt attach finds them:\n" +
			"<work> is matched as wt find matches; with none it is the worktree you\n" +
			"are in, and from the main checkout every worktree of this repository.\n" +
			"Each row says whether the session has an inbox a message can be written\n" +
			"to, or why not: it has no process (a parked background session), wt\n" +
			"runs under it, claude's registry does not name it, its socket is not\n" +
			"yours, or it did not accept a connection. wt connects to each inbox to\n" +
			"find out and writes nothing; --no-inbox-check leaves the connection\n" +
			"out, for a listing made often. Codex sessions are listed too, with no\n" +
			"inbox.\n\n" +
			"--all, --roots or --profile list every repository's sessions instead,\n" +
			"from one read of claude's sessions, opening only the repositories that\n" +
			"hold one; the pattern is not taken with them.\n\n" +
			"--json prints one object on stdout: every session with its full session\n" +
			"id, pid, state, what it waits for, inbox, transcript, start time, last\n" +
			"activity (the transcript's mtime), since when its state holds, and which\n" +
			"one wt list shows. wt schema session-list prints its schema;\n" +
			"docs/json.md explains it.\n\n" +
			selectionHelp,
		Example: "  wt session list                  # this worktree's sessions\n" +
			"  wt session list login-crash      # another worktree's\n" +
			"  wt session list --all --json --no-inbox-check  # all repositories, often\n" +
			"  wt session list --roots work   # the repositories under one root\n" +
			"  wt session list --profile api  # a profile's repositories",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			o := commands.SessionListOptions{JSON: asJSON, NoInboxCheck: noInbox,
				Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()}
			if sel.selection().Any() {
				if len(args) > 0 {
					return usagef("--all, --roots and --profile list every repository: give no worktree with them")
				}
				o.Selection, o.User = sel.selection(), loadUserWarn(cmd.ErrOrStderr())
				return commands.SessionListRun(nil, o)
			}
			ctx, err := openLenient(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if len(args) > 0 {
				o.Pattern = args[0]
			}
			return commands.SessionListRun(ctx, o)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the sessions as JSON (default: a table)")
	cmd.Flags().BoolVar(&noInbox, "no-inbox-check", false, "do not connect to each inbox: take it from claude's registry")
	sel.add(cmd, true)
	return cmd
}

func newSessionSendCmd() *cobra.Command {
	var o commands.SessionSendOptions
	var messageFile string
	cmd := &cobra.Command{
		Use:   "send <work> [<session>]",
		Short: "Send a message to a worktree's running Claude session",
		Long: "Write the message on stdin, or in --message-file, to the inbox of one\n" +
			"running Claude session in a worktree. The session's Claude reads it\n" +
			"between steps, or starts a turn with it when idle, told that another\n" +
			"session sent it and not its user typed it.\n\n" +
			"<work> is matched as wt find matches. <session> picks one of the\n" +
			"worktree's sessions as wt attach does: an id, the start of the full\n" +
			"session id, or part of the name. With none and several running, wt\n" +
			"lists them and exits 2: it never sends to a guess. --expect-session\n" +
			"names the session by its full id, so that a send meant for one cannot\n" +
			"reach another: it is refused when the session there is not that one.\n\n" +
			"The message is refused when empty, or over 262144 bytes. It is wrapped\n" +
			"in a header naming the sender, --from-name (default wt). --reply-to\n" +
			"<uds:path> names a socket of yours in the inbox's directory where the\n" +
			"session may answer. The text of a message that contains the header's\n" +
			"own tags has their opening brackets written as &lt;.\n\n" +
			"Sent means the line was written to the inbox and the connection closed\n" +
			"cleanly: not that the session has read it. Nothing is retried, and a\n" +
			"session with no process is never sent to or resumed.\n\n" +
			"--dry-run resolves the session and reads the message, and sends nothing.\n" +
			"--json prints one object on stdout, also on failure; wt schema\n" +
			"session-send prints its schema, and docs/json.md explains it.",
		Example: "  wt session send crash < q.txt   # the one session running there\n" +
			"  wt session send crash review --message-file q.txt  # by part of its name\n" +
			"  wt session send crash --from-name bot --reply-to uds:/tmp/cc-socks/b.sock < q\n" +
			"  wt session send crash --expect-session $ID < q  # $ID: its full session id\n" +
			"  wt session send crash --dry-run --json < q  # who, which inbox, how big",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openLenient(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			o.Pattern = args[0]
			if len(args) > 1 {
				o.Session = args[1]
			}
			o.Out, o.Err = cmd.OutOrStdout(), cmd.ErrOrStderr()
			if messageFile != "" {
				f, err := os.Open(messageFile)
				if err != nil {
					return err
				}
				defer f.Close()
				o.In = f
			} else {
				o.In = cmd.InOrStdin()
				if f, ok := o.In.(*os.File); ok && isTerminal(f) {
					return usagef("the message is read from stdin or --message-file: " +
						"echo \"question\" | wt session send <work>")
				}
			}
			return commands.SessionSendRun(ctx, o)
		},
	}
	cmd.Flags().StringVar(&messageFile, "message-file", "", "read the message from this file (default: stdin)")
	cmd.Flags().StringVar(&o.FromName, "from-name", "wt", "the name the session shows for the sender")
	cmd.Flags().StringVar(&o.ReplyTo, "reply-to", "", "where the session may answer: uds:<socket path>")
	cmd.Flags().StringVar(&o.ExpectSession, "expect-session", "", "refuse unless the target is this full session id")
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "resolve the target and the message, and send nothing")
	cmd.Flags().BoolVar(&o.JSON, "json", false, "print the result as JSON (default: one line)")
	return cmd
}
