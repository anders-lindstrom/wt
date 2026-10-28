package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// Main runs a command package's tests off the machine running them:
// gittest.Isolate, XDG_CONFIG_HOME a directory of this run's own,
// kit.FindGitHub "no gh here" and kit.MachineAgents NoAgents, then fakes,
// which put the hooks the package's tests reach in a state that reaches
// nothing real, until a test asks for another. It does not return. Keep it
// in step with TestMain in kit, which repeats these steps because kit cannot
// import testkit.
func Main(m *testing.M, fakes ...func()) {
	restore := gittest.Isolate()
	dir, err := os.MkdirTemp("", "wt-"+filepath.Base(os.Args[0])+"-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	kit.FindGitHub = func() (github.CLI, bool) { return github.CLI{}, false }
	kit.MachineAgents = NoAgents
	// A test that reads the real process table waits a minute for ps: it
	// answers in well under a second, but a machine running several test
	// suites at once has taken more than the 10 s wt gives it.
	agents.Deadline = time.Minute
	for _, fake := range fakes {
		fake()
	}
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, "testkit: leaked", dir+":", err)
	}
	restore()
	os.Exit(code)
}
