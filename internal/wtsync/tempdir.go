package wtsync

import (
	"os"
	"sync"
)

// tempDirs is every temporary directory a simulation, a strategy or a run has
// open. The overview assesses worktrees in parallel, and an interrupt exits
// from the signal handler's goroutine before any of their deferred removals
// run; RemoveTempDirs is how the interrupt takes the directories with it.
var tempDirs = struct {
	sync.Mutex
	dirs map[string]bool
}{dirs: map[string]bool{}}

// tempDir makes a private directory under the system's temporary directory,
// registered until cleanup removes it.
func tempDir(pattern string) (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	tempDirs.Lock()
	tempDirs.dirs[dir] = true
	tempDirs.Unlock()
	return dir, func() {
		tempDirs.Lock()
		delete(tempDirs.dirs, dir)
		tempDirs.Unlock()
		_ = os.RemoveAll(dir)
	}, nil
}

// RemoveTempDirs removes every temporary directory still registered and
// reports how many it removed, for an interrupt that exits without running
// deferred cleanups. It runs after the gits are killed, and an assessment
// whose git died bails on the error rather than opening another.
func RemoveTempDirs() int {
	tempDirs.Lock()
	defer tempDirs.Unlock()
	n := 0
	for dir := range tempDirs.dirs {
		if err := os.RemoveAll(dir); err == nil {
			n++
		}
		delete(tempDirs.dirs, dir)
	}
	return n
}
