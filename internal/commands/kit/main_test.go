package kit

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// TestMain keeps this package off the machine running it: gittest.Isolate,
// XDG_CONFIG_HOME a directory of this run's own, FindGitHub "no gh here"
// and MachineAgents nobody, until a test asks for another state. It repeats testkit.Main, which kit
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
	none := func() ([]agents.Agent, error) { return nil, nil }
	MachineAgents = Agents{List: none, Required: none, Codex: none,
		Claude:    func(time.Duration) ([]agents.Agent, bool, error) { return nil, false, nil },
		Ancestors: func() (map[int]bool, error) { return map[int]bool{}, nil },
		Scan:      func([]agents.Agent) (*agents.ProcessScan, error) { return &agents.ProcessScan{}, nil },
		Reap:      agents.Reap}
	agents.Deadline = time.Minute
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, "kit: leaked", dir+":", err)
	}
	restore()
	os.Exit(code)
}
