package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Every test here runs against temporary repositories no session lives in,
// and must not ask the machine's own claude about them: a caller that names
// no sessions gets none, until a test puts a claude of its own in place.
func init() {
	listSessions = func() ([]wtsync.Agent, error) { return nil, nil }
}

// fakeClaude puts a claude on the PATH whose `agents --json` lists agents,
// and has wt ask it the way it asks the real one, own session excluded.
func fakeClaude(t *testing.T, agents []wtsync.Agent) {
	t.Helper()
	data, err := json.Marshal(agents)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	listing := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(listing, data, 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat '" + listing + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	was := listSessions
	listSessions = wtsync.ListOtherAgents
	t.Cleanup(func() { listSessions = was })
}

// ownSession is a session wt runs under: its pid is an ancestor of this
// process, as a Claude session's is of the hook it runs.
func ownSession(cwd string) wtsync.Agent {
	return wtsync.Agent{ID: "own", Name: "the-caller", Cwd: cwd, Status: "busy", State: "working", PID: os.Getppid()}
}
