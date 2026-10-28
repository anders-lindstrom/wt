package retire

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/session"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// A worktree that turns busy between the plan and its turn in the apply is
// kept for what runs in it, in sweep's words; under --reap the process the
// plan never listed is the one that gets stopped.
func TestSweepKeepsAWorktreeThatTurnedBusyAfterThePlan(t *testing.T) {
	// Not parallel: swaps retire.ScanProcesses and signals real processes.
	RealProcesses(t)
	ctx, _, _ := testkit.SweepRepo(t)
	path := provisiontest.MergedWorktree(t, ctx, "fix/login-crash")

	reap := SweepOptions{Agents: []agents.Agent{}, Reap: true}
	p, reaping := PlanOf(t, ctx), planWith(t, ctx, reap)
	if len(p.Remove) != 1 || len(reaping.Remove) != 1 {
		t.Fatalf("remove %v and %v, want the worktree in both", WorktreeNames(p.Remove), WorktreeNames(reaping.Remove))
	}
	pid := Leftover(t, path)
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
	// Not parallel: swaps retire.ScanProcesses and session.ListSessions.
	ctx, _, _ := testkit.SweepRepo(t)
	path := provisiontest.MergedWorktree(t, ctx, "fix/login-crash")
	was, wasList := ScanProcesses, session.ListSessions
	t.Cleanup(func() { ScanProcesses, session.ListSessions = was, wasList })

	// Without --reap, with the sessions listed fine.
	ScanProcesses = func([]agents.Agent) (*agents.ProcessScan, error) { return nil, errors.New("ps: boom") }
	p := PlanOf(t, ctx)
	if len(p.Remove) != 0 || len(p.CheckedOut) != 1 || !slices.Contains(p.CheckedOut[0].KeptCodes, KeptProcessesUnknown) {
		t.Fatalf("remove %v kept %+v, want it kept with the processes unknown", WorktreeNames(p.Remove), p.CheckedOut)
	}
	if out := Rendered(p); !strings.Contains(out, "sweep again once wt can list them") {
		t.Errorf("want the advice in:\n%s", out)
	}
	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true, Agents: []agents.Agent{}}, &buf); err != nil || !testkit.Exists(path) {
		t.Errorf("Sweep: %v, worktree there %v; want it left alone", err, testkit.Exists(path))
	}

	// With --reap, and the sessions not listed: no --force goes past it.
	session.ListSessions = func() ([]agents.Agent, error) { return nil, errors.New("claude agents --json failed") }
	p = planWith(t, ctx, SweepOptions{Reap: true})
	if len(p.Remove) != 0 || len(p.CheckedOut) != 1 || !slices.Contains(p.CheckedOut[0].KeptCodes, KeptProcessesUnknown) {
		t.Fatalf("remove %v kept %+v, want it kept with the processes unknown", WorktreeNames(p.Remove), p.CheckedOut)
	}
}

// --reap would not take a detached worktree or one whose branch cannot be
// deleted, so a process in one is not the advice's business.
func TestSweepAdvisesWtRemoveWhereReapWouldStillKeepIt(t *testing.T) {
	t.Parallel()
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
