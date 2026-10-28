package synckeep

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synckeep/keepstate"
)

// onPlatform pins the platform the keeper's start, stop and status see for
// the length of a test, whatever machine runs it.
func onPlatform(t *testing.T, goos string) {
	t.Helper()
	was := keepstate.KeepGOOS
	keepstate.KeepGOOS = goos
	t.Cleanup(func() { keepstate.KeepGOOS = was })
}

// keepFiles is the keeper's log and state file for the repository.
func keepFiles(t *testing.T, ctx *kit.Context) (log, state string) {
	t.Helper()
	_, log, state, err := keepstate.KeepPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return log, state
}

// readKeep is the keeper's state, failing the test when there is none.
func readKeep(t *testing.T, ctx *kit.Context) keepstate.KeepState {
	t.Helper()
	_, statePath := keepFiles(t, ctx)
	st, ok, err := keepstate.ReadKeepState(statePath)
	if err != nil || !ok {
		t.Fatalf("state file: ok %v err %v", ok, err)
	}
	return st
}
