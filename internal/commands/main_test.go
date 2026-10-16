package commands

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// TestMain runs the tests off the machine: no Superset and no process read
// for its host, until a test asks for another state. sessions_test.go and
// remove_reap_test.go fake the session listers and the process scan.
func TestMain(m *testing.M) {
	testkit.Main(m, func() {
		provision.ProbeSuperset = testkit.NoSuperset
		provision.SupersetHostCommand = testkit.NoHostCommand
	})
}
