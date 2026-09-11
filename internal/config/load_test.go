package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeConf(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "bin", "worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReadsBashConf(t *testing.T) {
	root := t.TempDir()
	writeConf(t, root, "worktree.conf", "MAIN_BRANCH=\"development\"\nBUILD_INIT_ENABLED=false\n")
	c, err := Load(root, "main")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.MainBranch != "development" {
		t.Errorf("MainBranch = %q", c.MainBranch)
	}
}

func TestLoadPrefersToml(t *testing.T) {
	root := t.TempDir()
	writeConf(t, root, "worktree.conf", "MAIN_BRANCH=\"from-conf\"\nBUILD_INIT_ENABLED=false\n")
	writeConf(t, root, "worktree.toml", "main_branch = \"from-toml\"\nbuild_init_enabled = false\n")
	c, err := Load(root, "main")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.MainBranch != "from-toml" {
		t.Errorf("MainBranch = %q, want from-toml", c.MainBranch)
	}
}

// The two formats are one table with two spellings, so a repository that
// converts must get the configuration it had. Every key is set here, because
// the ones nobody sets are exactly where a wrong table row would hide.
func TestTheTwoFormatsResolveToTheSameConfiguration(t *testing.T) {
	tomlRoot := t.TempDir()
	writeConf(t, tomlRoot, "worktree.toml", `main_branch = "trunk"
worktree_branch_prefix = "fix_wt"
worktree_type_suffix = "_wt"
worktree_default_type = "fix"
worktree_types = ["fix", "feat"]
developer_config_dirs = [".idea"]
developer_config_files = [".env"]
build_init_enabled = true
build_init_command = "make deps"
required_bins = ["git"]
test_command = "make test"
run_tests_before_remove = true
`)
	bashRoot := t.TempDir()
	writeConf(t, bashRoot, "worktree.conf", `MAIN_BRANCH="trunk"
WORKTREE_BRANCH_PREFIX="fix_wt"
WORKTREE_TYPE_SUFFIX="_wt"
WORKTREE_DEFAULT_TYPE="fix"
WORKTREE_TYPES=(fix feat)
DEVELOPER_CONFIG_DIRS=(.idea)
DEVELOPER_CONFIG_FILES=(.env)
BUILD_INIT_ENABLED=true
BUILD_INIT_COMMAND="make deps"
REQUIRED_BINS=(git)
TEST_COMMAND="make test"
RUN_TESTS_BEFORE_REMOVE=true
`)

	fromTOML, err := Load(tomlRoot, "main")
	if err != nil {
		t.Fatalf("worktree.toml: %v", err)
	}
	fromBash, err := Load(bashRoot, "main")
	if err != nil {
		t.Fatalf("worktree.conf: %v", err)
	}
	if !reflect.DeepEqual(fromTOML, fromBash) {
		t.Errorf("the formats disagree:\n toml %+v\n conf %+v", fromTOML, fromBash)
	}
}

func TestLoadMissingConfig(t *testing.T) {
	if _, err := Load(t.TempDir(), "main"); !errors.Is(err, ErrNoConfig) {
		t.Errorf("got %v, want ErrNoConfig", err)
	}
}

// The message for a missing configuration is the one a first-time user hits,
// and for a long time it named the problem without naming any way out.
func TestErrNoConfigNamesTheCommandThatFixesIt(t *testing.T) {
	if !strings.Contains(ErrNoConfig.Error(), "wt init") {
		t.Errorf("ErrNoConfig does not point at wt init: %q", ErrNoConfig)
	}
}
