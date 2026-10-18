package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/compat"
	"github.com/anders-lindstrom/wt/shell"
)

func newShellLayerCmd() *cobra.Command {
	var compatLayer bool
	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Print wt's shell layer, for your shell rc",
		Long: "Print the shell functions behind `wt cd`, `wt exec` and `wt attach`,\n" +
			"for your shell rc to evaluate. They are part of this binary, so they\n" +
			"match it however wt was installed: by install.sh, mise or a download.\n" +
			"Add to ~/.zshrc or ~/.bashrc:\n" +
			"    eval \"$(wt shell)\"\n\n" +
			"With --compat it prints worktree_functions.sh instead, the functions\n" +
			"Herdr's skills source by name.",
		Example: "  wt shell           # the shell layer, for your shell rc to eval\n" +
			"  wt shell --compat  # worktree_functions.sh instead",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			text := shell.Layer
			if compatLayer {
				text = compat.Functions
			}
			_, err := fmt.Fprint(cmd.OutOrStdout(), text)
			return err
		},
	}
	cmd.Flags().BoolVar(&compatLayer, "compat", false,
		"print worktree_functions.sh instead")
	return cmd
}
