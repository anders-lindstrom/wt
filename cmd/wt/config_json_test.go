package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anlindstrom/wt/internal/commands/testkit"
	"github.com/anlindstrom/wt/internal/gittest"
	"github.com/anlindstrom/wt/schema"
)

func configJSON(t *testing.T) map[string]any {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := newRootCmd("config", "--json")
	root.SetArgs([]string{"config", "--json"})
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("config --json: %v\n%s", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	testkit.ValidateJSON(t, "config", stdout.Bytes())
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestConfigJSONResolvedValues(t *testing.T) {
	testkit.WriteUserConfig(t, "branch_suffix = \"_mine\"\ntype_names = [\"feat=feature\", \"docs=doc\"]\n")
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
`)
	script := filepath.Join(main, "bin", "worktree", "provision.sh")
	gittest.WriteFile(t, script, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(script, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(main)
	got := configJSON(t)
	want := map[string]any{
		"schema": float64(1), "schemaVersion": "1.0.0", "command": "config",
		"repo": "demo", "mainRoot": main, "configured": true, "trunk": "main", "trunkSource": "config",
		"branchPrefix": "team/", "typeSuffix": "_wt", "branchSuffix": "_mine", "defaultType": "feat",
		"types": []any{"feat", "fix", "docs"}, "typeNames": map[string]any{"feat": "feature", "docs": "doc"},
		"developerConfigDirs": []any{".settings", "config/local"}, "developerConfigFiles": []any{".env", "config/dev's.env"},
		"cacheDirs": []any{"node_modules", ".cache/build"}, "requiredBins": []any{"go", "git"},
		"buildInitEnabled": true, "buildInitCommand": "go build ./...", "provisionScript": false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolved config = %#v, want %#v", got, want)
	}
	human, err := runCmd(t, "config")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{`branch suffix: "_mine" (user file); worktree paths keep "_wt"`, "type names:    feat=feature docs=doc (user file); worktree paths keep the type"} {
		if !strings.Contains(human, line) {
			t.Errorf("human output missing %q: %s", line, human)
		}
	}
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := configJSON(t); got["provisionScript"] != true {
		t.Errorf("executable provision.sh: %v", got["provisionScript"])
	}
}

func TestConfigJSONDetectedDefaults(t *testing.T) {
	testkit.UserConfigIn(t)
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	if err := os.Remove(filepath.Join(main, "bin", "worktree", "worktree.conf")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(main)
	got := configJSON(t)
	for key, want := range map[string]any{
		"mainRoot": main, "configured": false, "trunk": "main", "trunkSource": "conventional",
		"buildInitCommand": nil, "typeNames": map[string]any{},
		"developerConfigFiles": []any{}, "cacheDirs": []any{}, "requiredBins": []any{},
		"provisionScript": false,
	} {
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}
}

func TestConfigJSONEmptyValues(t *testing.T) {
	testkit.UserConfigIn(t)
	main := testkit.FixtureRepo(t, testkit.MinimalConf+`WORKTREE_BRANCH_PREFIX=""
WORKTREE_TYPE_SUFFIX=""
WORKTREE_BRANCH_SUFFIX=""
DEVELOPER_CONFIG_DIRS=()
`)
	t.Chdir(main)
	got := configJSON(t)
	for _, key := range []string{"branchSuffix", "buildInitCommand"} {
		if got[key] != nil {
			t.Errorf("%s = %#v, want null", key, got[key])
		}
	}
	if got["branchPrefix"] != "feat_wt" || got["typeSuffix"] != "_wt" {
		t.Errorf("empty prefix and type suffix should resolve to defaults: %#v", got)
	}
	if !reflect.DeepEqual(got["developerConfigDirs"], []any{}) {
		t.Errorf("developerConfigDirs = %#v, want []", got["developerConfigDirs"])
	}
}

func TestConfigJSONShellIsUsageError(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCmd(t, "config", "--json", "--shell")
	if err == nil || errorStatus(err) != 2 || !strings.Contains(err.Error(), "--shell") {
		t.Fatalf("error = %v, want --shell usage error (2)", err)
	}
}

func TestConfigJSONSchemaCommand(t *testing.T) {
	out, err := runCmd(t, "schema", "config")
	if err != nil {
		t.Fatal(err)
	}
	doc, ok := schema.Get("config")
	if !ok || out != string(doc.Bytes) {
		t.Fatal("schema config did not print the embedded schema")
	}
	listed, err := runCmd(t, "schema")
	if err != nil || !strings.Contains(listed, "wt config --json") {
		t.Fatalf("schema list: %v\n%s", err, listed)
	}
}
