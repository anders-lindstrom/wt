package testkit

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Background is a background claude session in cwd.
func Background(name, id, state, cwd string) wtsync.Agent {
	return wtsync.Agent{Name: name, ID: id, SessionID: id + "-0000-4000-8000-000000000000", Kind: "background", State: state, Cwd: cwd}
}

// InTerminal is an interactive claude session in a terminal.
func InTerminal(name, status, cwd string, pid int) wtsync.Agent {
	return wtsync.Agent{Name: name, Kind: "interactive", Status: status, Cwd: cwd, PID: pid}
}

// CanonicalWorktree is a repository with one worktree where the convention puts
// it, and the Context opened on it.
func CanonicalWorktree(t *testing.T) (ctx *kit.Context, main, wt string) {
	t.Helper()
	main, wt = RepoWithWorktree(t, func(parent string) string {
		return filepath.Join(parent, "demo_wt", "feat_wt", "thing")
	})
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, main, wt
}
