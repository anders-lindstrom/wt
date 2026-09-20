package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/config"
)

func newConfigCmd() *cobra.Command {
	var shell bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print the resolved configuration, and change your own settings",
		Long: "Print the configuration as every command sees it: the repository's\n" +
			"values, the defaults it did not set, the main branch detected from\n" +
			"origin, and your own settings with where each one came from.\n\n" +
			"Your settings live in one file per machine, outside any repository:\n" +
			"$XDG_CONFIG_HOME/wt/config.toml, or ~/.config/wt/config.toml. It is\n" +
			"normal for it not to exist; `wt config set` is the only thing that\n" +
			"creates it, and the get, set, unset and path subcommands work from\n" +
			"anywhere, repository or not.\n\n" +
			"With --shell, emit eval-able assignments using the legacy variable\n" +
			"names that the Herdr skills and plugin expect from\n" +
			"load_worktree_config. Only the repository's values are in there.",
		Example: "  wt config          # the resolved configuration, typed\n" +
			"  wt config --shell  # the same as shell assignments, for eval",
		Args: cobra.NoArgs,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			return commands.Config(ctx, shell, cmd.OutOrStdout())
		}),
	}
	cmd.Flags().BoolVar(&shell, "shell", false, "emit eval-able shell assignments")
	cmd.AddCommand(newConfigGetCmd(), newConfigSetCmd(), newConfigUnsetCmd(), newConfigPathCmd())
	return cmd
}

// userKeyList is the settings listing every user-config subcommand shows.
func userKeyList() string {
	var b strings.Builder
	b.WriteString("Settings:\n")
	for _, name := range config.UserKeyNames() {
		b.WriteString("  " + name + " — " + config.UserKeyDoc(name) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print one of your settings",
		Long: "Print one setting's value alone, so a script can read it. The value\n" +
			"is what wt would use: the file's, the built-in default when the file\n" +
			"does not set it, or false when wt cannot read the file at all. What\n" +
			"is wrong with the file goes to stderr; only an unknown key fails.\n\n" +
			userKeyList(),
		Example: "  wt config get superset  # false until you turn it on\n" +
			"  wt config get github    # true unless you turned it off",
		Args:      needArgs(1, "<key>", "wt config get superset"),
		ValidArgs: config.UserKeyNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commands.UserGet(args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change one of your settings",
		Long: "Write one setting to your own configuration file, creating it if it\n" +
			"is not there. Values are true or false. The key and the value are\n" +
			"validated first, so a mistake writes nothing, and the comments and\n" +
			"ordering already in the file are kept.\n\n" +
			"This writes your file only. A repository's own bin/worktree config\n" +
			"is committed and edited by hand.\n\n" + userKeyList(),
		Example: "  wt config set superset true  # register new worktrees with Superset\n" +
			"  wt config set github false   # keep wt away from the GitHub CLI",
		Args:      needArgs(2, "<key> <value>", "wt config set superset true"),
		ValidArgs: config.UserKeyNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commands.UserSet(args[0], args[1], cmd.OutOrStdout())
		},
	}
}

func newConfigUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove one of your settings, back to its default",
		Long: "Take one setting out of your configuration file, so it falls back to\n" +
			"wt's built-in default. A key the file never set is not an error.\n\n" +
			userKeyList(),
		Example: "  wt config unset superset  # back to the built-in default\n" +
			"  wt config unset github    # the same for the GitHub integration",
		Args:      needArgs(1, "<key>", "wt config unset superset"),
		ValidArgs: config.UserKeyNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commands.UserUnset(args[0], cmd.OutOrStdout())
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path of your configuration file",
		Long: "Print the file `wt config set` writes to, whether or not it exists\n" +
			"yet: $XDG_CONFIG_HOME/wt/config.toml when the environment sets one,\n" +
			"and ~/.config/wt/config.toml otherwise.",
		Example: "  wt config path  # where your own settings live, if anywhere",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return commands.UserConfigPath(cmd.OutOrStdout())
		},
	}
}
