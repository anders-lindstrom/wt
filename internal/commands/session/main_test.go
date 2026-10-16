package session

import (
	"os"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// TestMain keeps this package off the machine running it: gittest.Isolate,
// XDG_CONFIG_HOME a directory of this run's own, kit.FindGitHub "no gh
// here", and no session listed by the machine's own claude, until a test
// asks for another state.
func TestMain(m *testing.M) {
	restore := gittest.Isolate()
	dir, err := os.MkdirTemp("", "wt-session-test")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	kit.FindGitHub = func() (github.CLI, bool) { return github.CLI{}, false }
	ListSessions = func() ([]wtsync.Agent, error) { return nil, nil }
	ListClaudeSessions = func(time.Duration) ([]wtsync.Agent, bool, error) { return nil, false, nil }
	code := m.Run()
	_ = os.RemoveAll(dir)
	restore()
	os.Exit(code)
}
