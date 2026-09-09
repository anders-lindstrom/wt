package wtsync

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

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
// sessions, not an error: "nobody to ask" is a normal state.
func ListAgents() ([]Agent, error) {
	exe, err := exec.LookPath("claude")
	if err != nil {
		return nil, nil
	}
	cmd := exec.Command(exe, "agents", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New("claude agents --json failed: " + stderrOf(err))
	}
	return ParseAgents(out)
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
