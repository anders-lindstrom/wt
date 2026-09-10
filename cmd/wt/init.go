package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/repo"
)

func newInitCmd() *cobra.Command {
	var yes, force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create this repository's worktree configuration",
		Long: "Write bin/worktree/worktree.conf, which every other command needs.\n\n" +
			"Asks for the three keys a repository actually varies — the main branch,\n" +
			"the branch prefix, and the build command — offering detected values as\n" +
			"the defaults. Every other key is written commented at its default, so\n" +
			"the file is this repository's reference for what it may set.\n\n" +
			"With --yes, or with nothing on stdin to answer with, the detected\n" +
			"values are written without asking.",
		Example: "  wt init          # ask three questions, detected values offered\n" +
			"  wt init --yes    # write the detected values, ask nothing\n" +
			"  wt init --force  # replace a configuration that is already there",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			// Deliberately not openContext: that needs a configuration, which
			// is the thing this command is here to write.
			r, err := repo.Discover(cwd)
			if err != nil {
				return err
			}
			opts := commands.InitOptions{Force: force}
			if !yes && isTerminal(os.Stdin) {
				opts.Ask = askAnswers(cmd.InOrStdin(), cmd.OutOrStdout())
			}
			return commands.Init(r, opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask; write the detected values")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing configuration")
	return cmd
}

// askAnswers prompts for the three varying keys, offering what was detected —
// or, on a re-ask, what was just rejected, so a correction is an edit rather
// than a retype.
//
// One bufio.Reader spans every prompt and every re-ask: a reader built per
// question — or per round — buffers past its own line and eats the answers
// after it.
func askAnswers(in io.Reader, out io.Writer) func(commands.Answers) (commands.Answers, error) {
	r := bufio.NewReader(in)
	return func(defaults commands.Answers) (commands.Answers, error) {
		ask := func(label, def string) (string, error) {
			if def != "" {
				fmt.Fprintf(out, "%s [%s]: ", label, def)
			} else {
				fmt.Fprintf(out, "%s: ", label)
			}
			line, err := r.ReadString('\n')
			if err != nil && line == "" {
				// ^D: the answers are abandoned, not confirmed. Returning the
				// remaining defaults here would write a configuration nobody
				// agreed to.
				return "", errors.New("aborted")
			}
			if line = strings.TrimSpace(line); line == "" {
				return def, nil
			}
			return line, nil
		}

		var a commands.Answers
		var err error
		if a.MainBranch, err = ask("main branch", defaults.MainBranch); err != nil {
			return a, err
		}
		if a.BranchPrefix, err = ask("branch prefix", defaults.BranchPrefix); err != nil {
			return a, err
		}
		if a.BuildCommand, err = ask("build command (blank for none)", defaults.BuildCommand); err != nil {
			return a, err
		}
		return a, nil
	}
}
