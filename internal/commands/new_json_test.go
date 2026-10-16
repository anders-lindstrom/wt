package commands

import (
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
)

// A signal writes the one object: the step in flight interrupted, and
// nothing written a second time.
var _ kit.Interruptible = (*provision.CreateJournal)(nil)
