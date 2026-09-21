package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Config is a repository's validated worktree configuration.
type Config struct {
	MainBranch           string
	BranchPrefix         string
	TypeSuffix           string
	BranchSuffix         string
	DefaultType          string
	Types                []string
	DeveloperConfigDirs  []string
	DeveloperConfigFiles []string
	BuildInitEnabled     bool
	BuildInitCommand     string
	RequiredBins         []string
	TestCommand          string
	RunTestsBeforeRemove bool
	SupersetRegister     SupersetMode
	// MainBranchSet says MAIN_BRANCH came from the configuration rather than
	// from the fallback, which is only a guess from origin or the checkout.
	MainBranchSet bool
	// BranchSuffixSet says WORKTREE_BRANCH_SUFFIX was written in the file.
	// Only then does the repository outrank the person's own setting; a
	// repository that says nothing leaves the choice to whoever clones it.
	BranchSuffixSet bool
	// SupersetRegisterSet says SUPERSET_REGISTER was written in the file
	// rather than left at its default, which is what `wt config` reports as
	// the value's origin.
	SupersetRegisterSet bool
}

// The keys a repository may set. They are named constants because `wt init`
// writes some of them while the loader reads all of them, and a name spelled
// differently in those two places is a key that quietly stops working.
const (
	KeyMainBranch           = "MAIN_BRANCH"
	KeyBranchPrefix         = "WORKTREE_BRANCH_PREFIX"
	KeyTypeSuffix           = "WORKTREE_TYPE_SUFFIX"
	KeyBranchSuffix         = "WORKTREE_BRANCH_SUFFIX"
	KeyDefaultType          = "WORKTREE_DEFAULT_TYPE"
	KeyTypes                = "WORKTREE_TYPES"
	KeyConfigDirs           = "DEVELOPER_CONFIG_DIRS"
	KeyConfigFiles          = "DEVELOPER_CONFIG_FILES"
	KeyBuildInitEnabled     = "BUILD_INIT_ENABLED"
	KeyBuildInitCommand     = "BUILD_INIT_COMMAND"
	KeyRequiredBins         = "REQUIRED_BINS"
	KeyTestCommand          = "TEST_COMMAND"
	KeyRunTestsBeforeRemove = "RUN_TESTS_BEFORE_REMOVE"
	KeySupersetRegister     = "SUPERSET_REGISTER"
)

// DefaultTypeSuffix marks a type in a worktree path, and in the branch name
// that goes with it, unless a repository says otherwise.
const DefaultTypeSuffix = "_wt"

// SupersetMode says whether a worktree wt creates is also registered as a
// workspace in the Superset desktop app. It is read only once the person has
// turned the integration on with `wt config set superset true`; until then wt
// runs no Superset at all, whatever this says.
type SupersetMode string

const (
	// SupersetAuto registers when Superset is installed, its host service is
	// running and this repository is one of its projects. The default: a
	// machine without Superset, and a repository Superset does not track, are
	// ordinary states and pass in silence. A Superset that is there and would
	// not answer is a line.
	SupersetAuto SupersetMode = "auto"
	// SupersetOn is auto for a repository that asked for registration: every
	// way it can stop is a line, and `wt doctor` counts an unusable Superset
	// as a problem. Registration is still never fatal.
	SupersetOn SupersetMode = "on"
	// SupersetOff never runs Superset. `wt doctor` still names the mode, so
	// there is somewhere to read why nothing is being registered.
	SupersetOff SupersetMode = "off"
)

// SupersetModes is every value SUPERSET_REGISTER accepts.
var SupersetModes = []SupersetMode{SupersetAuto, SupersetOn, SupersetOff}

// kind is how a key's value is written.
type kind int

const (
	kindString kind = iota
	kindList
	kindBool
)

// key is one configuration key: its name, the key worktree.toml uses for the
// same setting, and how the value is written.
type key struct {
	Name string
	TOML string
	Kind kind
}

// keys is every key wt accepts, and the only list of them there is: the
// unknown-key check and the TOML reader are both derived from it, so a new key
// is a row here and the line in FromRaw that reads it.
var keys = []key{
	{KeyMainBranch, "main_branch", kindString},
	{KeyBranchPrefix, "worktree_branch_prefix", kindString},
	{KeyTypeSuffix, "worktree_type_suffix", kindString},
	{KeyBranchSuffix, "worktree_branch_suffix", kindString},
	{KeyDefaultType, "worktree_default_type", kindString},
	{KeyTypes, "worktree_types", kindList},
	{KeyConfigDirs, "developer_config_dirs", kindList},
	{KeyConfigFiles, "developer_config_files", kindList},
	{KeyBuildInitEnabled, "build_init_enabled", kindBool},
	{KeyBuildInitCommand, "build_init_command", kindString},
	{KeyRequiredBins, "required_bins", kindList},
	{KeyTestCommand, "test_command", kindString},
	{KeyRunTestsBeforeRemove, "run_tests_before_remove", kindBool},
	{KeySupersetRegister, "superset_register", kindString},
}

// isKnown reports whether a key is one wt reads.
func isKnown(name string) bool {
	return slices.ContainsFunc(keys, func(k key) bool { return k.Name == name })
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

// FromRaw validates parsed assignments into a Config, reporting every problem
// at once rather than stopping at the first.
//
// The Config is returned even when there are problems. Callers that must have a
// valid configuration check the error and discard it; `wt doctor` deliberately
// keeps it, so one retired key does not hide every other value in the file.
func FromRaw(r map[string]Value, mainBranchFallback string) (*Config, error) {
	return fromRaw(r, mainBranchFallback, "worktree.conf")
}

// fromRaw is FromRaw with the problems headed by the file they were read
// from, so the same key gets the same sentence under either file's name.
func fromRaw(r map[string]Value, mainBranchFallback, file string) (*Config, error) {
	var problems []string

	for name := range r {
		if msg, ok := retired[name]; ok {
			problems = append(problems, msg)
			continue
		}
		if !isKnown(name) {
			problems = append(problems, fmt.Sprintf("unknown key %q", name))
		}
	}

	// The prefix is a type spelled with this repository's own suffix, so a
	// repository that changes the suffix needs no second key changed to stay
	// consistent: the default is feat_wt, and feat-wt under a "-wt" suffix.
	ts := str(r, KeyTypeSuffix, DefaultTypeSuffix)
	c := &Config{
		MainBranch:   str(r, KeyMainBranch, mainBranchFallback),
		BranchPrefix: str(r, KeyBranchPrefix, "feat"+ts),
		TypeSuffix:   ts,
		BranchSuffix: branchSuffix(r, ts),
		Types:        list(r, KeyTypes, DefaultTypes),
		DeveloperConfigDirs: list(r, KeyConfigDirs,
			[]string{".cursor", ".claude", ".run", ".vscode", ".idea"}),
		DeveloperConfigFiles: list(r, KeyConfigFiles, nil),
		RequiredBins:         list(r, KeyRequiredBins, nil),
		BuildInitCommand:     str(r, KeyBuildInitCommand, ""),
		TestCommand:          str(r, KeyTestCommand, ""),
	}

	if v, ok := r[KeyMainBranch]; ok && v.Scalar != "" {
		c.MainBranchSet = true
	}
	if v, ok := r[KeyBranchSuffix]; ok && !v.IsList {
		c.BranchSuffixSet = true
	}
	if v, ok := r[KeySupersetRegister]; ok && !v.IsList && v.Scalar != "" {
		c.SupersetRegisterSet = true
	}

	// Build init defaults to "on if a command was given". Defaulting it to true
	// while BUILD_INIT_COMMAND has no default would make an empty config invalid.
	var err error
	if c.BuildInitEnabled, err = boolean(r, KeyBuildInitEnabled, c.BuildInitCommand != ""); err != nil {
		problems = append(problems, err.Error())
	}
	if c.RunTestsBeforeRemove, err = boolean(r, KeyRunTestsBeforeRemove, false); err != nil {
		problems = append(problems, err.Error())
	}
	if c.SupersetRegister, err = supersetMode(r, KeySupersetRegister, SupersetAuto); err != nil {
		problems = append(problems, err.Error())
	}

	for _, k := range []struct{ name, value string }{
		{KeyTypeSuffix, c.TypeSuffix}, {KeyBranchSuffix, c.BranchSuffix},
	} {
		if strings.ContainsAny(k.value, "/ \t") {
			problems = append(problems, fmt.Sprintf(
				"%s=%q may not contain a slash or a space; it is what a name carries before one",
				k.name, k.value))
		}
	}

	c.DefaultType = str(r, KeyDefaultType, "")
	if c.DefaultType == "" {
		c.DefaultType = strings.TrimSuffix(c.BranchPrefix, c.TypeSuffix)
	}
	if !slices.Contains(c.Types, c.DefaultType) {
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
		return c, fmt.Errorf("%s:\n  - %s", file, strings.Join(problems, "\n  - "))
	}
	return c, nil
}

// branchSuffix reads WORKTREE_BRANCH_SUFFIX, where — alone among the string
// keys — an empty value is a value rather than an absent key:
// WORKTREE_BRANCH_SUFFIX="" is how a repository asks for feat/login-crash
// instead of feat_wt/login-crash. It names branches only; the worktrees sit
// where the type suffix puts them either way, which is why a repository can
// change it without moving anybody's checkouts.
func branchSuffix(r map[string]Value, typeSuffix string) string {
	if v, ok := r[KeyBranchSuffix]; ok && !v.IsList {
		return v.Scalar
	}
	return typeSuffix
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

func supersetMode(r map[string]Value, key string, def SupersetMode) (SupersetMode, error) {
	v, ok := r[key]
	if !ok {
		return def, nil
	}
	if v.IsList {
		return def, fmt.Errorf("%s=(%s) is a list; it takes one of: auto on off",
			key, strings.Join(v.List, " "))
	}
	if v.Scalar == "" {
		return def, nil
	}
	m := SupersetMode(strings.ToLower(v.Scalar))
	if !slices.Contains(SupersetModes, m) {
		return def, fmt.Errorf("%s=%q is not one of: auto on off", key, v.Scalar)
	}
	return m, nil
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
