package syncrun

import (
	"fmt"
	"io"
	"sync"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// RebaseInFlight is the worktree a run is in the middle of rebasing: what an
// interrupt has to tell the user about, since killing git there leaves the
// rebase stopped rather than finished.
type RebaseInFlight struct {
	Work, Path, Safety string
	// Rebased is set once the rebase itself has finished and the deferred
	// steps are running: the worktree is not stopped mid-rebase any more,
	// it is rebased, and putting it back is undo's job rather than git's.
	Rebased bool
	// Resuming is set while wt sync resume drives a handed-over rebase. The
	// worktree holds a person's own resolution, which an abort would
	// discard, and the handover survives the interrupt, so resuming again is
	// the way back.
	Resuming bool
	// Forwarding is set while the run fast-forwards the branch to its own
	// remote, before any rebase has started: there is nothing to abort.
	Forwarding bool
}

// RebaseTracker carries that across goroutines: the command sets it, the
// signal handler reads it. A nil tracker is one that never rebases, which is
// what sync undo passes.
type RebaseTracker struct {
	mu sync.Mutex
	at *RebaseInFlight
}

// Set records the rebase now in flight, or nil once there is none.
func (t *RebaseTracker) Set(at *RebaseInFlight) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.at = at
	t.mu.Unlock()
}

// Get is the rebase in flight, nil when there is none.
func (t *RebaseTracker) Get() *RebaseInFlight {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.at
}

// WatchSignals is kit.WatchSignals for a command that runs the sync engine:
// a signal also takes the engine's locks and temporary directories with it,
// and says how to put back the worktree t has in flight. A nil t is a
// command that never rebases.
func WatchSignals(w io.Writer, t *RebaseTracker) func() {
	return kit.WatchSignals(w, func(w io.Writer) string {
		at := t.Get()
		OnInterrupt(w, wtsync.HeldLocks(), at)
		if at == nil {
			return ""
		}
		return at.Work
	})
}

// OnInterrupt cleans up what a signal caught mid-flight once kit has killed
// every process group: it removes the temporary directories the assessments
// and strategies still have open, releases the locks so the next run is not
// blocked for LockExpiry, and says how to put back a worktree left
// mid-rebase. Everything is best effort; the process exits straight after.
func OnInterrupt(w io.Writer, locks []*wtsync.Lock, at *RebaseInFlight) {
	// The process exits from the handler without running any deferred
	// cleanup, so the directories go now or never.
	wtsync.RemoveTempDirs()
	for _, l := range locks {
		if l != nil {
			_ = l.Release()
		}
	}
	switch {
	case at == nil:
		fmt.Fprintln(w, "\ninterrupted")
	case at.Rebased:
		// The run never got to pin where it left the branch, so a plain
		// undo would refuse it as moved since the run.
		fmt.Fprintf(w, "\ninterrupted after rebasing %s: %s\n", at.Work, wtsync.WayOut(wtsync.Way{Work: at.Work, Moved: true}))
	case at.Forwarding:
		// No rebase to abort, and a reset to the safety ref by hand would
		// leave the reflog saying the branch once had its remote's commit:
		// undo puts the branch back and takes that word away with it.
		fmt.Fprintf(w, "\ninterrupted while fast-forwarding %s to its own remote: %s\n", at.Work, wtsync.WayOut(wtsync.Way{Work: at.Work, Result: true}))
	case at.Resuming:
		fmt.Fprintf(w, "\ninterrupted while resuming %s: the rebase and its plan are left as they are; %s%s\n", at.Work, wtsync.WayOut(wtsync.Way{Work: at.Work, Plan: true, Rebasing: true}), marksLeftNote(at.Path))
	default:
		fmt.Fprintf(w, "\ninterrupted while rebasing %s: %s%s\n", at.Work, wtsync.WayOut(wtsync.Way{Work: at.Work, Path: at.Path, Rebasing: true, Safety: at.Safety}), marksLeftNote(at.Path))
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
