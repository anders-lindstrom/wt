package wtsync

import (
	"cmp"
	"context"
	"encoding/json"
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
)

// agentsDeadline bounds claude agents --json, the one CLI other than git on
// every acting path. A claude that has not answered by then is stuck, and a
// run that cannot tell who is in a worktree refuses rather than waits.
var agentsDeadline = 10 * time.Second

// Agent is a Claude session, from `claude agents --json`. It is enough to
// answer "is a session living in this worktree, and is it doing anything";
// it says nothing about Codex, a dev server or a running test, so the dirty
// check stays the real guard (spec §1).
type Agent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Cwd    string `json:"cwd"`
	State  string `json:"state"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	PID    int    `json:"pid"`
}

// Idle is an interactive session waiting for its person: status idle and no
// state. A background session is never idle — blocked on a question it
// reports status idle too — and a listing with no status, or with a value
// nobody has seen, is not known to be idle, so it counts as busy.
func (a Agent) Idle() bool {
	return a.Status == "idle" && a.State == "" && a.Kind != "background"
}

// ParseAgents decodes the listing and drops finished sessions, which stay in
// the list with state "done" and are nobody.
func ParseAgents(data []byte) ([]Agent, error) {
	var raw []Agent
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var live []Agent
	for _, a := range raw {
		if a.State != "done" {
			live = append(live, a)
		}
	}
	return live, nil
}

// ListAgents asks claude for its sessions. No claude on the PATH means no
// sessions, not an error: "nobody to ask" is a normal state. It runs in its
// own process group, so the deadline takes down whatever it forked.
func ListAgents() ([]Agent, error) {
	exe, err := exec.LookPath("claude")
	if err != nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentsDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "agents", "--json")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("claude agents --json did not answer within %s", agentsDeadline)
	}
	if err != nil {
		return nil, errors.New("claude agents --json failed: " + stderrOf(err))
	}
	return ParseAgents(out)
}

// stderrOf extracts a command's captured stderr from its exec error, empty
// when err carries none.
func stderrOf(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return strings.TrimSpace(string(exit.Stderr))
	}
	return ""
}

// Sessions are the live sessions in one worktree, shallowest working
// directory first.
type Sessions []Agent

// SessionsAt returns every session whose working directory is the worktree
// at path or a directory inside it. A worktree's path can carry a symlink (a
// macOS /tmp, a mounted home) that a session's reported cwd has already
// resolved, so path is resolved first.
func SessionsAt(agents []Agent, path string) Sessions {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	root := filepath.Clean(path)
	sep := string(filepath.Separator)
	var in Sessions
	for _, a := range agents {
		cwd := filepath.Clean(a.Cwd)
		if cwd == root || strings.HasPrefix(cwd, root+sep) {
			in = append(in, a)
		}
	}
	slices.SortStableFunc(in, func(x, y Agent) int {
		xc, yc := filepath.Clean(x.Cwd), filepath.Clean(y.Cwd)
		return cmp.Or(cmp.Compare(strings.Count(xc, sep), strings.Count(yc, sep)), cmp.Compare(len(xc), len(yc)))
	})
	return in
}

// AgentAt returns the shallowest session in the worktree at path, or nil.
func AgentAt(agents []Agent, path string) *Agent {
	if s := SessionsAt(agents, path); len(s) > 0 {
		return &s[0]
	}
	return nil
}

// Busy is the sessions that are not idle.
func (s Sessions) Busy() Sessions {
	var busy Sessions
	for _, a := range s {
		if !a.Idle() {
			busy = append(busy, a)
		}
	}
	return busy
}

// Lead is the session a label names: the first busy one, which is what
// keeps a verb off the worktree, else the first. Nil when there are none.
func (s Sessions) Lead() *Agent {
	for i := range s {
		if !s[i].Idle() {
			return &s[i]
		}
	}
	if len(s) == 0 {
		return nil
	}
	return &s[0]
}

// Label names the sessions in one worktree: the lead by name, "(idle)" when
// the lead is idle — and since a busy one leads, that means all of them are
// — and a count of the rest, as in "parked-1 (idle) +1".
func (s Sessions) Label(name func(*Agent) string) string {
	lead := s.Lead()
	if lead == nil {
		return ""
	}
	label := name(lead)
	if lead.Idle() {
		label += " (idle)"
	}
	if n := len(s) - 1; n > 0 {
		label += " +" + strconv.Itoa(n)
	}
	return label
}

// Arrived is the sessions in s that since does not have: a session opened
// after the others were named. Sessions match on id when both carry one, and
// otherwise on name and working directory.
func (s Sessions) Arrived(since Sessions) Sessions {
	var arrived Sessions
	for _, a := range s {
		if !slices.ContainsFunc(since, func(b Agent) bool {
			if a.ID != "" && b.ID != "" {
				return a.ID == b.ID
			}
			return a.Name == b.Name && a.Cwd == b.Cwd
		}) {
			arrived = append(arrived, a)
		}
	}
	return arrived
}

// WithoutCaller drops the sessions wt is itself running under: a session
// whose pid is an ancestor of this process. An agent resolving a handed-over
// worktree runs wt sync resume from inside it and is busy while it does;
// counting it would make it refuse itself.
func WithoutCaller(agents []Agent, ancestors map[int]bool) []Agent {
	var others []Agent
	for _, a := range agents {
		if a.PID > 1 && ancestors[a.PID] {
			continue
		}
		others = append(others, a)
	}
	return others
}

// Ancestors is the pid of every process above this one, read from one ps
// under the same deadline as claude agents.
func Ancestors() (map[int]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentsDeadline)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=", "-o", "ppid=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	parent := map[int]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, perr := strconv.Atoi(f[0])
		ppid, qerr := strconv.Atoi(f[1])
		if perr == nil && qerr == nil {
			parent[pid] = ppid
		}
	}
	ancestors := map[int]bool{}
	for pid := os.Getppid(); pid > 1 && !ancestors[pid]; pid = parent[pid] {
		ancestors[pid] = true
	}
	return ancestors, nil
}

// ListOtherAgents is ListAgents without the session wt runs under. When the
// process tree cannot be read nobody is dropped: the caller then counts like
// any other session, which refuses rather than rebases.
func ListOtherAgents() ([]Agent, error) {
	agents, err := ListAgents()
	if err != nil || len(agents) == 0 {
		return agents, err
	}
	ancestors, aerr := Ancestors()
	if aerr != nil {
		return agents, nil
	}
	return WithoutCaller(agents, ancestors), nil
}
