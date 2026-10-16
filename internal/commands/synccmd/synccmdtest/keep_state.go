package synccmdtest

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
)

// OnPlatform pins the platform the keeper's start, stop and status see for
// the length of a test, whatever machine runs it.
func OnPlatform(t *testing.T, goos string) {
	t.Helper()
	was := synccmd.KeepGOOS
	synccmd.KeepGOOS = goos
	t.Cleanup(func() { synccmd.KeepGOOS = was })
}

// KeepFiles is the keeper's log and state file for the repository.
func KeepFiles(t *testing.T, ctx *kit.Context) (log, state string) {
	t.Helper()
	_, log, state, err := synccmd.KeepPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return log, state
}

// ReadKeep is the keeper's state, failing the test when there is none.
func ReadKeep(t *testing.T, ctx *kit.Context) synccmd.KeepState {
	t.Helper()
	_, statePath := KeepFiles(t, ctx)
	st, ok, err := synccmd.ReadKeepState(statePath)
	if err != nil || !ok {
		t.Fatalf("state file: ok %v err %v", ok, err)
	}
	return st
}
