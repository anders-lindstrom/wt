package session

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// TestMain runs the tests off the machine, with no session listed by the
// machine's own claude until a test asks for another state.
func TestMain(m *testing.M) {
	testkit.Main(m, func() {
		ListSessions = testkit.NoSessions
		ListClaudeSessions = testkit.NoClaudeSessions
	})
}
