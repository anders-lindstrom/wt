package keepstate

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestFmtEvery(t *testing.T) {
	t.Parallel()
	for d, want := range map[time.Duration]string{30 * time.Minute: "30m", time.Hour: "1h", 90 * time.Minute: "1h30m", 45 * time.Second: "45s", 2 * time.Hour: "2h", 70 * time.Second: "1m10s", 80 * time.Second: "1m20s", 90 * time.Second: "1m30s", time.Hour + 5*time.Second: "1h0m5s"} {
		if got := FmtEvery(d); got != want {
			t.Errorf("fmtEvery(%s) = %q, want %q", d, got, want)
		}
	}
}

// A process forked while the lock is held has a copy of its descriptor until
// it execs; releasing the lock frees it all the same, so a pass right after
// is not refused.
func TestReleasingTheKeepLockFreesItWhateverHoldsACopy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f, held, err := TryKeepLock(dir, os.O_CREATE)
	if err != nil || held {
		t.Fatalf("lock: %v, held %v", err, held)
	}
	forked, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Close(forked) }()
	if err := ReleaseKeepLock(f); err != nil {
		t.Fatal(err)
	}
	g, held, err := TryKeepLock(dir, 0)
	if err != nil || held {
		t.Fatalf("after the release: %v, held %v", err, held)
	}
	_ = ReleaseKeepLock(g)
}
