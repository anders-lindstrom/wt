package agents

import (
	"os"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// TestMain keeps these tests off the machine running them: gittest.Isolate.
// Deadline is a minute: ps answers in well under a second, but a machine
// running several test suites at once has taken more than 10 s to start it,
// and a hang still fails at a minute.
func TestMain(m *testing.M) {
	restore := gittest.Isolate()
	Deadline = time.Minute
	code := m.Run()
	restore()
	os.Exit(code)
}
