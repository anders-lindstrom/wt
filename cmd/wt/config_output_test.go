package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

func TestConfigOutputFixtures(t *testing.T) {
	// Not parallel: config resolution reads the environment and working directory.
	fixtureDir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	userPath := testkit.WriteUserConfig(t, "branch_suffix = \"_mine\"\ntype_names = [\"feat=feature\", \"docs=doc\"]\n")
	t.Setenv("WT_ROOTS", "/fixture/repos")
	main := testkit.FixtureRepo(t, `MAIN_BRANCH="main"
WORKTREE_BRANCH_PREFIX="team/"
WORKTREE_TYPE_SUFFIX="_wt"
WORKTREE_DEFAULT_TYPE="feat"
WORKTREE_TYPES="feat fix docs"
DEVELOPER_CONFIG_DIRS=(".settings" "config/local")
DEVELOPER_CONFIG_FILES=(".env" "config/dev's.env")
WORKTREE_CACHE_DIRS=("node_modules" ".cache/build")
REQUIRED_BINS="go git"
BUILD_INIT_ENABLED=true
BUILD_INIT_COMMAND="go build ./..."
TEST_COMMAND="go test ./..."
RUN_TESTS_BEFORE_REMOVE=true
`)
	gittest.WriteFile(t, filepath.Join(main, "bin", "worktree", "provision.sh"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(main, "bin", "worktree", "provision.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(main)
	for _, name := range []string{"human", "shell"} {
		t.Run(name, func(t *testing.T) {
			args := []string{"config"}
			if name == "shell" {
				args = append(args, "--shell")
			}
			got, err := runCmd(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := os.ReadFile(filepath.Join(fixtureDir, "config-"+name+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			want := strings.NewReplacer("{{MAIN_ROOT}}", main, "{{USER_CONFIG}}", userPath).Replace(string(fixture))
			if got != want {
				t.Errorf("config output differs from fixture:\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
