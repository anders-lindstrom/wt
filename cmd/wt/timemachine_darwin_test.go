//go:build darwin

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anlindstrom/wt/internal/commands/testkit"
	"github.com/anlindstrom/wt/internal/repo"
)

func TestCommandsWarnWhenTimeMachineFails(t *testing.T) {
	// Not parallel: commands change cwd; fake tmutil changes PATH and FindTmutil.
	ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/back", false)
	tm := testkit.FakeTmutil(t, ctx, true)
	outside := t.TempDir()
	for _, tc := range []struct {
		cwd  string
		args []string
	}{
		{ctx.Repo.MainRoot, []string{"new", "fix/another", "--no-setup"}},
		{outside, []string{"restore", dir}},
	} {
		t.Chdir(tc.cwd)
		var out, warned bytes.Buffer
		root := newRootCmd(tc.args...)
		root.SetOut(&out)
		root.SetErr(&warned)
		root.SetArgs(tc.args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if strings.Count(warned.String(), "not excluded from Time Machine") != 1 {
			t.Errorf("%v stderr = %q, want one exclusion warning", tc.args, warned.String())
		}
		if strings.Contains(out.String(), "not excluded from Time Machine") {
			t.Errorf("%v warning leaked to stdout", tc.args)
		}
	}
	if len(tm.Calls(t)) != 4 {
		t.Errorf("tmutil calls = %q, want query and exclusion for each command", tm.Calls(t))
	}
}

func TestSetupFromSubdirectoryUsesWorktreeRoot(t *testing.T) {
	// Not parallel: commands change cwd; fake tmutil changes PATH and FindTmutil.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	tm := testkit.FakeTmutil(t, ctx, false)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(ctx.Repo.Parent, alias); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(alias, filepath.Base(ctx.Repo.MainRoot))
	elsewhere := filepath.Join(t.TempDir(), "external")
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "-b", "fix/external", elsewhere)
	for _, root := range []string{main, elsewhere} {
		sub := filepath.Join(root, "sub")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(sub)
		if out, err := runCmd(t, "setup", "--no-build", "--no-cache"); err != nil {
			t.Fatalf("setup: %v\n%s", err, out)
		}
	}
	if got := tm.Excluded(t); len(got) != 1 || !repo.SamePath(got[0], elsewhere) {
		t.Fatal("setup from a subdirectory did not protect main and exclude the whole external worktree")
	}
}
