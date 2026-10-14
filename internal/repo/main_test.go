package repo

import (
	"os"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// TestMain keeps these tests off the machine running them: gittest.Isolate.
func TestMain(m *testing.M) {
	restore := gittest.Isolate()
	code := m.Run()
	restore()
	os.Exit(code)
}
