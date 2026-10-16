package commands

import (
	"errors"
	"os"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// TestMain keeps this package off the machine running it: gittest.Isolate,
// XDG_CONFIG_HOME a directory of this run's own, provision.ProbeSuperset
// answering "no Superset here", kit.FindGitHub "no gh here" and
// provision.SupersetHostCommand reading no process, until a test asks for
// another state.
func TestMain(m *testing.M) {
	restore := gittest.Isolate()
	dir, err := os.MkdirTemp("", "wt-commands-test")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	provision.ProbeSuperset = func() superset.Status { return superset.Status{} }
	kit.FindGitHub = func() (github.CLI, bool) { return github.CLI{}, false }
	provision.SupersetHostCommand = func(int) (string, error) { return "", errors.New("no ps in tests") }
	code := m.Run()
	_ = os.RemoveAll(dir)
	restore()
	os.Exit(code)
}
