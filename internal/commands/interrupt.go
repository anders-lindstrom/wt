package commands

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// interruptStatus is what a shell expects from a command a signal ended.
const interruptStatus = 130

// rebaseInFlight is the worktree a run is in the middle of rebasing: what an
// interrupt has to tell the user about, since killing git there leaves the
// rebase stopped rather than finished.
type rebaseInFlight struct {
	work, path, safety string
	// rebased is set once the rebase itself has finished and the deferred
	// steps are running: the worktree is not stopped mid-rebase any more,
	// it is rebased, and putting it back is undo's job rather than git's.
	rebased bool
	// resuming is set while wt sync resume drives a handed-over rebase. The
	// worktree holds a person's own resolution, which an abort would
	// discard, and the handover survives the interrupt, so resuming again is
	// the way back.
	resuming bool
}

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
	// The groups die first: nothing may still be writing to a worktree
	// whose lock we are about to drop.
	wtsync.KillRunning()
	git.KillRunning()
	for _, l := range locks {
		if l != nil {
			_ = l.Release()
		}
	}
	switch {
	case at == nil:
		fmt.Fprintln(w, "\ninterrupted")
	case at.rebased:
		// The run never got to pin where it left the branch, so a plain
		// undo would refuse it as moved since the run.
		fmt.Fprintf(w, "\ninterrupted after rebasing %s: the rebase stands; wt sync undo --force %s puts it back\n", at.work, at.work)
	case at.resuming:
		fmt.Fprintf(w, "\ninterrupted while resuming %s: the rebase and its plan are left as they are; wt sync resume %s picks it up again\n", at.work, at.work)
	default:
		fmt.Fprintf(w, "\ninterrupted while rebasing %s: git -C %s rebase --abort restores it; the old tip is %s\n", at.work, at.path, at.safety)
	}
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
