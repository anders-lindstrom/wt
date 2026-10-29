package kit

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

func TestOnInterruptReleasesEveryLockItHolds(t *testing.T) {
	// Two lock files in two stand-in git dirs: a run holding several must
	// not leave any of them for LockExpiry.
	var locks []*wtsync.Lock
	var dirs []string
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		l, err := wtsync.Acquire(dir, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		locks = append(locks, l)
		dirs = append(dirs, dir)
	}
	var out bytes.Buffer
	OnInterrupt(&out, locks, nil)
	for _, dir := range dirs {
		if _, ok, err := wtsync.ReadLock(dir); err != nil || ok {
			t.Fatalf("%s: lock survived (ok %v err %v)", dir, ok, err)
		}
	}
	if !strings.Contains(out.String(), "interrupted") {
		t.Fatalf("out %q", out.String())
	}
}

func TestOnInterruptSaysHowToPutBackAWorktreeLeftMidRebase(t *testing.T) {
	var out bytes.Buffer
	OnInterrupt(&out, nil, &RebaseInFlight{Work: "bump", Path: "/w/bump", Safety: "refs/wt-sync/feat_wt/bump/99"})
	for _, want := range []string{"interrupted while rebasing bump", "git -C /w/bump rebase --abort", "refs/wt-sync/feat_wt/bump/99"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out lacks %q:\n%s", want, out.String())
		}
	}
}

// A resume's rebase holds a person's own resolution: the abort that is the
// right advice for a run would discard it. The sidecar and the rebase both
// survive an interrupt, so the safe answer is to resume again.
func TestOnInterruptDuringAResumePointsAtResumeNotAbort(t *testing.T) {
	var out bytes.Buffer
	OnInterrupt(&out, nil, &RebaseInFlight{Work: "bump", Path: "/w/bump", Safety: "refs/wt-sync/feat_wt/bump/99", Resuming: true})
	s := out.String()
	if !strings.Contains(s, "interrupted while resuming bump") || !strings.Contains(s, "wt sync resume bump") {
		t.Fatalf("out %q", s)
	}
	if strings.Contains(s, "rebase --abort") {
		t.Fatalf("the interrupt advises the abort that discards the person's resolution: %q", s)
	}
}

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
