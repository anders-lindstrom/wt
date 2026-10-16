package refstest

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/refs"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// Restore runs wt restore on the quarantine dir and fails the test when it
// fails.
func Restore(t *testing.T, ctx *kit.Context, dir string) quarantine.RestoreResult {
	t.Helper()
	return RestoreWith(t, ctx, dir, nil)
}

// RestoreWith is Restore with hooks the restore runs.
func RestoreWith(t *testing.T, ctx *kit.Context, dir string, h *quarantine.Hooks) quarantine.RestoreResult {
	t.Helper()
	var res quarantine.RestoreResult
	var buf bytes.Buffer
	if err := refs.Restore(ctx, dir, refs.RestoreOptions{Result: &res, QuarantineHooks: h}, &buf); err != nil {
		t.Fatalf("Restore: %v\n%s", err, buf.String())
	}
	return res
}
