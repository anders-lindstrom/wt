package commands

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Every test in this package runs where the machine's own processes are not
// the tests' business: a caller that starts no process finds none, until a
// test reads the real process table.
func init() {
	scanProcesses = func([]wtsync.Agent) (*wtsync.ProcessScan, error) { return &wtsync.ProcessScan{}, nil }
}

// realProcesses has wt read the machine's process table for this test.
func realProcesses(t *testing.T) {
	t.Helper()
	was := scanProcesses
	scanProcesses = wtsync.ScanProcesses
	t.Cleanup(func() { scanProcesses = was })
}

// leftover starts a process in dir the way a dev server outlives the
// session that started it: in a session of its own, with no terminal, its
// parent gone. It is killed when the test ends.
func leftover(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exec sleep 120 >/dev/null 2>&1 & echo $!")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	// Seen by the process table before the test goes on.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if s, err := wtsync.ScanProcesses(nil); err == nil && len(s.In(dir)) > 0 {
			break
		}
	}
	return pid
}

// alive is whether pid is running. A zombie, an orphan not yet collected by
// init, has exited and is not.
func alive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	// Nothing listed is a process collected since signal 0 found it.
	out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	stat := strings.TrimSpace(string(out))
	return stat != "" && !strings.HasPrefix(stat, "Z")
}

// --reap stops a process working in the worktree, then removes it.
func TestRemoveReapStopsAProcessLeftInTheWorktree(t *testing.T) {
	realProcesses(t)
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/served", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(path, "web")
	if err := os.Mkdir(web, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := leftover(t, web)

	buf.Reset()
	if err := Remove(ctx, "fix/served", RemoveOptions{Reap: true}, &buf); err != nil {
		t.Fatalf("Remove: %v\n%s", err, buf.String())
	}
	if alive(pid) {
		t.Errorf("pid %d still running", pid)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree still there")
	}
	out := buf.String()
	if !strings.Contains(out, "will be stopped") || !strings.Contains(out, "stopped pid "+strconv.Itoa(pid)) {
		t.Errorf("want the plan and the stop named:\n%s", out)
	}
}

// --dry-run names the processes --reap would stop, and stops none.
func TestRemoveDryRunListsTheProcessesAndStopsNone(t *testing.T) {
	realProcesses(t)
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/served", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	pid := leftover(t, path)

	buf.Reset()
	if err := Remove(ctx, "fix/served", RemoveOptions{Reap: true, DryRun: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "1 process, "+strconv.Itoa(pid)+" (") {
		t.Errorf("want pid %d named:\n%s", pid, buf.String())
	}
	p, err := removePlanJSON(t, ctx, path, RemoveOptions{Reap: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Processes) != 1 || p.Processes[0].PID != pid || !p.Processes[0].Reap || p.Token == nil {
		t.Errorf("processes %+v token %v, want pid %d to reap and a token", p.Processes, p.Token, pid)
	}
	if !alive(pid) {
		t.Error("a dry run stopped the process")
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("a dry run removed the worktree")
	}
	// Without --reap the plan lists it too, as left running.
	p, _ = removePlanJSON(t, ctx, path, RemoveOptions{})
	if len(p.Processes) != 1 || p.Processes[0].Reap {
		t.Errorf("processes %+v, want one left running", p.Processes)
	}
}

// Without --reap the removal goes ahead, warns about what still runs in
// the worktree, and stops nothing.
func TestRemoveWithoutReapWarnsAndLeavesThemRunning(t *testing.T) {
	realProcesses(t)
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/served", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	pid := leftover(t, path)

	buf.Reset()
	if err := Remove(ctx, "fix/served", RemoveOptions{}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	want := "! 1 process, " + strconv.Itoa(pid) + " (sleep 120) will keep running in it; --reap stops them"
	if !strings.Contains(out, want) {
		t.Errorf("want %q in:\n%s", want, out)
	}
	if !alive(pid) {
		t.Error("removal without --reap stopped the process")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("worktree still there")
	}
}

// When the agent sessions cannot be listed, nothing is known to be safe to
// stop: --reap stops nothing and removes nothing, --force or not.
func TestRemoveReapRefusesWhenTheAgentsAreUnknownEvenForced(t *testing.T) {
	realProcesses(t)
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/served", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	pid := leftover(t, path)

	opts := RemoveOptions{Reap: true, Force: kit.ForceAll, AgentsErr: errors.New("claude agents --json failed")}
	err = Remove(ctx, "fix/served", opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "cannot tell which processes run in it") {
		t.Fatalf("err %v, want the processes unknown", err)
	}
	if !alive(pid) {
		t.Error("the process was stopped")
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("the worktree was removed")
	}
	p, _ := removePlanJSON(t, ctx, path, opts)
	if codes := problemCodes(p.Problems); !strings.Contains(strings.Join(codes, ","), KeptProcessesUnknown) {
		t.Errorf("problems %v, want %s", codes, KeptProcessesUnknown)
	}
}

// When the sessions list fine for the plan and then not right before the
// checkout goes, nothing is known to be safe to stop: --reap stops nothing
// and removes nothing, --force or not.
func TestRemoveReapStopsNothingWhenTheRelistingFails(t *testing.T) {
	realProcesses(t)
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/served", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	pid := leftover(t, path)

	opts := RemoveOptions{Reap: true, Force: kit.ForceAll, Agents: []wtsync.Agent{},
		Relist: func() ([]wtsync.Agent, error) { return nil, errors.New("claude agents --json failed") }}
	err = Remove(ctx, "fix/served", opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "cannot tell which processes run in it") {
		t.Fatalf("err %v, want the processes unknown:\n%s", err, buf.String())
	}
	if !alive(pid) {
		t.Error("the process was stopped")
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("the worktree was removed")
	}
}

// --yes --json reports each process it stopped; one that will not stop
// refuses the removal, with nothing removed.
func TestRemoveJSONReportsReapedAndRefusesOnOneThatWillNotStop(t *testing.T) {
	realProcesses(t)
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/served", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	pid := leftover(t, path)

	was := reapProcesses
	t.Cleanup(func() { reapProcesses = was })
	reapProcesses = func(procs []wtsync.Process, _ time.Duration) []wtsync.Reaped {
		var out []wtsync.Reaped
		for _, p := range procs {
			out = append(out, wtsync.Reaped{Process: p, Result: wtsync.ReapSurvived})
		}
		return out
	}
	r, err := removeJSON(t, ctx, path, RemoveOptions{Reap: true})
	if err == nil || !strings.Contains(err.Error(), "would not stop") {
		t.Fatalf("err %v, want the process that would not stop", err)
	}
	if r.Outcome != RemoveRefused || len(r.Reaped) != 1 || r.Reaped[0].PID != pid || r.Reaped[0].Result != wtsync.ReapSurvived {
		t.Errorf("outcome %s reaped %+v", r.Outcome, r.Reaped)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("the worktree was removed")
	}

	reapProcesses = was
	r, err = removeJSON(t, ctx, path, RemoveOptions{Reap: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != RemoveRemoved || len(r.Reaped) != 1 || r.Reaped[0].Result != wtsync.ReapTerminated {
		t.Errorf("outcome %s reaped %+v, want removed and terminated", r.Outcome, r.Reaped)
	}
}
