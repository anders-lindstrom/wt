package synccmdtest

import (
	"time"

	"github.com/anlindstrom/wt/internal/agents"
	"github.com/anlindstrom/wt/internal/commands/synccmd"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
)

// NoAgents is run options with no session anywhere, no fetch and a fixed
// clock.
func NoAgents() synccmd.RunOptions {
	return synccmd.RunOptions{NoFetch: true, VerbOptions: syncrun.VerbOptions{
		Agents: []agents.Agent{},
		Now:    func() time.Time { return time.Unix(0, 99) },
	}}
}
