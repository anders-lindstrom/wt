package agents

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// ProcessScan is the processes wt may end, each with its working directory:
// the process table read once, then asked about any number of worktrees.
// Spared, and never in it: another user's processes, a process with a
// controlling terminal, wt itself with its children and its ancestors, and
// the agents' processes: for Claude the session's main process, for Codex
// every process of the session, and anything that looks like either. A dev
// server a Claude session started is not its main process and is found.
// The zero value holds no processes.
type ProcessScan struct {
	procs []Process
	cwds  map[int]string
}

// ScanProcesses reads the process table and the working directory of every
// process it does not spare. agents are the sessions wt's discovery found;
// the pids each names are spared (a Claude session's main process, all of a
// Codex session's), and so is anything that discovery would take for an
// agent, so a session the listing left out is still not ended.
func ScanProcesses(agents []Agent) (*ProcessScan, error) {
	procs, err := listProcesses()
	if err != nil {
		return nil, err
	}
	spared := sparedPIDs(procs, agents)
	var pids []int
	byPID := map[int]Process{}
	for _, p := range procs {
		if !spared[p.PID] {
			pids = append(pids, p.PID)
			byPID[p.PID] = p
		}
	}
	// A process whose directory may not be read is not placed anywhere:
	// what cannot be found in a worktree is not ended there. Linux keeps the
	// directory of a non-dumpable process (ssh-agent, gpg-agent) even from
	// its own user, so such a process is passed over, not an error. wt's own
	// directory can always be read: asked for alongside, it tells a reader
	// that failed, an lsof that printed nothing, from processes that may not
	// be read, so a failure never passes for an idle machine.
	self := os.Getpid()
	cwds, _, err := processCwds(append(pids, self))
	if err != nil {
		return nil, err
	}
	if _, ok := cwds[self]; !ok {
		return nil, errors.New("cannot read the working directories of processes: not even wt's own")
	}
	s := &ProcessScan{cwds: map[int]string{}}
	for _, pid := range pids {
		if dir, ok := cwds[pid]; ok {
			s.procs = append(s.procs, byPID[pid])
			s.cwds[pid] = dir
		}
	}
	return s, nil
}

// sparedPIDs is every process in procs the reaper leaves alone.
func sparedPIDs(procs []Process, agents []Agent) map[int]bool {
	spared := ancestorsOf(procs)
	self, uid := os.Getpid(), os.Getuid()
	spared[self] = true
	for _, a := range agents {
		spared[a.PID] = true
		for _, pid := range a.pids {
			spared[pid] = true
		}
	}
	for _, p := range procs {
		if p.PID <= 1 || p.UID != uid || p.PPID == self || p.HasTerminal() || agentProcess(p) {
			spared[p.PID] = true
		}
	}
	return spared
}

// agentProcess is a process session discovery would take for an agent:
// claude, any codex, and the helper a hardened codex runs its code in.
func agentProcess(p Process) bool {
	if len(p.Argv) == 0 {
		return false
	}
	switch filepath.Base(p.Argv[0]) {
	case "claude", "codex-code-mode-host":
		return true
	}
	role, _, _ := classifyCodex(p.Argv, p.Exact)
	return role != codexNone
}

// In is the processes working in the worktree at path or in a directory
// inside it, by pid. path is resolved first, as SessionsAt does, since a
// process's directory is reported with its symlinks resolved.
func (s *ProcessScan) In(path string) []Process {
	if s == nil {
		return nil
	}
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	}
	var in []Process
	for _, p := range s.procs {
		if repo.Inside(path, s.cwds[p.PID], false) {
			in = append(in, p)
		}
	}
	slices.SortFunc(in, func(a, b Process) int { return a.PID - b.PID })
	return in
}

// What ending one process came to.
const (
	// ReapTerminated is a process that exited after SIGTERM.
	ReapTerminated = "terminated"
	// ReapKilled is a process that outlived SIGTERM's grace and was ended
	// by SIGKILL.
	ReapKilled = "killed"
	// ReapGone is a process that had exited before it was signalled.
	ReapGone = "gone"
	// ReapSurvived is a process still running after SIGKILL, or one wt may
	// not signal.
	ReapSurvived = "survived"
)

// Reaped is one process Reap tried to end and what that came to.
type Reaped struct {
	Process
	Result string
}

// killWait is how long a process is given to go after SIGKILL. A var so
// tests need not sit through it.
var killWait = 2 * time.Second

// Reap ends procs: SIGTERM to all, up to grace for them to exit, then
// SIGKILL to whatever is left. A zombie counts as exited: it has exited and
// waits only for its parent to read its status, so SIGTERM to it succeeds
// and it is reported terminated, not gone.
func Reap(procs []Process, grace time.Duration) []Reaped {
	out := make([]Reaped, len(procs))
	var pending []int
	for i, p := range procs {
		out[i].Process = p
		switch err := syscall.Kill(p.PID, syscall.SIGTERM); {
		case errors.Is(err, syscall.ESRCH):
			out[i].Result = ReapGone
		case err != nil:
			out[i].Result = ReapSurvived
		default:
			pending = append(pending, i)
		}
	}
	pending = waitGone(out, pending, grace, ReapTerminated)
	var killed []int
	for _, i := range pending {
		switch err := syscall.Kill(out[i].PID, syscall.SIGKILL); {
		case errors.Is(err, syscall.ESRCH):
			out[i].Result = ReapTerminated
		case err != nil:
			out[i].Result = ReapSurvived
		default:
			killed = append(killed, i)
		}
	}
	for _, i := range waitGone(out, killed, killWait, ReapKilled) {
		out[i].Result = ReapSurvived
	}
	return out
}

// waitGone waits up to limit for the processes at pending to exit, marking
// each that does with result, and returns the ones still running.
func waitGone(out []Reaped, pending []int, limit time.Duration, result string) []int {
	deadline := time.Now().Add(limit)
	for len(pending) > 0 {
		pids := make([]int, len(pending))
		for k, i := range pending {
			pids[k] = out[i].PID
		}
		alive := running(pids)
		var still []int
		for _, i := range pending {
			if alive[out[i].PID] {
				still = append(still, i)
			} else {
				out[i].Result = result
			}
		}
		pending = still
		if len(pending) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return pending
}

// running is which of pids are still running: signal 0 says a pid is
// there, and ps says whether that is a zombie. Signal 0 alone decides that
// a pid is gone; ps only drops the zombies. A ps that does not answer
// leaves every pid running, so a process is never reported gone that may
// not be, and the removal then refuses.
func running(pids []int) map[int]bool {
	alive := map[int]bool{}
	var ask []string
	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
			alive[pid] = true
			ask = append(ask, strconv.Itoa(pid))
		}
	}
	if len(ask) == 0 {
		return alive
	}
	cmd := exec.Command(systemTool("ps"), "-o", "pid=", "-o", "stat=", "-p", strings.Join(ask, ","))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// ps exits 1, printing nothing, when none of the pids is there any
	// more. Any other failure says nothing about them.
	timedOut, code, err := git.RunBounded(Deadline, cmd)
	if timedOut || (err != nil && (code != 1 || strings.TrimSpace(stdout.String()) != "")) {
		return alive
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.HasPrefix(f[1], "Z") {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil {
			delete(alive, pid)
		}
	}
	return alive
}

// ReapedLine says what ending one process took, as wt prints it.
func ReapedLine(r Reaped) string {
	what := fmt.Sprintf("pid %d (%s)", r.PID, r.Short(60))
	switch r.Result {
	case ReapTerminated:
		return "stopped " + what + " with SIGTERM"
	case ReapKilled:
		return "killed " + what + " with SIGKILL: it outlived SIGTERM"
	case ReapGone:
		return what + " had already exited"
	}
	return what + " did not stop"
}
