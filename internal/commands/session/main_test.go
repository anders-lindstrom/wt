package session

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// TestMain runs the tests off the machine with testkit.Main's fakes, and
// gives a fake claude on the PATH the time a loaded machine needs to start it.
func TestMain(m *testing.M) {
	testkit.Main(m, func() {
		SessionsDeadline = testkit.FakeClaudeDeadline
	})
}
