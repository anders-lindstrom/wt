package kit

import (
	"io"
	"os"
	"sync"
	"syscall"
	"testing"
)

type namedJournal string

func (namedJournal) Interrupted(string, string) {}

// A run that sets a journal inside another's, as a bulk run does around
// each repository, hands the signal back to the outer journal when it ends.
func TestSetInterruptJournalNestedRestoresTheOuter(t *testing.T) {
	outer, inner := namedJournal("outer"), namedJournal("inner")
	unsetOuter := SetInterruptJournal(outer)
	defer unsetOuter()
	unsetInner := SetInterruptJournal(inner)
	if got := CurrentInterruptJournal(); got != inner {
		t.Fatalf("inside the inner: %v", got)
	}
	unsetInner()
	if got := CurrentInterruptJournal(); got != outer {
		t.Fatalf("after the inner ended the journal is %v, want outer", got)
	}
	unsetOuter()
	if got := CurrentInterruptJournal(); got != nil {
		t.Fatalf("after both ended: %v", got)
	}
}

// A signal that comes in as the last watch ends finds the handler with both
// its signal and its teardown ready, and select picks either: whichever it
// picks, the signal ends the process rather than being dropped.
func TestASignalAtTheLastWatchsEndIsNotSwallowed(t *testing.T) {
	// Not parallel: swaps exitLate for the package.
	var got []os.Signal
	was := exitLate
	exitLate = func(sig os.Signal) { got = append(got, sig) }
	t.Cleanup(func() { exitLate = was })
	const tries = 64 // select picks at random: 2^-64 that teardown never wins
	for i := 0; i < tries; i++ {
		ch := make(chan os.Signal, 1)
		ch <- syscall.SIGINT
		done := make(chan struct{})
		close(done)
		handleSignals(ch, done, make(chan struct{}))
	}
	if len(got) != tries {
		t.Fatalf("%d of %d signals that came in at the last watch's end were swallowed", tries-len(got), tries)
	}
}

// The real teardown, with a signal pending as the last watch ends: by the
// time the teardown returns, and the command after it can exit 0, the
// handler has acted on the signal. beforeHandle holds the handler until the
// teardown has begun, so the signal is still pending then.
func TestTheLastWatchsTeardownWaitsForAPendingSignal(t *testing.T) {
	// Not parallel: swaps exitLate and beforeHandle for the package.
	var mu sync.Mutex
	var got []os.Signal
	was := exitLate
	exitLate = func(sig os.Signal) { mu.Lock(); got = append(got, sig); mu.Unlock() }
	beforeHandle = func(done <-chan struct{}) { <-done }
	t.Cleanup(func() {
		exitLate = was
		beforeHandle = nil
	})
	const tries = 64
	for i := 0; i < tries; i++ {
		stop := WatchSignals(io.Discard, nil)
		watchMu.Lock()
		watchCh <- syscall.SIGINT
		watchMu.Unlock()
		stop()
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n != i+1 {
			t.Fatalf("try %d: the teardown returned with the pending signal not yet acted on", i+1)
		}
	}
}
