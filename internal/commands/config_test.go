package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureRepo(t *testing.T, conf string) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(parent, "demo")
	cmd := exec.Command("git", "init", "-q", "-b", "main", "demo")
	cmd.Dir = parent
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	dir := filepath.Join(main, "bin", "worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worktree.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return main
}

const minimalConf = "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\n"

func TestConfigShellEmitsLegacyNames(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	ctx, err := Open(main)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var buf bytes.Buffer
	if err := Config(ctx, true, &buf); err != nil {
		t.Fatalf("Config: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"REPO_NAME='demo'",
		"MAIN_BRANCH='main'",
		"WORKTREE_BRANCH_PREFIX='feat_wt'",
		"WORKTREE_DEFAULT_TYPE='feat'",
		"AWS_SETUP_ENABLED=false",
		"DEVELOPER_CONFIG_DIRS=(",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// provision.sh is what AWS_SETUP_ENABLED became; the compat output has to
// reflect it so Herdr keeps seeing the flag it documents.
func TestConfigShellReportsProvisionScript(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	p := filepath.Join(main, "bin", "worktree", "provision.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Config(ctx, true, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "AWS_SETUP_ENABLED=true") {
		t.Errorf("want AWS_SETUP_ENABLED=true, got:\n%s", buf.String())
	}
}

func TestConfigShellQuotesSafely(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=true\nBUILD_INIT_COMMAND=\"echo it's fine\"\n")
	ctx, err := Open(main)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var buf bytes.Buffer
	if err := Config(ctx, true, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `BUILD_INIT_COMMAND='echo it'\''s fine'`) {
		t.Errorf("apostrophe not escaped:\n%s", buf.String())
	}
}
