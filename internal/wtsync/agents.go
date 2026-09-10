package wtsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// agentsDeadline bounds claude agents --json, the one CLI other than git on
// every acting path. A claude that has not answered by then is stuck, and a
// run that cannot tell who is in a worktree refuses rather than waits.
var agentsDeadline = 10 * time.Second

// Agent is a Claude session, from `claude agents --json`. It is enough to
// answer "is a session living in this worktree"; it says nothing about
// Codex, a dev server or a running test, so the dirty check stays the real
// guard (spec §1).
type Agent struct {
	Name  string `json:"name"`
	Cwd   string `json:"cwd"`
	State string `json:"state"`
	Kind  string `json:"kind"`
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

// AgentAt returns a session whose working directory is the worktree at path
// or a directory inside it, preferring the shallowest match. The returned
// pointer aliases the caller's slice.
func AgentAt(agents []Agent, path string) *Agent {
	root := filepath.Clean(path)
	var best *Agent
	for i := range agents {
		cwd := filepath.Clean(agents[i].Cwd)
		if cwd != root && !strings.HasPrefix(cwd, root+string(filepath.Separator)) {
			continue
		}
		if best == nil {
			best = &agents[i]
			continue
		}
		bestCwd := filepath.Clean(best.Cwd)
		bestDepth := strings.Count(bestCwd, string(filepath.Separator))
		cwdDepth := strings.Count(cwd, string(filepath.Separator))
		if cwdDepth < bestDepth || (cwdDepth == bestDepth && len(cwd) < len(bestCwd)) {
			best = &agents[i]
		}
	}
	return best
}
