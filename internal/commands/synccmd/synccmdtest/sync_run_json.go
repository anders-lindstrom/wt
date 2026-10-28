package synccmdtest

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// DecodeSyncRun checks out against the sync-run schema and decodes it.
func DecodeSyncRun(t *testing.T, out []byte) syncrun.RunResult {
	t.Helper()
	testkit.ValidateJSON(t, "sync-run", out)
	var r syncrun.RunResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	return r
}

// SyncRunJSON runs wt sync rebase --json and decodes its checked output.
func SyncRunJSON(t *testing.T, ctx *kit.Context, works []string, opts synccmd.RunOptions) (syncrun.RunResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = syncrun.NewSyncRunJournal(&out, "sync rebase")
	err := synccmd.SyncRebase(ctx, works, opts, &human)
	return DecodeSyncRun(t, out.Bytes()), err
}
