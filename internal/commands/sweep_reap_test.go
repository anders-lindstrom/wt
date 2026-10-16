package commands

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Without --reap a merged worktree something still runs in is in use: the
// row says what runs there and how to have sweep stop it, and the sweep
// leaves both the worktree and the process.
func TestSweepKeepsABusyWorktreeWithoutReap(t *testing.T) {
	realProcesses(t)
	ctx, _, _ := testkit.SweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	pid := leftover(t, path)

	p := planOf(t, ctx)
	if len(p.Remove) != 0 || len(p.CheckedOut) != 1 || !slices.Contains(p.CheckedOut[0].KeptCodes, KeptProcesses) {
		t.Fatalf("remove %v kept %+v, want it kept for its process", worktreeNames(p.Remove), p.CheckedOut)
	}
	want := "1 process, " + strconv.Itoa(pid) + " (sleep 120) running in it; stop them, or wt sweep --reap"
	if out := rendered(p); !strings.Contains(out, want) {
		t.Errorf("want %q in:\n%s", want, out)
	}

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true, Agents: []wtsync.Agent{}}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if !testkit.Exists(path) || !alive(pid) {
		t.Errorf("worktree there %v, process alive %v; want both", testkit.Exists(path), alive(pid))
	}

	plan, err := sweepPlanJSON(t, ctx, SweepOptions{Agents: []wtsync.Agent{}})
	if err != nil {
		t.Fatal(err)
	}
	it := planItem(t, plan, "fix_wt/login-crash")
	if it.Category != SweepInUse || len(it.Processes) != 1 || it.Processes[0].PID != pid || it.Processes[0].Reap {
		t.Errorf("item %+v, want inUse with its process left alone", it)
	}
}

// --reap lists the process on the row that goes, stops it right before the
// worktree goes, and reports it on the row of the result.
func TestSweepReapStopsWhatRunsInAMergedWorktree(t *testing.T) {
	realProcesses(t)
	ctx, _, _ := testkit.SweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	pid := leftover(t, path)

	opts := SweepOptions{Agents: []wtsync.Agent{}, Reap: true}
	plan, err := sweepPlanJSON(t, ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	it := planItem(t, plan, "fix_wt/login-crash")
	if !plan.Reap || it.Category != SweepRemove || len(it.Processes) != 1 || !it.Processes[0].Reap ||
		!strings.Contains(it.Reason, "stops 1 process, "+strconv.Itoa(pid)) {
		t.Fatalf("item %+v, want it removed with its process stopped", it)
	}
	if !alive(pid) {
		t.Fatal("the plan stopped the process")
	}
	without, _ := sweepPlanJSON(t, ctx, SweepOptions{Agents: []wtsync.Agent{}})
	if without.Token != nil || plan.Token == nil {
		t.Errorf("tokens %v and %v: without --reap there is nothing to sweep", without.Token, plan.Token)
	}

	opts.Expect = *plan.Token
	r, human, err := sweepJSON(t, ctx, opts)
	if err != nil {
		t.Fatalf("Sweep: %v\n%s", err, human)
	}
	row := resultItem(t, r, "fix_wt/login-crash")
	if row.Result != SweepRemoved || len(row.Reaped) != 1 || row.Reaped[0].PID != pid ||
		row.Reaped[0].Result != wtsync.ReapTerminated {
		t.Errorf("row %+v, want removed with its process terminated", row)
	}
	if testkit.Exists(path) || alive(pid) {
		t.Errorf("worktree there %v, process alive %v; want neither", testkit.Exists(path), alive(pid))
	}
	if !strings.Contains(human, "stopped pid "+strconv.Itoa(pid)) {
		t.Errorf("want the stop said:\n%s", human)
	}
}

// A worktree that turns busy between the plan and its turn in the apply is
// kept for what runs in it, in sweep's words; under --reap the process the
// plan never listed is the one that gets stopped.
func TestSweepKeepsAWorktreeThatTurnedBusyAfterThePlan(t *testing.T) {
	realProcesses(t)
	ctx, _, _ := testkit.SweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")

	reap := SweepOptions{Agents: []wtsync.Agent{}, Reap: true}
	p, reaping := planOf(t, ctx), planWith(t, ctx, reap)
	if len(p.Remove) != 1 || len(reaping.Remove) != 1 {
		t.Fatalf("remove %v and %v, want the worktree in both", worktreeNames(p.Remove), worktreeNames(reaping.Remove))
	}
	pid := leftover(t, path)
	_, why := changedDuringSweep(ctx, p.Remove[0].Plan, nobodyIn)
	if want := "1 process, " + strconv.Itoa(pid) + " (sleep 120) running in it"; why != want {
		t.Errorf("kept because %q, want %q", why, want)
	}

	now, why := changedDuringSweep(ctx, reaping.Remove[0].Plan, reap)
	if why != "" || len(now.Processes) != 1 || now.Processes[0].PID != pid {
		t.Errorf("kept because %q, processes %+v; want it to go with %d stopped", why, now.Processes, pid)
	}
}

// A process table that cannot be read keeps the worktree, with --reap or
// without, and says what to do.
func TestSweepKeepsAWorktreeWhenTheProcessesAreUnknown(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	was, wasList := scanProcesses, listSessions
	t.Cleanup(func() { scanProcesses, listSessions = was, wasList })

	// Without --reap, with the sessions listed fine.
	scanProcesses = func([]wtsync.Agent) (*wtsync.ProcessScan, error) { return nil, errors.New("ps: boom") }
	p := planOf(t, ctx)
	if len(p.Remove) != 0 || len(p.CheckedOut) != 1 || !slices.Contains(p.CheckedOut[0].KeptCodes, KeptProcessesUnknown) {
		t.Fatalf("remove %v kept %+v, want it kept with the processes unknown", worktreeNames(p.Remove), p.CheckedOut)
	}
	if out := rendered(p); !strings.Contains(out, "sweep again once wt can list them") {
		t.Errorf("want the advice in:\n%s", out)
	}
	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true, Agents: []wtsync.Agent{}}, &buf); err != nil || !testkit.Exists(path) {
		t.Errorf("Sweep: %v, worktree there %v; want it left alone", err, testkit.Exists(path))
	}

	// With --reap, and the sessions not listed: no --force goes past it.
	listSessions = func() ([]wtsync.Agent, error) { return nil, errors.New("claude agents --json failed") }
	p = planWith(t, ctx, SweepOptions{Reap: true})
	if len(p.Remove) != 0 || len(p.CheckedOut) != 1 || !slices.Contains(p.CheckedOut[0].KeptCodes, KeptProcessesUnknown) {
		t.Fatalf("remove %v kept %+v, want it kept with the processes unknown", worktreeNames(p.Remove), p.CheckedOut)
	}
}

// --reap would not take a detached worktree or one whose branch cannot be
// deleted, so a process in one is not the advice's business.
func TestSweepAdvisesWtRemoveWhereReapWouldStillKeepIt(t *testing.T) {
	for _, b := range []SweepBranch{
		{Worktree: "/w/a", Work: "a", Detached: true, Kept: "k", KeptCodes: []string{KeptDetached, KeptProcesses}},
		{Worktree: "/w/a", Work: "a", Kept: "k", KeptCodes: []string{KeptBranchNotDeletable, KeptProcessesUnknown}},
	} {
		got := SweepPlan{}.keptBecause(b)
		if want := "k; wt remove "; !strings.HasPrefix(got, want) || strings.Contains(got, "--reap") {
			t.Errorf("advice %q for %v", got, b.KeptCodes)
		}
	}
	b := SweepBranch{Worktree: "/w/a", Work: "a", Kept: "k", KeptCodes: []string{KeptProcesses}}
	if got := (SweepPlan{}).keptBecause(b); got != "k; stop them, or wt sweep --reap" {
		t.Errorf("advice %q", got)
	}
}
