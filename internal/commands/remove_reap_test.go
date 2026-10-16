package commands

import (
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Every test in this package runs where the machine's own processes are not
// the tests' business: a caller that starts no process finds none, until a
// test reads the real process table.
func init() {
	retire.ScanProcesses = func([]wtsync.Agent) (*wtsync.ProcessScan, error) { return &wtsync.ProcessScan{}, nil }
}
