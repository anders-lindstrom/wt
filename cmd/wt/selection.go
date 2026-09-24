package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/config"
)

// selectionFlags are the flags that turn a command on one repository into one
// on many: --all, --roots and --profile, spelled the same on every command
// that takes them.
type selectionFlags struct {
	all      bool
	roots    []string
	profiles []string
}

// add puts the flags on cmd. withAll is false for a command that covers every
// repository already when given none of them, where --all would say nothing.
func (s *selectionFlags) add(cmd *cobra.Command, withAll bool) {
	if withAll {
		cmd.Flags().BoolVar(&s.all, "all", false, "every repository wt manages under your roots")
	}
	cmd.Flags().StringSliceVar(&s.roots, "roots", nil, "only the repositories under these roots, by name")
	cmd.Flags().StringSliceVar(&s.profiles, "profile", nil, "only the repositories these profiles name, by name")
	cmd.MarkFlagsMutuallyExclusive("roots", "profile")
	_ = cmd.RegisterFlagCompletionFunc("roots", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		u, _ := config.LoadUser()
		roots, _ := commands.RootsFor(u)
		var names []string
		for _, r := range roots {
			names = append(names, r.Name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("profile", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		u, _ := config.LoadUser()
		var names []string
		for _, p := range u.Profiles {
			names = append(names, p.Name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})
}

func (s selectionFlags) selection() commands.Selection {
	return commands.Selection{All: s.all, Roots: s.roots, Profiles: s.profiles}
}

// selectionHelp is the paragraph every command taking the flags adds to its
// help, so the three read the same everywhere.
const selectionHelp = "Your roots are the directories your repositories sit in, one level\n" +
	"down, or single repositories such as ~/dotfiles, searched no deeper.\n" +
	"They are WT_ROOTS when set, else the [roots] table of your config file\n" +
	"(wt config set root.<name> <dir>), else wt's defaults. A profile is a\n" +
	"named list of repositories (wt config set profile.<name> \"<dir> <dir>\").\n" +
	"Only repositories with a bin/worktree configuration count."

// loadUserWarn reads the user file, saying on errw what is wrong with it; the
// command goes on with what did parse.
func loadUserWarn(errw io.Writer) *config.User {
	u, err := config.LoadUser()
	if err != nil {
		fmt.Fprintf(errw, "wt: %v\n", err)
	}
	return u
}
