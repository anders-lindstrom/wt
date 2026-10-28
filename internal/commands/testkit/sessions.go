package testkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// FakeClaude puts a claude on the PATH whose `agents --json` lists listed,
// and points list, the package's session lister, at the way wt asks the
// real one, own session excluded.
func FakeClaude(t *testing.T, list *func() ([]agents.Agent, error), listed []agents.Agent) {
	t.Helper()
	data, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	listing := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(listing, data, 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat '" + listing + "'\n"
	gittest.WriteExecutable(t, filepath.Join(dir, "claude"), script)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	was := *list
	*list = agents.ListOtherAgents
	t.Cleanup(func() { *list = was })
}

// ClaudeLists has list, the package's claude lister, list sessions for the
// length of the test, and returns how many times it was asked.
func ClaudeLists(t *testing.T, list *func(time.Duration) ([]agents.Agent, bool, error), sessions ...agents.Agent) *int {
	t.Helper()
	asked := new(int)
	was := *list
	*list = func(time.Duration) ([]agents.Agent, bool, error) {
		*asked++
		return sessions, true, nil
	}
	t.Cleanup(func() { *list = was })
	return asked
}

// ListedSessions has list, the package's session lister, name listed for
// the length of the test.
func ListedSessions(t *testing.T, list *func() ([]agents.Agent, error), listed ...agents.Agent) {
	t.Helper()
	was := *list
	*list = func() ([]agents.Agent, error) { return listed, nil }
	t.Cleanup(func() { *list = was })
}
