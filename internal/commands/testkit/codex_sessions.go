package testkit

import (
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// CodexSession is a codex session in cwd.
func CodexSession(cwd, status string) wtsync.Agent {
	resolved, _ := filepath.EvalSymlinks(cwd)
	return wtsync.Agent{Tool: wtsync.ToolCodex, ID: "codex-4242", Name: "codex exec", Cwd: resolved, Status: status, PID: 4242}
}
