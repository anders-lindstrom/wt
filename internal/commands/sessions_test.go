package commands

import (
	"os"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/session"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Every test here runs against temporary repositories no session lives in,
// and must not ask the machine's own claude about them: a caller that names
// no sessions gets none, until a test puts a claude of its own in place.
func init() {
	session.ListSessions = func() ([]wtsync.Agent, error) { return nil, nil }
	session.ListClaudeSessions = func(time.Duration) ([]wtsync.Agent, bool, error) { return nil, false, nil }
}

// ownSession is a session wt runs under: its pid is an ancestor of this
// process, as a Claude session's is of the hook it runs.
func ownSession(cwd string) wtsync.Agent {
	return wtsync.Agent{ID: "own", Name: "the-caller", Cwd: cwd, Status: "busy", State: "working", PID: os.Getppid()}
}
