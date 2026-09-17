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
// process group still running (a git, a script, a deferred step, claude
// agents), removes the temporary directories the assessments and strategies
// still have open, releases the locks so the next run is not blocked for
// LockExpiry, and says how to put back a worktree left mid-rebase.
// Everything is best effort; the process exits straight after.
func onInterrupt(w io.Writer, locks []*wtsync.Lock, at *rebaseInFlight) {
	// The groups die first: nothing may still be writing to a worktree
	// whose lock we are about to drop, or to a temporary index we are about
	// to remove. The process exits from here without running any deferred
	// cleanup, so the directories go now or never.
	git.KillRunning()
	wtsync.RemoveTempDirs()
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
		fmt.Fprintf(w, "\ninterrupted after rebasing %s: %s\n", at.work, wtsync.WayOut(wtsync.Way{Work: at.work, Moved: true}))
	case at.resuming:
		fmt.Fprintf(w, "\ninterrupted while resuming %s: the rebase and its plan are left as they are; %s%s\n", at.work, wtsync.WayOut(wtsync.Way{Work: at.work, Plan: true, Rebasing: true}), marksLeftNote(at.path))
	default:
		fmt.Fprintf(w, "\ninterrupted while rebasing %s: %s%s\n", at.work, wtsync.WayOut(wtsync.Way{Work: at.work, Path: at.path, Rebasing: true, Safety: at.safety}), marksLeftNote(at.path))
	}
}

// marksLeftNote takes the run's own stops out of the list of a rebase an
// interrupt is leaving to a person, so that continuing it by hand does not
// stop where the run would have, and lands on trunk's version in silence
// where the run would have lifted it. A list that cannot be edited at this
// moment is said so, in the same breath as the way out.
func marksLeftNote(path string) string {
	if err := wtsync.StripMarks(path); err != nil {
		return "; " + wtsync.MarksLeft(err)
	}
	return ""
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
