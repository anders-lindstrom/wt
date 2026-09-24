package commands

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
)

// Config prints the resolved configuration. With shell set, it emits
// eval-able assignments using the *legacy* variable names, because the Herdr
// skills document those as the output of load_worktree_config. REPO_NAME and
// AWS_SETUP_ENABLED are retired as inputs but still produced here: the first is
// derived, the second reports whether bin/worktree/provision.sh exists.
func Config(ctx *Context, shell bool, w io.Writer) error {
	c := ctx.Config
	if !shell {
		fmt.Fprintf(w, "repo:          %s\n", ctx.Repo.Name)
		fmt.Fprintf(w, "main root:     %s\n", ctx.Repo.MainRoot)
		fmt.Fprintf(w, "trunk:         %s\n", c.MainBranch)
		fmt.Fprintf(w, "branch prefix: %s\n", c.BranchPrefix)
		fmt.Fprintf(w, "default type:  %s\n", c.DefaultType)
		suffix, origin := ctx.BranchSuffix()
		if suffix == c.TypeSuffix {
			fmt.Fprintf(w, "branch suffix: %q (%s)\n", suffix, origin)
		} else {
			fmt.Fprintf(w, "branch suffix: %q (%s); worktree paths keep %q\n",
				suffix, origin, c.TypeSuffix)
		}
		fmt.Fprintf(w, "types:         %s\n", strings.Join(c.Types, " "))
		if names, origin := ctx.TypeNames(); len(names) > 0 {
			fmt.Fprintf(w, "type names:    %s (%s); worktree paths keep the type\n",
				strings.Join(typeNamePairs(ctx), " "), origin)
		}
		fmt.Fprintf(w, "config dirs:   %s\n", strings.Join(c.DeveloperConfigDirs, " "))
		fmt.Fprintf(w, "config files:  %s\n", strings.Join(c.DeveloperConfigFiles, " "))
		fmt.Fprintf(w, "required bins: %s\n", strings.Join(c.RequiredBins, " "))
		fmt.Fprintf(w, "build init:    %v %s\n", c.BuildInitEnabled, c.BuildInitCommand)
		fmt.Fprintf(w, "provision.sh:  %v\n", ctx.HasProvisionScript())
		userBlock(ctx, w)
		return nil
	}

	fmt.Fprintf(w, "REPO_NAME=%s\n", shellQuote(ctx.Repo.Name))
	fmt.Fprintf(w, "MAIN_BRANCH=%s\n", shellQuote(c.MainBranch))
	fmt.Fprintf(w, "WORKTREE_BRANCH_PREFIX=%s\n", shellQuote(c.BranchPrefix))
	fmt.Fprintf(w, "WORKTREE_TYPE_SUFFIX=%s\n", shellQuote(c.TypeSuffix))
	branchSuffix, _ := ctx.BranchSuffix()
	fmt.Fprintf(w, "WORKTREE_BRANCH_SUFFIX=%s\n", shellQuote(branchSuffix))
	fmt.Fprintf(w, "WORKTREE_TYPE_NAMES=(%s)\n", shellQuoteAll(typeNamePairs(ctx)))
	fmt.Fprintf(w, "WORKTREE_DEFAULT_TYPE=%s\n", shellQuote(c.DefaultType))
	fmt.Fprintf(w, "WORKTREE_TYPES=%s\n", shellQuote(strings.Join(c.Types, " ")))
	fmt.Fprintf(w, "REQUIRED_BINS=%s\n", shellQuote(strings.Join(c.RequiredBins, " ")))
	fmt.Fprintf(w, "BUILD_INIT_ENABLED=%v\n", c.BuildInitEnabled)
	fmt.Fprintf(w, "BUILD_INIT_COMMAND=%s\n", shellQuote(c.BuildInitCommand))
	fmt.Fprintf(w, "TEST_COMMAND=%s\n", shellQuote(c.TestCommand))
	fmt.Fprintf(w, "RUN_TESTS_BEFORE_REMOVE=%v\n", c.RunTestsBeforeRemove)
	fmt.Fprintf(w, "AWS_SETUP_ENABLED=%v\n", ctx.HasProvisionScript())
	fmt.Fprintf(w, "DEVELOPER_CONFIG_DIRS=(%s)\n", shellQuoteAll(c.DeveloperConfigDirs))
	fmt.Fprintf(w, "DEVELOPER_CONFIG_FILES=(%s)\n", shellQuoteAll(c.DeveloperConfigFiles))
	return nil
}

// typeNamePairs is this repository's resolved naming as the configuration
// files write it.
func typeNamePairs(ctx *Context) []string {
	names, _ := ctx.TypeNames()
	return typeNamePairsOf(names, ctx.Config.Types)
}

// shellQuote single-quotes a value so that eval cannot reinterpret it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellQuoteAll(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, s := range items {
		quoted = append(quoted, shellQuote(s))
	}
	return strings.Join(quoted, " ")
}

// userBlock prints the user settings with where each value came from, then
// the Superset mode the two files resolve to. --shell gets none of it: the
// Herdr skills eval that output.
func userBlock(ctx *Context, w io.Writer) {
	u := ctx.UserConfig()
	where := u.Path
	switch {
	case !u.Exists:
		where += " (no file yet)"
	case u.Unusable:
		where += " (wt cannot read it, so every integration is off)"
	}
	fmt.Fprintf(w, "user config:   %s\n", where)
	for _, name := range config.UserKeyNames() {
		v, err := u.Value(name)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "  %-14s %v (%s)\n", name+":", config.UserLiteral(name, v), u.Origin(name))
	}
	roots, origin := RootsFor(u)
	fmt.Fprintf(w, "roots:         (%s)\n", origin)
	for _, root := range roots {
		fmt.Fprintf(w, "  %-14s %s\n", root.Name+":", root.Path)
	}
	for _, p := range u.Profiles {
		fmt.Fprintf(w, "profile %s:\n", p.Name)
		for _, r := range p.Repos {
			fmt.Fprintf(w, "  %s\n", r)
		}
	}
	mode, origin := supersetOrigin(ctx)
	fmt.Fprintf(w, "superset mode: %s (%s)\n", mode, origin)
}

// supersetOrigin resolves the Superset setting across both files and names
// the one that decided it. With the user setting off, SUPERSET_REGISTER is
// never reached.
func supersetOrigin(ctx *Context) (config.SupersetMode, string) {
	if !ctx.UserConfig().Superset {
		return config.SupersetOff, "user config"
	}
	if ctx.Config.SupersetRegisterSet {
		return ctx.Config.SupersetRegister, "repo file"
	}
	return ctx.Config.SupersetRegister, "repo default"
}

// UserGet prints one setting's value, alone, for a script to read: the value
// wt would use, which for a file wt cannot read is off.
//
// What is wrong with the file goes to errw and the exit stays 0. A script
// asking what the setting is gets the answer wt itself is acting on; an
// unknown key is still an error, because then there is no answer to give.
func UserGet(name string, w, errw io.Writer) error {
	u, loadErr := config.LoadUser()
	v, err := u.Value(name)
	if err != nil {
		return err
	}
	if loadErr != nil {
		fmt.Fprintf(errw, "wt: %v\n", loadErr)
	}
	fmt.Fprintln(w, v)
	return nil
}

// UserSet writes one setting to the user file. It is the only thing that
// creates that file.
func UserSet(name, value string, w io.Writer) error {
	path, set, err := config.SetUser(name, value)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s = %s in %s\n", name, set, path)
	if strings.HasPrefix(name, "root.") && os.Getenv("WT_ROOTS") != "" {
		fmt.Fprintln(w, "note: WT_ROOTS is set in this environment, and stands in for every root the file names")
	}
	return nil
}

// UserUnset takes one setting back out of the user file. A key that was never
// written is not an error.
func UserUnset(name string, w io.Writer) error {
	path, removed, err := config.UnsetUser(name)
	if err != nil {
		return err
	}
	if config.IsTableKey(name) {
		if !removed {
			fmt.Fprintf(w, "%s was not set in %s\n", name, path)
			return nil
		}
		fmt.Fprintf(w, "%s removed from %s\n", name, path)
		return nil
	}
	def, err := config.DefaultUser().Value(name)
	if err != nil {
		return err
	}
	if !removed {
		fmt.Fprintf(w, "%s was not set in %s; it is %v\n", name, path, config.UserLiteral(name, def))
		return nil
	}
	fmt.Fprintf(w, "%s removed from %s; back to %v\n", name, path, config.UserLiteral(name, def))
	return nil
}

// UserConfigPath prints the user file's path, whether or not it exists.
func UserConfigPath(w io.Writer) error {
	path, err := config.UserPath()
	if err != nil {
		return err
	}
	fmt.Fprintln(w, path)
	return nil
}
