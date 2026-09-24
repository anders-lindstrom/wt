package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/config"
)

func newDoctorCmd() *cobra.Command {
	var sel selectionFlags
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration, required tools and worktree health",
		Long: "Check what every other command depends on: that worktree.conf parses\n" +
			"and says something sensible, that your own settings do, that the tools\n" +
			"it requires are on the PATH, and that no worktree is nested, missing or\n" +
			"in a layout nothing owns. It changes nothing, and exits non-zero when\n" +
			"it found something.\n\n" +
			"A Superset section and a GitHub section say how far each integration\n" +
			"would get here and where it would stop. Neither counts as a problem on\n" +
			"its own — a machine without gh is an ordinary machine — except where a\n" +
			"repository asked for Superset with SUPERSET_REGISTER=on.\n\n" +
			"It also checks your roots and profiles, and outside any repository that\n" +
			"is all it checks. --all, --roots or --profile check every repository\n" +
			"they name instead: this doctor in each, and wt sync doctor where trunk\n" +
			"declares wt sync, one line per repository with what they found under it.\n\n" +
			selectionHelp,
		Example: "  wt doctor               # check this repository\n" +
			"  wt doctor; echo $?      # 0 when it found nothing, 1 when it did\n" +
			"  wt doctor --all         # every repository under your roots\n" +
			"  wt doctor --roots work  # the ones under one root\n" +
			"  wt doctor --profile api # the ones a profile names",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sel.selection().Any() {
				return commands.DoctorAll(loadUserWarn(cmd.ErrOrStderr()), sel.selection(), cmd.OutOrStdout())
			}
			// Lenient on purpose: a repository whose configuration is the
			// problem is exactly the one that needs diagnosing.
			// io.Discard: doctor reports the configuration problem itself, below.
			ctx, err := openLenient(io.Discard)
			if err != nil {
				return err
			}
			if ctx == nil {
				// Outside every repository, the roots and profiles are all
				// there is to check.
				u, userErr := config.LoadUser()
				if n := commands.DoctorRepositories(u, userErr, cmd.OutOrStdout()); n > 0 {
					return fmt.Errorf("%d problem(s) found", n)
				}
				return nil
			}
			problems, err := commands.Doctor(ctx, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if problems > 0 {
				return fmt.Errorf("%d problem(s) found", problems)
			}
			return nil
		},
	}
	sel.add(cmd, true)
	return cmd
}
