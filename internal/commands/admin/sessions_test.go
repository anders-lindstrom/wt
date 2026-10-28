package admin

import (
	"os"

	"github.com/anders-lindstrom/wt/internal/agents"
)

// ownSession is a session wt runs under: its pid is an ancestor of this
// process, as a Claude session's is of the hook it runs.
func ownSession(cwd string) agents.Agent {
	return agents.Agent{ID: "own", Name: "the-caller", Cwd: cwd, Status: "busy", State: "working", PID: os.Getppid()}
}
