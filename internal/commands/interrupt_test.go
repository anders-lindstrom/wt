package commands

import (
	"bytes"
	"strings"
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
	onInterrupt(&out, locks, nil)
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
	onInterrupt(&out, nil, &rebaseInFlight{work: "bump", path: "/w/bump", safety: "refs/wt-sync/feat_wt/bump/99"})
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
	onInterrupt(&out, nil, &rebaseInFlight{work: "bump", path: "/w/bump", safety: "refs/wt-sync/feat_wt/bump/99", resuming: true})
	s := out.String()
	if !strings.Contains(s, "interrupted while resuming bump") || !strings.Contains(s, "wt sync resume bump") {
		t.Fatalf("out %q", s)
	}
	if strings.Contains(s, "rebase --abort") {
		t.Fatalf("the interrupt advises the abort that discards the person's resolution: %q", s)
	}
}
