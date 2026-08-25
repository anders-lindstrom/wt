package commands

import (
	"fmt"
	"io"
	"strings"
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
		fmt.Fprintf(w, "main branch:   %s\n", c.MainBranch)
		fmt.Fprintf(w, "branch prefix: %s\n", c.BranchPrefix)
		fmt.Fprintf(w, "default type:  %s\n", c.DefaultType)
		fmt.Fprintf(w, "types:         %s\n", strings.Join(c.Types, " "))
		fmt.Fprintf(w, "config dirs:   %s\n", strings.Join(c.DeveloperConfigDirs, " "))
		fmt.Fprintf(w, "config files:  %s\n", strings.Join(c.DeveloperConfigFiles, " "))
		fmt.Fprintf(w, "required bins: %s\n", strings.Join(c.RequiredBins, " "))
		fmt.Fprintf(w, "build init:    %v %s\n", c.BuildInitEnabled, c.BuildInitCommand)
		fmt.Fprintf(w, "provision.sh:  %v\n", ctx.HasProvisionScript())
		return nil
	}

	fmt.Fprintf(w, "REPO_NAME=%s\n", shellQuote(ctx.Repo.Name))
	fmt.Fprintf(w, "MAIN_BRANCH=%s\n", shellQuote(c.MainBranch))
	fmt.Fprintf(w, "WORKTREE_BRANCH_PREFIX=%s\n", shellQuote(c.BranchPrefix))
	fmt.Fprintf(w, "WORKTREE_TYPE_SUFFIX=%s\n", shellQuote(c.TypeSuffix))
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
