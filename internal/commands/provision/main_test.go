package provision

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/session"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/superset"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// TestMain keeps this package off the machine running it: gittest.Isolate,
// XDG_CONFIG_HOME a directory of this run's own, ProbeSuperset answering
// "no Superset here", kit.FindGitHub "no gh here", SupersetHostCommand
// reading no process and no session listed by the machine's own claude,
// until a test asks for another state.
func TestMain(m *testing.M) {
	restore := gittest.Isolate()
	dir, err := os.MkdirTemp("", "wt-provision-test")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	ProbeSuperset = func() superset.Status { return superset.Status{} }
	kit.FindGitHub = func() (github.CLI, bool) { return github.CLI{}, false }
	SupersetHostCommand = func(int) (string, error) { return "", errors.New("no ps in tests") }
	session.ListSessions = func() ([]wtsync.Agent, error) { return nil, nil }
	session.ListClaudeSessions = func(time.Duration) ([]wtsync.Agent, bool, error) { return nil, false, nil }
	code := m.Run()
	_ = os.RemoveAll(dir)
	restore()
	os.Exit(code)
}
