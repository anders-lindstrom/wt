package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/config"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
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
			"repository asked for Superset with SUPERSET_REGISTER=on.",
		Example: "  wt doctor          # check this repository\n" +
			"  wt doctor; echo $? # 0 when clean, 1 when it found problems",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
}
