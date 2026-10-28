package testkit

import (
	"errors"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// NoSuperset is a Superset probe answering "no Superset here".
func NoSuperset() superset.Status { return superset.Status{} }

// NoHostCommand reads no process.
func NoHostCommand(int) (string, error) { return "", errors.New("no ps in tests") }

// NoSessions is a session lister that names none.
func NoSessions() ([]agents.Agent, error) { return nil, nil }

// NoClaudeSessions is a claude lister that names none.
func NoClaudeSessions(time.Duration) ([]agents.Agent, bool, error) { return nil, false, nil }

// NoProcesses is a process scan that finds none.
func NoProcesses([]agents.Agent) (*agents.ProcessScan, error) { return &agents.ProcessScan{}, nil }

// FakeClaudeDeadline is how long a listing waits for claude agents --json in
// the tests. Their claude is a shell script, and on a loaded machine starting
// one can take longer than the 2s session.SessionsDeadline gives it.
const FakeClaudeDeadline = time.Minute
