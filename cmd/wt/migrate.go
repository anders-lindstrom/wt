package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/naming"
)

func newMigrateCmd() *cobra.Command {
	var opts commands.MigrateOptions
	cmd := &cobra.Command{
		Use:     "migrate <worktree> [<type>/<name>]",
		Aliases: []string{"move"},
		Short:   "Move a worktree where it belongs, renaming it if you like",
		Long: "Move a worktree to the path this repository's layout gives it.\n\n" +
			"<worktree> is any of the three things `wt list` prints for it: the work\n" +
			"name, the branch, or the path. Add a destination to change the type, the\n" +
			"name, or both — the branch is renamed to match, because in this layout the\n" +
			"path and the branch are the same words.\n\n" +
			"With no destination the branch decides: one already in the convention\n" +
			"keeps its name, and one outside it (fix/idiotthings, axis_acc) is fitted\n" +
			"to the convention.\n\n" +
			"The move is git's own, so commits, stashes, uncommitted changes and\n" +
			"ignored files all travel with it — but tools holding the old absolute\n" +
			"path will not. Use --dry-run first on a worktree carrying work that\n" +
			"matters.",
		Example: "  wt migrate webkey                      # fit it to the layout\n" +
			"  wt migrate fix/idiotthings fix/local-gecko\n" +
			"  wt migrate ../server-controller_stats  # by path\n" +
			"  wt migrate stats chore/stats           # keep the name, change the type",
		Args:              migrateArgs,
		ValidArgsFunction: completeMigrate,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := openContext()
			if err != nil {
				return err
			}
			dest := ""
			if len(args) > 1 {
				dest = args[1]
			}
			path, err := commands.Migrate(ctx, args[0], dest, opts, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show what would happen, change nothing")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "move it even with an agent session working in it")
	return cmd
}

// migrateArgs takes one worktree and an optional destination, and says so in
// the terms the command's own help uses.
func migrateArgs(_ *cobra.Command, args []string) error {
	switch {
	case len(args) == 0:
		return fmt.Errorf("needs a worktree — for example: wt migrate webkey, " +
			"or wt migrate fix/idiotthings fix/local-gecko")
	case len(args) > 2:
		return fmt.Errorf("takes a worktree and at most one destination, got %d arguments — "+
			"for example: wt migrate fix/idiotthings fix/local-gecko", len(args))
	}
	return nil
}

// completeMigrate offers the worktrees for the first argument — by work name,
// or by branch for the ones outside the convention, which are the whole point
// of this command — and this repository's types for the second.
func completeMigrate(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	ctx, err := openContext()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) > 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, w := range worktrees {
		if w.IsMain || w.Branch == "" {
			continue
		}
		name := w.Branch
		if typ, work, ok := naming.ParseBranch(w.Branch, ctx.Config.TypeSuffix); ok {
			name = typ + "/" + work
		}
		if len(args) == 0 {
			out = append(out, name)
			continue
		}
		if name != args[0] && w.Branch != args[0] {
			continue
		}
		for _, t := range ctx.Config.Types {
			out = append(out, t+"/"+naming.StripPrefix(w.Branch, ctx.Config.TypeSuffix))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
