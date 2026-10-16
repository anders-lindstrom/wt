package purgetest

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/purge"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// Purge runs wt purge on the quarantine dir and fails the test when it fails.
func Purge(t *testing.T, dir string) quarantine.PurgeResult {
	t.Helper()
	var res quarantine.PurgeResult
	var buf bytes.Buffer
	if err := purge.QuarantinePurge(dir, purge.Options{Result: &res}, &buf); err != nil {
		t.Fatalf("QuarantinePurge: %v\n%s", err, buf.String())
	}
	return res
}
