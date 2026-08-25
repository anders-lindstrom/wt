package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ErrNoConfig means the repository declares no worktree configuration.
var ErrNoConfig = errors.New("no bin/worktree/worktree.conf or worktree.toml")

// tomlConfig mirrors Config with snake_case keys. It exists so a repo can opt
// into a typed format without the bash-subset parser, which stays the default
// so no repo has to convert.
type tomlConfig struct {
	MainBranch           string   `toml:"main_branch"`
	BranchPrefix         string   `toml:"worktree_branch_prefix"`
	TypeSuffix           string   `toml:"worktree_type_suffix"`
	DefaultType          string   `toml:"worktree_default_type"`
	Types                []string `toml:"worktree_types"`
	DeveloperConfigDirs  []string `toml:"developer_config_dirs"`
	DeveloperConfigFiles []string `toml:"developer_config_files"`
	BuildInitEnabled     *bool    `toml:"build_init_enabled"`
	BuildInitCommand     string   `toml:"build_init_command"`
	RequiredBins         []string `toml:"required_bins"`
	TestCommand          string   `toml:"test_command"`
	RunTestsBeforeRemove bool     `toml:"run_tests_before_remove"`
}

// Load reads a repository's worktree configuration, preferring worktree.toml.
func Load(repoRoot, mainBranchFallback string) (*Config, error) {
	dir := filepath.Join(repoRoot, "bin", "worktree")

	tomlPath := filepath.Join(dir, "worktree.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		return loadTOML(tomlPath, mainBranchFallback)
	}

	confPath := filepath.Join(dir, "worktree.conf")
	f, err := os.Open(confPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoConfig
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	raw, err := ParseBash(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", confPath, err)
	}
	return FromRaw(raw, mainBranchFallback)
}

func loadTOML(path, mainBranchFallback string) (*Config, error) {
	var tc tomlConfig
	if _, err := toml.DecodeFile(path, &tc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	raw := map[string]Value{}
	setStr := func(k, v string) {
		if v != "" {
			raw[k] = Value{Scalar: v}
		}
	}
	setList := func(k string, v []string) {
		if v != nil {
			raw[k] = Value{List: v, IsList: true}
		}
	}
	setStr("MAIN_BRANCH", tc.MainBranch)
	setStr("WORKTREE_BRANCH_PREFIX", tc.BranchPrefix)
	setStr("WORKTREE_TYPE_SUFFIX", tc.TypeSuffix)
	setStr("WORKTREE_DEFAULT_TYPE", tc.DefaultType)
	setStr("BUILD_INIT_COMMAND", tc.BuildInitCommand)
	setStr("TEST_COMMAND", tc.TestCommand)
	setList("WORKTREE_TYPES", tc.Types)
	setList("DEVELOPER_CONFIG_DIRS", tc.DeveloperConfigDirs)
	setList("DEVELOPER_CONFIG_FILES", tc.DeveloperConfigFiles)
	setList("REQUIRED_BINS", tc.RequiredBins)
	if tc.BuildInitEnabled != nil {
		raw["BUILD_INIT_ENABLED"] = Value{Scalar: fmt.Sprint(*tc.BuildInitEnabled)}
	}
	if tc.RunTestsBeforeRemove {
		raw["RUN_TESTS_BEFORE_REMOVE"] = Value{Scalar: "true"}
	}
	return FromRaw(raw, mainBranchFallback)
}
