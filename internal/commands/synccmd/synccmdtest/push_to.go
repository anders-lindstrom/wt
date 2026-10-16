package synccmdtest

import "github.com/anders-lindstrom/wt/internal/commands/synccmd"

// PushAlways is NoAgents with --push.
func PushAlways() synccmd.RunOptions {
	opts := NoAgents()
	opts.Push = synccmd.PushAlways
	return opts
}
