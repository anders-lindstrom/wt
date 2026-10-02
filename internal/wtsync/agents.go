package wtsync

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// agentsDeadline bounds claude agents --json, the one CLI other than git on
// every acting path. A claude that has not answered by then is stuck, and a
// run that cannot tell who is in a worktree refuses rather than waits.
var agentsDeadline = 10 * time.Second

// Agent is an agent session: a Claude one from `claude agents --json`, or a
// Codex one from the process table and Codex's session logs (codex.go). It
// is enough to answer "is a session living in this worktree, and is it doing
// anything"; it says nothing about a dev server or a running test, so the
// dirty check stays the real guard (spec §1).
//
// For a Codex session Status is "idle" or "busy" and State and Kind are
// empty. ID is "codex-<pid>" and PID the topmost process for a codex running
// in the worktree; for a thread an app-server runs, ID is the thread's id
// and PID the app-server's, or 0 when more than one could be running it.
type Agent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Cwd    string `json:"cwd"`
	State  string `json:"state"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	PID    int    `json:"pid"`
	// Tool is ToolCodex for a Codex session; empty is Claude.
	Tool string `json:"-"`
	// pids is every process of a Codex session. hosted marks a thread an
	// app-server runs, viaHost an interactive codex whose thread one runs.
	pids    []int
	hosted  bool
	viaHost bool
}

// Program is what the session is a session of: ToolClaude or ToolCodex.
func (a Agent) Program() string {
	if a.Tool == "" {
		return ToolClaude
	}
	return a.Tool
}

// Idle is an interactive session waiting for its person: status idle and no
// state. A background session is never idle — blocked on a question it
// reports status idle too — and a listing with no status, or with a value
// nobody has seen, is not known to be idle, so it counts as busy. A Codex
// session is idle when codexSessions found it so.
func (a Agent) Idle() bool {
	if a.Tool == ToolCodex {
		return a.Status == "idle"
	}
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
// sessions, not an error: "nobody to ask" is a normal state. It runs through
// git.RunBounded, so the deadline and an interrupt take down whatever it
// forked.
func ListAgents() ([]Agent, error) {
	exe, err := exec.LookPath("claude")
	if err != nil {
		return nil, nil
	}
	cmd := exec.Command(exe, "agents", "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	timedOut, _, err := git.RunBounded(agentsDeadline, cmd)
	if timedOut {
		return nil, fmt.Errorf("claude agents --json did not answer within %s", agentsDeadline)
	}
	if err != nil {
		// Only a claude that ran and exited has a stderr worth quoting.
		reason := ""
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			reason = strings.TrimSpace(stderr.String())
		}
		return nil, errors.New("claude agents --json failed: " + reason)
	}
	return ParseAgents(stdout.Bytes())
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
	sep := string(filepath.Separator)
	var in Sessions
	for _, a := range agents {
		if repo.Inside(path, a.Cwd, false) {
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

// Ancestors is the pid of every process above this one.
func Ancestors() (map[int]bool, error) {
	procs, err := listProcesses()
	if err != nil {
		return nil, err
	}
	return ancestorsOf(procs), nil
}

// ErrNoClaude is ListOtherAgentsRequired's answer without a claude to ask.
var ErrNoClaude = errors.New("claude is not on the PATH")

// ListOtherAgentsRequired is ListOtherAgents for a caller that has to know
// who is in a worktree: no claude on the PATH is not nobody there, it is
// not knowing, and is an error. Codex sessions are looked for either way.
func ListOtherAgentsRequired() ([]Agent, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, ErrNoClaude
	}
	return ListOtherAgents()
}

// ListOtherAgents is every agent session on the machine but the one wt runs
// under: Claude's from ListAgents, Codex's from codexSessions. A process
// table or a Codex log that cannot be read is an error, not no sessions: a
// caller that does not know who is in a worktree refuses rather than rebases.
func ListOtherAgents() ([]Agent, error) {
	agents, err := ListAgents()
	if err != nil {
		return agents, err
	}
	procs, err := listProcesses()
	if err != nil {
		return nil, fmt.Errorf("cannot look for Codex sessions: %w", err)
	}
	ancestors := ancestorsOf(procs)
	codex, err := codexSessions(procs, ancestors)
	if err != nil {
		return nil, fmt.Errorf("cannot look for Codex sessions: %w", err)
	}
	return append(WithoutCaller(agents, ancestors), codex...), nil
}
