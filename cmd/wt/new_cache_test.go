package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// wt new --no-cache --json reaches the cache step: it reports it skipped
// with the flag as the reason, in a repository that declares a cache.
func TestNewNoCacheJSONSkipsTheCacheStep(t *testing.T) {
	parent := t.TempDir()
	main := gittest.NewRepo(t, parent, "demo")
	gittest.WriteFile(t, filepath.Join(main, "bin", "worktree", "worktree.conf"),
		"MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\nWORKTREE_CACHE_DIRS=(node_modules)\n")
	gittest.WriteFile(t, filepath.Join(main, "node_modules", "left-pad", "index.js"), "module.exports = 1")
	gittest.Git(t, main, "add", "-A")
	gittest.Git(t, main, "commit", "-qm", "conf")
	t.Chdir(main)
	args := []string{"new", "fix/login-crash", "--json", "--no-cache", "--no-superset"}
	var stdout, stderr bytes.Buffer
	root := newRootCmd(args...)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	out := stdout.String()
	var r struct {
		Steps []struct {
			Step, Result string
			Reason       *string
		}
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, s := range r.Steps {
		if s.Step == "cache" {
			if s.Result != "skipped" || s.Reason == nil || *s.Reason != "--no-cache" {
				t.Errorf("cache step %+v", s)
			}
			return
		}
	}
	t.Errorf("no cache step:\n%s", out)
}
