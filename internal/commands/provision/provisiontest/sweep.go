package provisiontest

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
)

// MergedWorktree is a worktree wt new made on a branch cut from trunk and
// never committed to, which is merged: what is left behind once a pull
// request lands and nobody ran wt remove.
func MergedWorktree(t *testing.T, ctx *kit.Context, work string) string {
	t.Helper()
	var buf bytes.Buffer
	path, err := provision.New(ctx, work, provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
