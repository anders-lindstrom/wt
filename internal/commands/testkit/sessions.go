package testkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// FakeClaude puts a claude on the PATH whose `agents --json` lists listed,
// and has ctx list the sessions the way wt asks the real one: own session
// excluded, which reads the real process table.
func FakeClaude(t *testing.T, ctx *kit.Context, listed []agents.Agent) {
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
	AgentsOf(ctx).List = agents.ListOtherAgents
}

// ClaudeLists has ctx's claude list sessions, and returns how many times it
// was asked.
func ClaudeLists(ctx *kit.Context, sessions ...agents.Agent) *int {
	asked := new(int)
	AgentsOf(ctx).Claude = func(time.Duration) ([]agents.Agent, bool, error) {
		*asked++
		return sessions, true, nil
	}
	return asked
}

// ListedSessions has ctx list listed as the sessions.
func ListedSessions(ctx *kit.Context, listed ...agents.Agent) {
	AgentsOf(ctx).List = func() ([]agents.Agent, error) { return listed, nil }
}

// MachineClaudeLists is ClaudeLists for a command that opens contexts of its
// own, as --all does: kit.MachineAgents' claude, for the length of the test.
// A test that calls it is not parallel.
func MachineClaudeLists(t *testing.T, sessions ...agents.Agent) *int {
	t.Helper()
	was := kit.MachineAgents
	t.Cleanup(func() { kit.MachineAgents = was })
	var probe kit.Context
	asked := ClaudeLists(&probe, sessions...)
	kit.MachineAgents.Claude = probe.Agents.Claude
	return asked
}
