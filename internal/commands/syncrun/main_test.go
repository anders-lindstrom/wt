package syncrun

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// TestMain runs the tests off the machine, with testkit.Main's fakes.
func TestMain(m *testing.M) {
	testkit.Main(m)
}
