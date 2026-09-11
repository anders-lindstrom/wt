package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/about"
)

// buildDate and commitDate are set by install.sh, like version. A plain
// `go build` leaves them empty and `wt about` then says nothing about when
// it was built.
var (
	buildDate  = ""
	commitDate = ""
)

func newAboutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "about",
		Short: "Print the version and what changed recently",
		Long: "Print which build of wt this is, when it was built, and what changed\n" +
			"recently: the newest 5 what's-new entries, or every entry from the last\n" +
			"3 days if that is more.",
		Example: "  wt about  # the build, and what landed recently",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), about.Text(version, buildDate, commitDate))
			return nil
		},
	}
}
