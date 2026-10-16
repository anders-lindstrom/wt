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
	var res quarantine.RestoreResult
	var buf bytes.Buffer
	if err := refs.Restore(ctx, dir, refs.RestoreOptions{Result: &res}, &buf); err != nil {
		t.Fatalf("Restore: %v\n%s", err, buf.String())
	}
	return res
}
