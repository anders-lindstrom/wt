package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

func TestConfigShellEmitsLegacyNames(t *testing.T) {
	main := testkit.FixtureRepo(t, testkit.MinimalConf)
	ctx, err := kit.Open(main)
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
	main := testkit.FixtureRepo(t, testkit.MinimalConf)
	p := filepath.Join(main, "bin", "worktree", "provision.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, _ := kit.Open(main)
	var buf bytes.Buffer
	if err := Config(ctx, true, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "AWS_SETUP_ENABLED=true") {
		t.Errorf("want AWS_SETUP_ENABLED=true, got:\n%s", buf.String())
	}
}

func TestConfigShowsCacheDirs(t *testing.T) {
	main := testkit.FixtureRepo(t, testkit.MinimalConf+"WORKTREE_CACHE_DIRS=(node_modules .gradle)\n")
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var text, shell bytes.Buffer
	if err := Config(ctx, false, &text); err != nil {
		t.Fatal(err)
	}
	if err := Config(ctx, true, &shell); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "cache dirs:    node_modules .gradle\n") {
		t.Errorf("wt config does not show the cache dirs:\n%s", text.String())
	}
	if !strings.Contains(shell.String(), "WORKTREE_CACHE_DIRS=('node_modules' '.gradle')\n") {
		t.Errorf("wt config --shell does not show the cache dirs:\n%s", shell.String())
	}
}

func TestConfigShellQuotesSafely(t *testing.T) {
	main := testkit.FixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=true\nBUILD_INIT_COMMAND=\"echo it's fine\"\n")
	ctx, err := kit.Open(main)
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

// A worktree whose branch changes worktree.conf must be read from that
// worktree, matching what the bash implementation did.
func TestOpenReadsConfigFromTheCurrentWorktree(t *testing.T) {
	main := testkit.FixtureRepo(t, testkit.MinimalConf)
	testkit.GitIn(t, main, "config", "user.email", "t@example.com")
	testkit.GitIn(t, main, "config", "user.name", "T")
	testkit.GitIn(t, main, "add", "-A")
	testkit.GitIn(t, main, "commit", "-qm", "init")

	linked := filepath.Join(filepath.Dir(main), "demo_wt", "feat_wt", "changed")
	testkit.GitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/changed", linked)
	if err := os.WriteFile(filepath.Join(linked, "bin", "worktree", "worktree.conf"),
		[]byte("MAIN_BRANCH=\"from-the-worktree\"\nBUILD_INIT_ENABLED=false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := kit.Open(linked)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ctx.Config.MainBranch != "from-the-worktree" {
		t.Errorf("MainBranch = %q, want the worktree's own value", ctx.Config.MainBranch)
	}
}
