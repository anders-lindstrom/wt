package testkit

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// GitAncestor reports whether a is an ancestor of b; exit 1 is a plain no.
func GitAncestor(t *testing.T, dir, a, b string) bool {
	t.Helper()
	_, err := gittest.Try(t, dir, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false
	}
	t.Fatalf("merge-base --is-ancestor %s %s: %v", a, b, err)
	return false
}

// IdleIn is one interactive session parked in path, as claude lists it: the
// cwd resolved.
func IdleIn(t *testing.T, path, name string) []wtsync.Agent {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return []wtsync.Agent{{ID: name, Name: name, Cwd: resolved, Kind: "interactive", Status: "idle"}}
}
