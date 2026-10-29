package quarantinecmd

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/supersetws"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// TestMain runs the tests off the machine: testkit.Main's fakes, and no
// Superset and no process read for its host, until a test asks for another
// state.
func TestMain(m *testing.M) {
	testkit.Main(m, func() {
		supersetws.Probe = testkit.NoSuperset
		supersetws.HostCommand = testkit.NoHostCommand
	})
}
