package retire_test

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// --yes --json reports each process it stopped; one that will not stop
// refuses the removal, with nothing removed.
func TestRemoveJSONReportsReapedAndRefusesOnOneThatWillNotStop(t *testing.T) {
	// Not parallel: swaps retire.ReapProcesses and retire.ScanProcesses and signals real processes.
	retire.RealProcesses(t)
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	path, err := provision.New(ctx, "fix/served", provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	pid := retire.Leftover(t, path)

	was := retire.ReapProcesses
	t.Cleanup(func() { retire.ReapProcesses = was })
	retire.ReapProcesses = func(procs []wtsync.Process, _ time.Duration) []wtsync.Reaped {
		var out []wtsync.Reaped
		for _, p := range procs {
			out = append(out, wtsync.Reaped{Process: p, Result: wtsync.ReapSurvived})
		}
		return out
	}
	r, err := retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{Reap: true})
	if err == nil || !strings.Contains(err.Error(), "would not stop") {
		t.Fatalf("err %v, want the process that would not stop", err)
	}
	if r.Outcome != retire.RemoveRefused || len(r.Reaped) != 1 || r.Reaped[0].PID != pid || r.Reaped[0].Result != wtsync.ReapSurvived {
		t.Errorf("outcome %s reaped %+v", r.Outcome, r.Reaped)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("the worktree was removed")
	}

	retire.ReapProcesses = was
	r, err = retiretest.RemoveJSON(t, ctx, path, retire.RemoveOptions{Reap: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != retire.RemoveRemoved || len(r.Reaped) != 1 || r.Reaped[0].Result != wtsync.ReapTerminated {
		t.Errorf("outcome %s reaped %+v, want removed and terminated", r.Outcome, r.Reaped)
	}
}
