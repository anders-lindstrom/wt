package provisiontest

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// LockedWorktree is a new worktree for work, locked with reason.
func LockedWorktree(t *testing.T, ctx *kit.Context, main, work, reason string) string {
	t.Helper()
	var buf bytes.Buffer
	path, err := provision.New(ctx, work, provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main, "worktree", "lock", "--reason", reason, path)
	return path
}
