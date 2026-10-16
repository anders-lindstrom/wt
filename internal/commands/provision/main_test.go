package provision

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/session"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// TestMain runs the tests off the machine: no Superset, no process read for
// its host, no session listed, until a test asks for another state.
func TestMain(m *testing.M) {
	testkit.Main(m, func() {
		ProbeSuperset = testkit.NoSuperset
		SupersetHostCommand = testkit.NoHostCommand
		session.ListSessions = testkit.NoSessions
		session.ListClaudeSessions = testkit.NoClaudeSessions
	})
}
