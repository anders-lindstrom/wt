package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Config is a repository's validated worktree configuration.
type Config struct {
	MainBranch           string
	BranchPrefix         string
	TypeSuffix           string
	DefaultType          string
	Types                []string
	DeveloperConfigDirs  []string
	DeveloperConfigFiles []string
	BuildInitEnabled     bool
	BuildInitCommand     string
	RequiredBins         []string
	TestCommand          string
	RunTestsBeforeRemove bool
}

// DefaultTypes is the Conventional Commits set plus the two exploratory kinds
// that produce no feature, so a worktree's type and its commits share one
// vocabulary.
var DefaultTypes = []string{
	"feat", "fix", "docs", "style", "refactor", "perf",
	"test", "build", "ci", "chore", "revert", "research", "spike",
}

// retired maps a removed key to the sentence explaining what replaced it.
// These stay recognised so a stale config gets a real answer instead of
// "unknown key".
var retired = map[string]string{
	"REPO_NAME":         "REPO_NAME is retired; the repository name is derived from the main worktree",
	"AWS_SETUP_ENABLED": "AWS_SETUP_ENABLED is retired; put the step in an executable bin/worktree/provision.sh instead",
	"WORKTREE_LAYOUT":   "WORKTREE_LAYOUT is retired; the worktree path shape is no longer configurable",
}

var known = map[string]bool{
	"MAIN_BRANCH": true, "WORKTREE_BRANCH_PREFIX": true, "WORKTREE_TYPE_SUFFIX": true,
	"WORKTREE_DEFAULT_TYPE": true, "WORKTREE_TYPES": true,
	"DEVELOPER_CONFIG_DIRS": true, "DEVELOPER_CONFIG_FILES": true,
	"BUILD_INIT_ENABLED": true, "BUILD_INIT_COMMAND": true,
	"REQUIRED_BINS": true, "TEST_COMMAND": true, "RUN_TESTS_BEFORE_REMOVE": true,
}

// FromRaw validates parsed assignments into a Config, reporting every problem
// at once rather than stopping at the first.
func FromRaw(r map[string]Value, mainBranchFallback string) (*Config, error) {
	var problems []string

	for key := range r {
		if msg, ok := retired[key]; ok {
			problems = append(problems, msg)
			continue
		}
		if !known[key] {
			problems = append(problems, fmt.Sprintf("unknown key %q", key))
		}
	}

	c := &Config{
		MainBranch:   str(r, "MAIN_BRANCH", mainBranchFallback),
		BranchPrefix: str(r, "WORKTREE_BRANCH_PREFIX", "feat_wt"),
		TypeSuffix:   str(r, "WORKTREE_TYPE_SUFFIX", "_wt"),
		Types:        list(r, "WORKTREE_TYPES", DefaultTypes),
		DeveloperConfigDirs: list(r, "DEVELOPER_CONFIG_DIRS",
			[]string{".cursor", ".claude", ".run", ".vscode", ".idea"}),
		DeveloperConfigFiles: list(r, "DEVELOPER_CONFIG_FILES", nil),
		RequiredBins:         list(r, "REQUIRED_BINS", nil),
		BuildInitCommand:     str(r, "BUILD_INIT_COMMAND", ""),
		TestCommand:          str(r, "TEST_COMMAND", ""),
	}

	// Build init defaults to "on if a command was given". Defaulting it to true
	// while BUILD_INIT_COMMAND has no default would make an empty config invalid.
	var err error
	if c.BuildInitEnabled, err = boolean(r, "BUILD_INIT_ENABLED", c.BuildInitCommand != ""); err != nil {
		problems = append(problems, err.Error())
	}
	if c.RunTestsBeforeRemove, err = boolean(r, "RUN_TESTS_BEFORE_REMOVE", false); err != nil {
		problems = append(problems, err.Error())
	}

	c.DefaultType = str(r, "WORKTREE_DEFAULT_TYPE", "")
	if c.DefaultType == "" {
		c.DefaultType = strings.TrimSuffix(c.BranchPrefix, c.TypeSuffix)
	}
	if !contains(c.Types, c.DefaultType) {
		problems = append(problems, fmt.Sprintf(
			"WORKTREE_BRANCH_PREFIX=%q yields default type %q, which is not in WORKTREE_TYPES; set WORKTREE_DEFAULT_TYPE to choose one of: %s",
			c.BranchPrefix, c.DefaultType, strings.Join(c.Types, " ")))
	}

	if c.BuildInitEnabled && c.BuildInitCommand == "" {
		problems = append(problems, "BUILD_INIT_ENABLED is true but BUILD_INIT_COMMAND is not set")
	}
	if c.RunTestsBeforeRemove && c.TestCommand == "" {
		problems = append(problems, "RUN_TESTS_BEFORE_REMOVE is true but TEST_COMMAND is not set")
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("worktree.conf:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return c, nil
}

func str(r map[string]Value, key, def string) string {
	if v, ok := r[key]; ok && !v.IsList && v.Scalar != "" {
		return v.Scalar
	}
	return def
}

func list(r map[string]Value, key string, def []string) []string {
	v, ok := r[key]
	if !ok {
		return def
	}
	if v.IsList {
		return v.List
	}
	return strings.Fields(v.Scalar)
}

func boolean(r map[string]Value, key string, def bool) (bool, error) {
	v, ok := r[key]
	if !ok || v.IsList || v.Scalar == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v.Scalar)
	if err != nil {
		return def, fmt.Errorf("%s=%q is not a boolean", key, v.Scalar)
	}
	return b, nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
