package config

import (
	"errors"
	"os"
	"path/filepath"
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
