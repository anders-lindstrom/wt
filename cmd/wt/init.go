package main

import (
	"errors"
	"fmt"
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
			"Asks for the three keys a repository actually varies — trunk (MAIN_BRANCH),\n" +
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
			if !yes && canAsk(cmd) {
				opts.Ask = askAnswers(newPrompter(cmd.InOrStdin(), cmd.OutOrStdout()))
			}
			return commands.Init(r, opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask; write the detected values")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "replace an existing configuration")
	return cmd
}

// askAnswers prompts for the three varying keys, offering what was detected —
// or, on a re-ask, what was just rejected, so a correction is an edit rather
// than a retype.
//
// The one prompter spans every prompt and every re-ask, so no round loses the
// answers typed ahead for the next.
func askAnswers(p *prompter) func(commands.Answers) (commands.Answers, error) {
	return func(defaults commands.Answers) (commands.Answers, error) {
		ask := func(label, def string) (string, error) {
			if def != "" {
				fmt.Fprintf(p.out, "%s [%s]: ", label, def)
			} else {
				fmt.Fprintf(p.out, "%s: ", label)
			}
			line, err := p.r.ReadString('\n')
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
		if a.MainBranch, err = ask("trunk (MAIN_BRANCH)", defaults.MainBranch); err != nil {
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
