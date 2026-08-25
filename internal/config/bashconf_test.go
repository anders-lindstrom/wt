package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBashScalars(t *testing.T) {
	in := `#!/usr/bin/env bash
# a comment
# REPO_NAME="commented-out"
AWS_SETUP_ENABLED=true
TEST_COMMAND="./gradlew test"
`
	got, err := ParseBash(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseBash: %v", err)
	}
	if _, ok := got["REPO_NAME"]; ok {
		t.Error("commented-out assignment was parsed")
	}
	if got["AWS_SETUP_ENABLED"].Scalar != "true" {
		t.Errorf("bare value: got %q", got["AWS_SETUP_ENABLED"].Scalar)
	}
	if got["TEST_COMMAND"].Scalar != "./gradlew test" {
		t.Errorf("quoted value: got %q", got["TEST_COMMAND"].Scalar)
	}
}

func TestParseBashArrays(t *testing.T) {
	in := `DEVELOPER_CONFIG_DIRS=(.cursor .claude .idea)
DEVELOPER_CONFIG_FILES=(
    "override.properties"
    "common/override.properties"
)
`
	got, err := ParseBash(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseBash: %v", err)
	}
	dirs := got["DEVELOPER_CONFIG_DIRS"]
	if !dirs.IsList || len(dirs.List) != 3 || dirs.List[0] != ".cursor" {
		t.Errorf("single-line array: got %+v", dirs)
	}
	files := got["DEVELOPER_CONFIG_FILES"]
	if !files.IsList || len(files.List) != 2 || files.List[1] != "common/override.properties" {
		t.Errorf("multi-line array: got %+v", files)
	}
}

func TestParseBashEmptyArray(t *testing.T) {
	got, err := ParseBash(strings.NewReader("DEVELOPER_CONFIG_FILES=()\n"))
	if err != nil {
		t.Fatalf("ParseBash: %v", err)
	}
	v := got["DEVELOPER_CONFIG_FILES"]
	if !v.IsList || len(v.List) != 0 {
		t.Errorf("got %+v, want empty list", v)
	}
}

// Conformance: the parser must handle all seven real files, not invented samples.
func TestParseBashRealConfigs(t *testing.T) {
	entries, err := filepath.Glob("testdata/*.conf")
	if err != nil || len(entries) != 7 {
		t.Fatalf("want 7 testdata configs, got %d (%v)", len(entries), err)
	}
	for _, path := range entries {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			got, err := ParseBash(f)
			if err != nil {
				t.Fatalf("ParseBash: %v", err)
			}
			if got["WORKTREE_BRANCH_PREFIX"].Scalar != "feat_wt" {
				t.Errorf("WORKTREE_BRANCH_PREFIX = %q, want feat_wt",
					got["WORKTREE_BRANCH_PREFIX"].Scalar)
			}
			if got["MAIN_BRANCH"].Scalar == "" {
				t.Error("MAIN_BRANCH missing")
			}
		})
	}
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustParseFile(t *testing.T, path string) map[string]Value {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	v, err := ParseBash(f)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
