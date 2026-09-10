package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration, required tools and worktree health",
		Long: "Check what every other command depends on: that worktree.conf parses\n" +
			"and says something sensible, that the tools it requires are on the\n" +
			"PATH, and that no worktree is nested, missing or in a layout nothing\n" +
			"owns. It changes nothing, and exits non-zero when it found something.",
		Example: "  wt doctor        # check this repository\n" +
			"  wt doctor; echo $?   # 0 when clean, 1 when it found problems",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Lenient on purpose: a repository whose configuration is the
			// problem is exactly the one that needs diagnosing.
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			// io.Discard: doctor reports the configuration problem itself, below.
			ctx := commands.OpenLenient(cwd, io.Discard)
			if ctx == nil {
				return errors.New("not a git repository")
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
