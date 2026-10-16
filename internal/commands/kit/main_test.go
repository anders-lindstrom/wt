package kit

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// TestMain keeps this package off the machine running it: gittest.Isolate,
// XDG_CONFIG_HOME a directory of this run's own and FindGitHub "no gh here",
// until a test asks for another state. It repeats testkit.Main, which kit
// cannot import; keep the two in step.
func TestMain(m *testing.M) {
	restore := gittest.Isolate()
	dir, err := os.MkdirTemp("", "wt-"+filepath.Base(os.Args[0])+"-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	FindGitHub = func() (github.CLI, bool) { return github.CLI{}, false }
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, "kit: leaked", dir+":", err)
	}
	restore()
	os.Exit(code)
}
