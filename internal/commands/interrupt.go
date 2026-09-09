package commands

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// interruptStatus is what a shell expects from a command a signal ended.
const interruptStatus = 130

// rebaseInFlight is the worktree a run is in the middle of rebasing: what an
// interrupt has to tell the user about, since killing git there leaves the
// rebase stopped rather than finished.
type rebaseInFlight struct{ work, path, safety string }

// rebaseTracker carries that across goroutines: the command sets it, the
// signal handler reads it. A nil tracker is one that never rebases, which is
// what sync undo passes.
type rebaseTracker struct {
	mu sync.Mutex
	at *rebaseInFlight
}

func (t *rebaseTracker) set(at *rebaseInFlight) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.at = at
	t.mu.Unlock()
}

func (t *rebaseTracker) get() *rebaseInFlight {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.at
}

// onInterrupt cleans up what a signal caught mid-flight: it kills every
// process group wtsync still has running (a script, a deferred step, a git),
// releases the locks so the next run is not blocked for LockExpiry, and says
// how to put back a worktree left mid-rebase. Everything is best effort; the
// process exits straight after.
func onInterrupt(w io.Writer, locks []*wtsync.Lock, at *rebaseInFlight) {
	// The group dies first: nothing may still be writing to a worktree
	// whose lock we are about to drop.
	wtsync.KillRunning()
	for _, l := range locks {
		if l != nil {
			_ = l.Release()
		}
	}
	if at != nil {
		fmt.Fprintf(w, "\ninterrupted while rebasing %s: git -C %s rebase --abort restores it; the old tip is %s\n", at.work, at.path, at.safety)
		return
	}
	fmt.Fprintln(w, "\ninterrupted")
}

// watchSignals handles Ctrl-C and SIGTERM for the length of one command,
// exiting 130 after onInterrupt has cleaned up. The returned function stops
// the handler and must be deferred: a signal after the command has finished
// is the shell's business, not wt's.
func watchSignals(w io.Writer, t *rebaseTracker) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			onInterrupt(w, wtsync.HeldLocks(), t.get())
			os.Exit(interruptStatus)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
