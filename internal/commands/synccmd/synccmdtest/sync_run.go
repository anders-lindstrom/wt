package synccmdtest

import (
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// NoAgents is run options with no session anywhere, no fetch and a fixed
// clock.
func NoAgents() synccmd.RunOptions {
	return synccmd.RunOptions{NoFetch: true, VerbOptions: synccmd.VerbOptions{
		Agents: []wtsync.Agent{},
		Now:    func() time.Time { return time.Unix(0, 99) },
	}}
}
