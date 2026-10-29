package provisiontest

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// SyncRepo builds a repository whose origin is itself, so origin/main
// exists, with a declaration on main and two feature worktrees: one that
// conflicts on the declared owned line, and one cut after trunk's last
// commit, so it is current.
func SyncRepo(t *testing.T) *kit.Context {
	t.Helper()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(main, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".wt-sync.yaml", "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n")
	write("v.txt", "1.0.0\n")
	// a.txt is claimed by nothing and touched by nobody here; it is in the
	// base so a test can make both sides edit it and get a real
	// modify/modify conflict rather than an add/add.
	write("a.txt", "base\n")
	testkit.GitIn(t, main, "add", "-A")
	testkit.GitIn(t, main, "commit", "-q", "-m", "declare")
	ctx, err := kit.Open(main, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	bump, err := provision.New(ctx, "feat/bump", provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bump, "v.txt"), []byte("1.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, bump, "commit", "-q", "-am", "bump")
	write("v.txt", "1.0.5\n")
	testkit.GitIn(t, main, "commit", "-q", "-am", "trunk bump")
	if _, err := provision.New(ctx, "feat/other", provision.NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main, "remote", "add", "origin", main)
	testkit.GitIn(t, main, "fetch", "-q", "origin")
	return ctx
}
