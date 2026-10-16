package provisiontest

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
)

// SafetyWorktreeIn is a wt worktree for work in ctx's repository.
func SafetyWorktreeIn(t *testing.T, ctx *kit.Context, work string) (*kit.Context, string) {
	t.Helper()
	var buf bytes.Buffer
	path, err := provision.New(ctx, work, provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return ctx, path
}
