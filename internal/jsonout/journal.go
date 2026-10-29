package jsonout

import (
	"io"
	"sync"
)

// Journal collects what a --json run that changes things did, as it does
// it, so that one object is written at the end: by the run when it returns,
// or by the signal handler when it does not. The object is written once,
// and every method is safe from both and on a nil journal.
//
// Res is what the run records, the result object itself for most commands.
// Lock guards it, and whatever the command's own journal keeps beside it.
// Never run git with the lock held: a signal parks the goroutine running
// it, and the handler could then never write the object.
type Journal[R any] struct {
	mu    sync.Mutex
	once  sync.Once
	out   io.Writer
	rules JournalRules[R]
	Res   R
	// inFlight is the step Start named and Settle has not yet settled: the
	// one a signal would catch.
	inFlight string
	// applying is set once the run changes things: from then on an error is
	// a step's, not the run's.
	applying bool
	// signalled is set by the signal handler as it writes the object.
	signalled bool
}

// JournalRules are what one command's journal does its own way. Every rule
// but Snapshot is called with the lock held.
type JournalRules[R any] struct {
	// Error records an error that ended the run before it changed anything.
	Error func(r *R, err error)
	// Caught settles what a signal caught: inFlight is the step Start named,
	// else the one the handler names, and recovery is what it printed. It
	// may run git: the handler's own git is not parked.
	Caught func(r *R, inFlight, recovery string)
	// Close is the object to write: it sets the outcome, which follows from
	// what was recorded, or is interrupted for a run a signal ended.
	Close func(r *R, signalled bool, recovery string) any
	// Snapshot, set instead of Close, is the object to write for a command
	// that reads it back from git as it is now: it is called on a copy of
	// Res, without the lock and before the object is claimed, since a signal
	// parks the git a returning run is reading with and the handler must
	// still be able to write.
	Snapshot func(r R, signalled bool, recovery string) any
}

// NewJournal is a journal that writes its object to out, starting from res.
func NewJournal[R any](out io.Writer, res R, rules JournalRules[R]) *Journal[R] {
	return &Journal[R]{out: out, Res: res, rules: rules}
}

// Lock takes the lock that guards Res.
func (j *Journal[R]) Lock() { j.mu.Lock() }

// Unlock releases it.
func (j *Journal[R]) Unlock() { j.mu.Unlock() }

// Edit changes Res under the lock.
func (j *Journal[R]) Edit(edit func(r *R)) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	edit(&j.Res)
}

// Begin marks the point after which the run changes things: an error from
// then on is a step's, which the steps record, not the run's. edit, when
// set, records what the run begins with under the same lock.
func (j *Journal[R]) Begin(edit func(r *R)) { j.applyingIs(true, edit) }

// Abandon takes Begin back: the run changed nothing after all, so an error
// is the run's again. edit, when set, takes back what it began with.
func (j *Journal[R]) Abandon(edit func(r *R)) { j.applyingIs(false, edit) }

func (j *Journal[R]) applyingIs(applying bool, edit func(r *R)) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.applying = applying
	if edit != nil {
		edit(&j.Res)
	}
}

// Start marks the step about to run: the one a signal would catch.
func (j *Journal[R]) Start(step string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inFlight = step
}

// Settle records what a step came to, under the lock; it is no longer the
// one in flight.
func (j *Journal[R]) Settle(step string, edit func(r *R)) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.inFlight == step {
		j.inFlight = ""
	}
	edit(&j.Res)
}

// RunError records err as what ended the run, unless the run had begun to
// change things.
func (j *Journal[R]) RunError(err error) {
	if j == nil || err == nil || j.rules.Error == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.applying {
		j.rules.Error(&j.Res, err)
	}
}

// Fail records an error that stopped the run before it began, and writes
// the object: what the caller does when it cannot even open the repository.
func (j *Journal[R]) Fail(err error) {
	j.RunError(err)
	j.Finish()
}

// Finish writes the object for a run that returned.
func (j *Journal[R]) Finish() { j.write(false, "", "") }

// Interrupted writes the object for a run a signal ended (kit.Interruptible):
// the step in flight is caught, the rest stand as recorded. inFlight is the
// rebase tracker's, recovery what the handler printed.
func (j *Journal[R]) Interrupted(inFlight, recovery string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.signalled = true
	j.mu.Unlock()
	j.write(true, inFlight, recovery)
}

// Park blocks for good once a signal has taken the run over: the handler
// writes the object and exits, and the run must neither go on nor return
// and exit with another status first.
func (j *Journal[R]) Park() {
	if j == nil {
		return
	}
	j.mu.Lock()
	signalled := j.signalled
	j.mu.Unlock()
	if signalled {
		select {}
	}
}

func (j *Journal[R]) write(signalled bool, inFlight, recovery string) {
	if j == nil || j.out == nil {
		return
	}
	if j.rules.Snapshot != nil {
		j.mu.Lock()
		r := j.Res
		j.mu.Unlock()
		o := j.rules.Snapshot(r, signalled, recovery)
		j.once.Do(func() { _ = WriteJSON(j.out, o) })
		return
	}
	j.once.Do(func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if signalled && j.rules.Caught != nil {
			if j.inFlight != "" {
				inFlight = j.inFlight
			}
			j.rules.Caught(&j.Res, inFlight, recovery)
		}
		_ = WriteJSON(j.out, j.rules.Close(&j.Res, signalled, recovery))
	})
}
