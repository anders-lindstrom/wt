package github

import (
	"os"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// TestMain runs every git these tests start without the config or the GIT_
// environment of whoever runs them.
func TestMain(m *testing.M) {
	restore := gittest.Isolate()
	code := m.Run()
	restore()
	os.Exit(code)
}
