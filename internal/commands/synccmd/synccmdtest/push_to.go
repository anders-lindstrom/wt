package synccmdtest

import (
	"github.com/anlindstrom/wt/internal/commands/synccmd"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
)

// PushAlways is NoAgents with --push.
func PushAlways() synccmd.RunOptions {
	opts := NoAgents()
	opts.Push = syncrun.PushAlways
	return opts
}
