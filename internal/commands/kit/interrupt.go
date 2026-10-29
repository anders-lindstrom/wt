package kit

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// InterruptStatus is what a shell expects from a command a signal ended.
const InterruptStatus = 130

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

// OnInterrupt cleans up what a signal caught mid-flight: it kills every
// process group still running (a git, a script, a deferred step, claude
// agents), removes the temporary directories the assessments and strategies
// still have open, releases the locks so the next run is not blocked for
// LockExpiry, and says how to put back a worktree left mid-rebase.
// Everything is best effort; the process exits straight after.
func OnInterrupt(w io.Writer, locks []*wtsync.Lock, at *RebaseInFlight) {
	// The groups die first: nothing may still be writing to a worktree
	// whose lock we are about to drop, or to a temporary index we are about
	// to remove. The goroutines that ran them park rather than act on the
	// failure while this handler is still cleaning up. The process exits
	// from here without running any deferred cleanup, so the directories go
	// now or never.
	git.Interrupt()
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

// WatchSignals handles Ctrl-C and SIGTERM for the length of one command,
// exiting 128+signal after OnInterrupt has cleaned up. The returned function stops
// the handler and must be deferred: a signal after the command has finished
// is the shell's business, not wt's.
//
// Watches nest: a bulk run watches for its whole length and each
// repository's rebase watches again inside it. One handler serves the
// process and acts on the innermost watch only, which is the one that knows
// the rebase in flight.
func WatchSignals(w io.Writer, t *RebaseTracker) func() {
	f := &watchFrame{w: w, t: t}
	watchMu.Lock()
	watchStack = append(watchStack, f)
	if len(watchStack) == 1 {
		watchCh = make(chan os.Signal, 1)
		watchDone, watchFinished = make(chan struct{}), make(chan struct{})
		signal.Notify(watchCh, os.Interrupt, syscall.SIGTERM)
		go handleSignals(watchCh, watchDone, watchFinished)
	}
	watchMu.Unlock()
	return func() {
		watchMu.Lock()
		watchStack = without(watchStack, f)
		var finished chan struct{}
		if len(watchStack) == 0 && watchCh != nil {
			signal.Stop(watchCh)
			close(watchDone)
			finished = watchFinished
			watchCh, watchDone, watchFinished = nil, nil, nil
		}
		watchMu.Unlock()
		// The handler acts on a signal that came in before the stop, and
		// the command must not return, and exit 0, before it has. Waited
		// for outside the lock, which the handler takes.
		if finished != nil {
			<-finished
		}
	}
}

// watchFrame is one WatchSignals call still in force.
type watchFrame struct {
	w io.Writer
	t *RebaseTracker
}

var (
	watchMu    sync.Mutex
	watchStack []*watchFrame
	watchCh    chan os.Signal
	watchDone  chan struct{}
	// watchFinished is closed once the handler has returned: never, when
	// it acted on a signal, since that exits.
	watchFinished chan struct{}
)

// beforeHandle runs as the handler starts, before it waits for anything.
// Tests set it to hold the handler until the teardown has begun.
var beforeHandle func(done <-chan struct{})

func handleSignals(ch <-chan os.Signal, done <-chan struct{}, finished chan<- struct{}) {
	defer close(finished)
	if beforeHandle != nil {
		beforeHandle(done)
	}
	select {
	case sig := <-ch:
		watchMu.Lock()
		if len(watchStack) == 0 {
			// The last watch ended as the signal came in.
			watchMu.Unlock()
			exitLate(sig)
			return
		}
		f := watchStack[len(watchStack)-1]
		watchMu.Unlock()
		at := f.t.Get()
		var said bytes.Buffer
		OnInterrupt(io.MultiWriter(f.w, &said), wtsync.HeldLocks(), at)
		// A --json run owes its one object even now: the participants
		// so far, the one in flight, and the way back that was printed.
		if j := CurrentInterruptJournal(); j != nil {
			inFlight := ""
			if at != nil {
				inFlight = at.Work
			}
			j.Interrupted(inFlight, strings.TrimSpace(said.String()))
		}
		os.Exit(128 + int(sig.(syscall.Signal)))
	case <-done:
		// The last watch stopped notifications before it closed done, so a
		// signal that came in before that is in ch now, and select may have
		// picked done over it.
		select {
		case sig := <-ch:
			exitLate(sig)
		default:
		}
	}
}

// exitLate ends the process for a signal that came in as the last watch
// ended, as a handled one would: 128+signal. The command's own cleanup has
// run by then, so there is nothing left to clean up or say. Sending the
// signal back to the process instead would race the command's exit, and
// does nothing at all where the shell started wt with it ignored.
var exitLate = func(sig os.Signal) { os.Exit(128 + int(sig.(syscall.Signal))) }

// without is stack less the frame e, wherever it sits: a watch normally ends
// innermost first, but one that does not must not take another with it.
func without[T comparable](stack []T, e T) []T {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == e {
			return append(stack[:i:i], stack[i+1:]...)
		}
	}
	return stack
}

// interruptJournals are the journals a signal could finish, innermost last:
// a --json run under way sets one, and WatchSignals hands the innermost the
// worktree in flight and the recovery it printed.
var (
	interruptMu       sync.Mutex
	interruptJournals []*journalFrame
)

// journalFrame is one SetInterruptJournal call still in force. It is a
// pointer so that the same journal set twice is still two frames.
type journalFrame struct{ j Interruptible }

// Interruptible is a journal a signal can finish, such as syncrun.RunJournal or retire.SweepJournal.
type Interruptible interface {
	Interrupted(inFlight, recovery string)
}

// SetInterruptJournal makes j the journal a signal finishes, and returns
// the func that hands that back to the journal set before it, if any.
func SetInterruptJournal(j Interruptible) func() {
	f := &journalFrame{j: j}
	interruptMu.Lock()
	interruptJournals = append(interruptJournals, f)
	interruptMu.Unlock()
	return func() {
		interruptMu.Lock()
		interruptJournals = without(interruptJournals, f)
		interruptMu.Unlock()
	}
}

// CurrentInterruptJournal is the journal a signal would finish now, if any.
func CurrentInterruptJournal() Interruptible {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	if len(interruptJournals) == 0 {
		return nil
	}
	return interruptJournals[len(interruptJournals)-1].j
}
