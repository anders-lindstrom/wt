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
)

// InterruptStatus is what a shell expects from a command a signal ended.
const InterruptStatus = 130

// An Interrupt is what a signal does inside one watch once every process
// group has been killed: it puts away what the run still has open and says
// on w how to get back what it left half done. It returns the work it caught
// in flight, "" for none. A nil Interrupt has nothing to put away and says
// "interrupted"; syncrun.WatchSignals is the one a run that rebases passes.
type Interrupt func(w io.Writer) (inFlight string)

// WatchSignals handles Ctrl-C and SIGTERM for the length of one command,
// exiting 128+signal after on has cleaned up. The returned function stops
// the handler and must be deferred: a signal after the command has finished
// is the shell's business, not wt's.
//
// Watches nest: a bulk run watches for its whole length and each
// repository's rebase watches again inside it. One handler serves the
// process and acts on the innermost watch only, which is the one that knows
// the rebase in flight.
func WatchSignals(w io.Writer, on Interrupt) func() {
	f := &watchFrame{w: w, on: on}
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
	w  io.Writer
	on Interrupt
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
		var said bytes.Buffer
		inFlight := interrupt(io.MultiWriter(f.w, &said), f.on)
		// A --json run owes its one object even now: the participants
		// so far, the one in flight, and the way back that was printed.
		if j := CurrentInterruptJournal(); j != nil {
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

// interrupt cleans up what a signal caught mid-flight: it kills every process
// group still running (a git, a script, a deferred step, claude agents), then
// leaves the rest to on. Everything is best effort; the process exits
// straight after.
func interrupt(w io.Writer, on Interrupt) (inFlight string) {
	// The groups die first: nothing may still be writing to a worktree
	// whose lock on is about to drop, or to a temporary index it is about
	// to remove. The goroutines that ran them park rather than act on the
	// failure while this handler is still cleaning up.
	git.Interrupt()
	if on == nil {
		fmt.Fprintln(w, "\ninterrupted")
		return ""
	}
	return on(w)
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
