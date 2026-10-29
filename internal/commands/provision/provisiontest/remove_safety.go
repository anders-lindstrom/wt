package provisiontest

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// SafetyWorktree is a worktree wt made, merged, and nobody's but the test's.
func SafetyWorktree(t *testing.T, work string) (*kit.Context, string) {
	t.Helper()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	path, err := provision.New(ctx, work, provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, path
}

// SubmoduleWorktree is a worktree wt made on a trunk that has a submodule,
// with the submodule initialised in it.
func SubmoduleWorktree(t *testing.T, work string) (*kit.Context, string, string) {
	t.Helper()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	inner := filepath.Join(t.TempDir(), "inner")
	testkit.GitIn(t, filepath.Dir(inner), "init", "-q", "-b", "main", "inner")
	testkit.GitIn(t, inner, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "i")
	testkit.GitIn(t, main, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "sm")
	testkit.GitIn(t, main, "commit", "-qm", "sm")
	ctx, _ := kit.Open(main, io.Discard)
	var buf bytes.Buffer
	path, err := provision.New(ctx, work, provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, path, "-c", "protocol.file.allow=always", "submodule", "update", "-q", "--init")
	return ctx, main, path
}
