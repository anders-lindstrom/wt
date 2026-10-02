package wtsync

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
)

// Process is one row of the machine's process table. Argv is the command
// line split on spaces, so an argument with a space in it is several, unless
// Exact says it is the arguments as the process was given them.
type Process struct {
	PID, PPID, UID int
	Started        time.Time
	Argv           []string
	Exact          bool
}

// systemTool is where ps or lsof is: on the PATH, or where the system keeps
// it, since a git hook or a launchd job may run wt with a PATH that has
// neither. The bare name, for exec to fail on, when it is nowhere.
func systemTool(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	for _, dir := range []string{"/bin", "/usr/bin", "/usr/sbin", "/sbin"} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return name
}

// listProcesses reads the process table with one ps, under the same
// deadline as claude agents. A var so tests name the processes themselves.
var listProcesses = func() ([]Process, error) {
	cmd := exec.Command(systemTool("ps"), "-A", "-ww", "-o", "pid=", "-o", "ppid=", "-o", "uid=", "-o", "etime=", "-o", "command=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var out bytes.Buffer
	cmd.Stdout = &out
	if timedOut, _, err := git.RunBounded(agentsDeadline, cmd); timedOut {
		return nil, fmt.Errorf("ps did not answer within %s", agentsDeadline)
	} else if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	procs := parseProcesses(out.String(), time.Now())
	// ps joins the arguments with spaces, which a prompt or a path with a
	// space in it does not survive. The few commands that may be Codex are
	// read again, exactly.
	for i, p := range procs {
		if !strings.Contains(strings.Join(p.Argv, " "), "codex") {
			continue
		}
		if argv, ok := processArgv(p.PID); ok {
			procs[i].Argv, procs[i].Exact = argv, true
		}
	}
	return procs, nil
}

// parseProcesses reads ps rows of pid, ppid, uid, etime and command. A row
// that does not parse is not a process.
func parseProcesses(out string, now time.Time) []Process {
	var procs []Process
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, perr := strconv.Atoi(f[0])
		ppid, qerr := strconv.Atoi(f[1])
		uid, uerr := strconv.Atoi(f[2])
		age, ok := parseElapsed(f[3])
		if perr != nil || qerr != nil || uerr != nil || !ok {
			continue
		}
		procs = append(procs, Process{PID: pid, PPID: ppid, UID: uid, Started: now.Add(-age), Argv: f[4:]})
	}
	return procs
}

// parseElapsed reads ps's etime, [[dd-]hh:]mm:ss.
func parseElapsed(s string) (time.Duration, bool) {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	secs := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, false
		}
		secs = secs*60 + n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(secs)*time.Second, true
}

// processCwds reads the working directory of each pid: from /proc where
// there is one, from one lsof otherwise. A pid that has exited since it was
// listed is in neither answer. One that is still there and may not be read
// is denied: its directory is not known, which is not the same as nowhere.
// A var so tests name the directories themselves.
var processCwds = func(pids []int) (cwds map[int]string, denied map[int]bool, err error) {
	cwds, denied = map[int]string{}, map[int]bool{}
	if len(pids) == 0 {
		return cwds, denied, nil
	}
	if _, err := os.Stat("/proc/self/cwd"); err == nil {
		for _, pid := range pids {
			dir, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "cwd"))
			switch {
			case err == nil:
				cwds[pid] = dir
			case errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH):
			case errors.Is(err, os.ErrPermission):
				denied[pid] = true
			default:
				return nil, nil, fmt.Errorf("cannot read the working directory of process %d: %w", pid, err)
			}
		}
		return cwds, denied, nil
	}
	list := make([]string, len(pids))
	for i, pid := range pids {
		list[i] = strconv.Itoa(pid)
	}
	cmd := exec.Command(systemTool("lsof"), "-a", "-d", "cwd", "-Fpn", "-p", strings.Join(list, ","))
	var out bytes.Buffer
	cmd.Stdout = &out
	// lsof exits 1 when one of the pids is gone and still prints the rest,
	// so its output is what is judged.
	if timedOut, _, err := git.RunBounded(agentsDeadline, cmd); timedOut {
		return nil, nil, fmt.Errorf("lsof did not answer within %s", agentsDeadline)
	} else if err != nil && !errors.As(err, new(*exec.ExitError)) {
		return nil, nil, fmt.Errorf("lsof: %w", err)
	}
	pid := 0
	for _, line := range strings.Split(out.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n") && pid > 0:
			cwds[pid] = line[1:]
		}
	}
	for _, pid := range pids {
		if _, ok := cwds[pid]; ok {
			continue
		}
		// Only ps saying so makes a process gone: exit status 1 and
		// nothing printed. Anything else is a process still to account for.
		alive := exec.Command(systemTool("ps"), "-p", strconv.Itoa(pid), "-o", "pid=")
		var seen bytes.Buffer
		alive.Stdout = &seen
		timedOut, code, err := git.RunBounded(agentsDeadline, alive)
		switch {
		case !timedOut && code == 1 && strings.TrimSpace(seen.String()) == "":
		case !timedOut && err == nil:
			denied[pid] = true
		default:
			return nil, nil, fmt.Errorf("cannot tell whether process %d is still running", pid)
		}
	}
	return cwds, denied, nil
}

// ancestorsOf is the pid of every process above this one in procs.
func ancestorsOf(procs []Process) map[int]bool {
	parent := map[int]int{}
	for _, p := range procs {
		parent[p.PID] = p.PPID
	}
	ancestors := map[int]bool{}
	for pid := os.Getppid(); pid > 1 && !ancestors[pid]; pid = parent[pid] {
		ancestors[pid] = true
	}
	return ancestors
}
