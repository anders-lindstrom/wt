package commands

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/session"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// TestMain runs the tests off the machine: no Superset, no process read for
// its host, no session listed and no process found in a worktree, until a
// test asks for another state.
func TestMain(m *testing.M) {
	testkit.Main(m, func() {
		provision.ProbeSuperset = testkit.NoSuperset
		provision.SupersetHostCommand = testkit.NoHostCommand
		session.ListSessions = testkit.NoSessions
		session.ListClaudeSessions = testkit.NoClaudeSessions
		retire.ScanProcesses = testkit.NoProcesses
	})
}
