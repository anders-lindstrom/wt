package testkit

import (
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/agents"
)

// CodexSession is a codex session in cwd.
func CodexSession(cwd, status string) agents.Agent {
	resolved, _ := filepath.EvalSymlinks(cwd)
	return agents.Agent{Tool: agents.ToolCodex, ID: "codex-4242", Name: "codex exec", Cwd: resolved, Status: status, PID: 4242}
}
