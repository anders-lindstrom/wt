package wtsync

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// scanWorld is a process table and the directories of its processes, put
// in place of ps and lsof.
func scanWorld(t *testing.T, procs []Process, cwds map[int]string) {
	t.Helper()
	oldList, oldCwds := listProcesses, processCwds
	t.Cleanup(func() { listProcesses, processCwds = oldList, oldCwds })
	listProcesses = func() ([]Process, error) { return procs, nil }
	processCwds = func(pids []int) (map[int]string, map[int]bool, error) {
		out := map[int]string{}
		for _, pid := range pids {
			if dir, ok := cwds[pid]; ok {
				out[pid] = dir
			}
			if pid == os.Getpid() {
				out[pid] = "/"
			}
		}
		return out, map[int]bool{}, nil
	}
}

func row(pid, ppid, uid int, tty, command string) Process {
	return Process{PID: pid, PPID: ppid, UID: uid, TTY: tty, Argv: strings.Fields(command)}
}

// Only a process nobody spares is found in the worktree: not one with a
// terminal, another user's, an agent's, or wt's own child, and not one
// working somewhere else.
func TestScanProcessesSparesTerminalsAgentsOthersAndWt(t *testing.T) {
	// Not parallel: swaps the package-level process listers.
	wt := resolved(t.TempDir())
	sub := filepath.Join(wt, "web")
	elsewhere := resolved(t.TempDir())
	uid := os.Getuid()
	procs := []Process{
		row(200, 1, uid, "??", "node vite"),
		row(201, 1, uid, "ttys004", "-zsh"),
		row(202, 1, uid+1, "?", "postgres"),
		row(203, 1, uid, "??", "node /opt/claude/cli.js"),
		row(204, 1, uid, "??", "/usr/local/bin/claude --bg"),
		row(205, 1, uid, "??", "codex exec fix it"),
		row(206, os.Getpid(), uid, "??", "git status"),
		row(207, 1, uid, "?", "esbuild --service"),
		row(208, 1, uid, "??", "npm run dev"),
	}
	cwds := map[int]string{200: wt, 201: wt, 202: wt, 203: wt, 204: wt, 205: wt, 206: wt, 207: sub, 208: elsewhere}
	scanWorld(t, procs, cwds)

	s, err := ScanProcesses([]Agent{{ID: "c1", Cwd: wt, PID: 203}})
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, p := range s.In(wt) {
		got = append(got, p.PID)
	}
	if len(got) != 2 || got[0] != 200 || got[1] != 207 {
		t.Fatalf("found %v in the worktree, want 200 and 207", got)
	}
	if in := s.In(elsewhere); len(in) != 1 || in[0].PID != 208 {
		t.Errorf("found %+v elsewhere, want 208", in)
	}
}

// A process whose terminal was never filled in is not known to have none,
// so it is spared.
func TestScanProcessesSparesAProcessWithoutATTYField(t *testing.T) {
	// Not parallel: swaps the package-level process listers.
	wt := resolved(t.TempDir())
	scanWorld(t, []Process{{PID: 200, PPID: 1, UID: os.Getuid(), Argv: []string{"node", "vite"}}}, map[int]string{200: wt})
	s, err := ScanProcesses(nil)
	if err != nil {
		t.Fatal(err)
	}
	if in := s.In(wt); len(in) != 0 {
		t.Fatalf("found %+v, want none", in)
	}
}

func TestScanProcessesFailsWithoutAProcessTable(t *testing.T) {
	// Not parallel: swaps the package-level process listers.
	old := listProcesses
	t.Cleanup(func() { listProcesses = old })
	listProcesses = func() ([]Process, error) { return nil, errors.New("ps: boom") }
	if _, err := ScanProcesses(nil); err == nil {
		t.Fatal("no error from a process table that cannot be read")
	}
}

// Directories nobody could read are a scan that failed, not an idle machine.
func TestScanProcessesFailsWhenNoDirectoryCanBeRead(t *testing.T) {
	// Not parallel: swaps the package-level process listers.
	uid := os.Getuid()
	scanWorld(t, []Process{row(200, 1, uid, "??", "node vite")}, nil)
	processCwds = func(_ []int) (map[int]string, map[int]bool, error) {
		return map[int]string{}, map[int]bool{200: true}, nil
	}
	if _, err := ScanProcesses(nil); err == nil {
		t.Fatal("a scan that could not read even wt's own directory reported success")
	}
}

// A process of ours whose directory may not be read, as Linux keeps a
// non-dumpable one's, is passed over: not placed, and not an error.
func TestScanProcessesPassesOverAProcessItMayNotRead(t *testing.T) {
	// Not parallel: swaps the package-level process listers.
	uid := os.Getuid()
	wt := resolved(t.TempDir())
	scanWorld(t, []Process{row(200, 1, uid, "?", "ssh-agent"), row(201, 1, uid, "?", "node vite")},
		map[int]string{201: wt})
	s, err := ScanProcesses(nil)
	if err != nil {
		t.Fatal(err)
	}
	if in := s.In(wt); len(in) != 1 || in[0].PID != 201 {
		t.Errorf("found %+v, want 201 alone", in)
	}
	scanWorld(t, []Process{row(200, 1, uid, "?", "ssh-agent")}, nil)
	if _, err := ScanProcesses(nil); err != nil {
		t.Errorf("a scan whose only candidate may not be read failed: %v", err)
	}
}

// daemon starts command in dir the way a dev server left behind is running:
// in a session of its own, with no terminal, its parent gone. It returns
// the pid, and kills it when the test ends.
func daemon(t *testing.T, dir, command string) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", command+" >/dev/null 2>&1 & echo $!")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	pid := 0
	for _, c := range strings.TrimSpace(string(out)) {
		pid = pid*10 + int(c-'0')
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

// The real ps and lsof find a process left running in a worktree, and Reap
// ends it with SIGTERM.
func TestScanAndReapALeftoverProcess(t *testing.T) {
	// Not parallel: timed against a real deadline and the machine's process table.
	wt := resolved(t.TempDir())
	pid := daemon(t, wt, "exec sleep 60")
	var found []Process
	for deadline := time.Now().Add(5 * time.Second); len(found) == 0 && time.Now().Before(deadline); {
		s, err := ScanProcesses(nil)
		if err != nil {
			t.Fatal(err)
		}
		found = s.In(wt)
	}
	if len(found) != 1 || found[0].PID != pid || found[0].Argv[0] != "sleep" {
		t.Fatalf("found %+v in the worktree, want sleep %d", found, pid)
	}
	reaped := Reap(found, 5*time.Second)
	if len(reaped) != 1 || reaped[0].Result != ReapTerminated {
		t.Fatalf("reaped %+v, want terminated", reaped)
	}
	if !strings.Contains(ReapedLine(reaped[0]), "SIGTERM") {
		t.Errorf("line %q", ReapedLine(reaped[0]))
	}
	// A reaped orphan may be a zombie until init collects it: not running.
	// running keeps a pid ps no longer lists, so one collected between its
	// signal 0 and its ps reads as running once; it is asked again.
	still := running([]int{pid})[pid]
	for deadline := time.Now().Add(time.Second); still && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		still = running([]int{pid})[pid]
	}
	if still {
		t.Errorf("pid %d still running", pid)
	}
}

// A process that ignores SIGTERM gets SIGKILL once grace is up; one that
// has exited already is gone.
func TestReapKillsWhatOutlivesSigterm(t *testing.T) {
	// Not parallel: timed against a real deadline and the machine's process table.
	wt := t.TempDir()
	ready := filepath.Join(wt, "ready")
	stubborn := daemon(t, wt, `exec /bin/sh -c 'trap "" TERM; : > ready; while :; do sleep 0.1; done'`)
	// SIGTERM is only sent once the shell has set its trap.
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatal("the process never set its trap")
	}
	gone := Process{PID: 2147483646, Argv: []string{"nothing"}}
	reaped := Reap([]Process{{PID: stubborn, Argv: []string{"sh"}}, gone}, 300*time.Millisecond)
	if reaped[0].Result != ReapKilled || reaped[1].Result != ReapGone {
		t.Fatalf("reaped %+v, want killed and gone", reaped)
	}
}

// A child that has exited and not been waited for is a zombie: signal 0
// still finds it, and it counts as exited all the same (terminated, since
// SIGTERM reached it).
func TestReapCountsAZombieAsGone(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	reaped := Reap([]Process{{PID: cmd.Process.Pid, Argv: []string{"sleep", "60"}}}, 5*time.Second)
	if reaped[0].Result != ReapTerminated {
		t.Fatalf("reaped %+v, want terminated", reaped)
	}
}

// A ps that fails, printing nothing, says nothing about the processes it was
// asked of: a live one is still running, not gone.
func TestRunningKeepsAProcessWhenPsFails(t *testing.T) {
	// Not parallel: sets PATH for the whole test binary.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	bin := t.TempDir()
	gittest.WriteExecutable(t, filepath.Join(bin, "ps"), "#!/bin/sh\nexit 2\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if alive := running([]int{cmd.Process.Pid}); !alive[cmd.Process.Pid] {
		t.Fatal("a process signal 0 finds was reported gone because ps failed")
	}
}

func TestProcessShortCutsTheCommand(t *testing.T) {
	t.Parallel()
	p := Process{Argv: []string{"/usr/local/bin/node", "/Users/someone/app/node_modules/.bin/vite", "--port", "5173"}}
	if got := p.Short(20); got != "node /Users/someone…" {
		t.Errorf("short %q", got)
	}
	if got := p.Short(200); got != "node /Users/someone/app/node_modules/.bin/vite --port 5173" {
		t.Errorf("short %q", got)
	}
}
