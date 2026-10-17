package retire_test

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Without --reap a merged worktree something still runs in is in use: the
// row says what runs there and how to have sweep stop it, and the sweep
// leaves both the worktree and the process.
func TestSweepKeepsABusyWorktreeWithoutReap(t *testing.T) {
	// Not parallel: swaps retire.ScanProcesses and signals real processes.
	retire.RealProcesses(t)
	ctx, _, _ := testkit.SweepRepo(t)
	path := provisiontest.MergedWorktree(t, ctx, "fix/login-crash")
	pid := retire.Leftover(t, path)

	p := retire.PlanOf(t, ctx)
	if len(p.Remove) != 0 || len(p.CheckedOut) != 1 || !slices.Contains(p.CheckedOut[0].KeptCodes, retire.KeptProcesses) {
		t.Fatalf("remove %v kept %+v, want it kept for its process", retire.WorktreeNames(p.Remove), p.CheckedOut)
	}
	want := "1 process, " + strconv.Itoa(pid) + " (sleep 120) running in it; stop them, or wt sweep --reap"
	if out := retire.Rendered(p); !strings.Contains(out, want) {
		t.Errorf("want %q in:\n%s", want, out)
	}

	var buf bytes.Buffer
	if err := retire.Sweep(ctx, retire.SweepOptions{Yes: true, Agents: []wtsync.Agent{}}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if !testkit.Exists(path) || !retire.Alive(pid) {
		t.Errorf("worktree there %v, process alive %v; want both", testkit.Exists(path), retire.Alive(pid))
	}

	plan, err := retiretest.SweepPlanJSON(t, ctx, retire.SweepOptions{Agents: []wtsync.Agent{}})
	if err != nil {
		t.Fatal(err)
	}
	it := planItem(t, plan, "fix_wt/login-crash")
	if it.Category != retire.SweepInUse || len(it.Processes) != 1 || it.Processes[0].PID != pid || it.Processes[0].Reap {
		t.Errorf("item %+v, want inUse with its process left alone", it)
	}
}

// --reap lists the process on the row that goes, stops it right before the
// worktree goes, and reports it on the row of the result.
func TestSweepReapStopsWhatRunsInAMergedWorktree(t *testing.T) {
	// Not parallel: swaps retire.ScanProcesses and signals real processes.
	retire.RealProcesses(t)
	ctx, _, _ := testkit.SweepRepo(t)
	path := provisiontest.MergedWorktree(t, ctx, "fix/login-crash")
	pid := retire.Leftover(t, path)

	opts := retire.SweepOptions{Agents: []wtsync.Agent{}, Reap: true}
	plan, err := retiretest.SweepPlanJSON(t, ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	it := planItem(t, plan, "fix_wt/login-crash")
	if !plan.Reap || it.Category != retire.SweepRemove || len(it.Processes) != 1 || !it.Processes[0].Reap ||
		!strings.Contains(it.Reason, "stops 1 process, "+strconv.Itoa(pid)) {
		t.Fatalf("item %+v, want it removed with its process stopped", it)
	}
	if !retire.Alive(pid) {
		t.Fatal("the plan stopped the process")
	}
	without, _ := retiretest.SweepPlanJSON(t, ctx, retire.SweepOptions{Agents: []wtsync.Agent{}})
	if without.Token != nil || plan.Token == nil {
		t.Errorf("tokens %v and %v: without --reap there is nothing to sweep", without.Token, plan.Token)
	}

	opts.Expect = *plan.Token
	r, human, err := retiretest.SweepJSON(t, ctx, opts)
	if err != nil {
		t.Fatalf("Sweep: %v\n%s", err, human)
	}
	row := retiretest.ResultItem(t, r, "fix_wt/login-crash")
	if row.Result != retire.SweepRemoved || len(row.Reaped) != 1 || row.Reaped[0].PID != pid ||
		row.Reaped[0].Result != wtsync.ReapTerminated {
		t.Errorf("row %+v, want removed with its process terminated", row)
	}
	if testkit.Exists(path) || retire.Alive(pid) {
		t.Errorf("worktree there %v, process alive %v; want neither", testkit.Exists(path), retire.Alive(pid))
	}
	if !strings.Contains(human, "stopped pid "+strconv.Itoa(pid)) {
		t.Errorf("want the stop said:\n%s", human)
	}
}
