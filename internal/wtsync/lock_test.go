package wtsync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireWritesTheLockAndReleaseRemovesIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	l, err := Acquire(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if l.PID != os.Getpid() || !l.Started.Equal(now) || l.Path != filepath.Join(dir, LockName) {
		t.Fatalf("lock %+v", l)
	}
	got, ok, err := ReadLock(dir)
	if err != nil || !ok || got.PID != l.PID || !got.Started.Equal(now) {
		t.Fatalf("read %+v ok=%v err=%v", got, ok, err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadLock(dir); ok {
		t.Fatal("lock survived release")
	}
}

func TestAcquireRefusesALiveLockAndReplacesAnExpiredOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	first, err := Acquire(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(dir, now.Add(5*time.Minute))
	var held *LockHeld
	if !errors.As(err, &held) || held.PID != first.PID {
		t.Fatalf("second acquire: %v", err)
	}
	second, err := Acquire(dir, now.Add(LockExpiry+time.Second))
	if err != nil {
		t.Fatalf("expired lock was not replaced: %v", err)
	}
	// The displaced owner's Release must not remove the replacement.
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := ReadLock(dir); !ok || !got.Started.Equal(second.Started) {
		t.Fatalf("replacement lock gone or wrong: %+v ok=%v", got, ok)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadLock(dir); ok {
		t.Fatal("lock survived its owner's release")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, LockName+"*")); len(left) != 0 {
		t.Fatalf("temp or stale files left behind: %v", left)
	}
}

func TestAcquireDoesNotTakeOverAnUnreadableLock(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	l, err := Acquire(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(l.Path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(l.Path, 0o644)
	})
	_, err = Acquire(dir, now.Add(LockExpiry+time.Hour))
	if err == nil {
		t.Fatal("expected an error for an unreadable lock")
	}
	var held *LockHeld
	if errors.As(err, &held) {
		t.Fatalf("unreadable lock reported as LockHeld: %v", err)
	}
	if _, statErr := os.Stat(l.Path); statErr != nil {
		t.Fatalf("unreadable lock was removed: %v", statErr)
	}
}

func TestGitDirOfAWorktreeIsItsOwnDirectoryUnderTheMainGitDir(t *testing.T) {
	t.Parallel()
	dir := linearRepo(t, nil, []map[string]string{{"a.txt": "a2\n"}})
	wt := featureWorktree(t, dir)
	got, err := GitDir(wt.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ".git", "worktrees", filepath.Base(wt.Path))
	if got != want {
		t.Fatalf("git dir %s, want %s", got, want)
	}
}

// A real run passes time.Now(), whose nanoseconds the lock file cannot
// record. Release must still recognise its own lock.
func TestReleaseRemovesALockAcquiredWithSubSecondPrecision(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l, err := Acquire(dir, time.Unix(1_700_000_000, 123_456_789))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadLock(dir); ok {
		t.Fatal("lock survived release")
	}
}

func TestHeldLocksTracksWhatThisProcessHoldsUntilReleased(t *testing.T) {
	// Not parallel: counts the locks the whole test binary holds.
	dir := t.TempDir()
	before := len(HeldLocks())
	l, err := Acquire(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(HeldLocks()) != before+1 {
		t.Fatalf("Acquire did not register the lock: %d, was %d", len(HeldLocks()), before)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if len(HeldLocks()) != before {
		t.Fatalf("Release did not forget the lock: %d, want %d", len(HeldLocks()), before)
	}
}

func TestTakeOverReclaimsTheRunsOwnLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Now()
	l, err := Acquire(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	l.Keep() // the run left it behind on purpose

	got, err := TakeOver(dir, now, LeftLock{PID: l.PID, Started: l.Started.Unix()})
	if err != nil {
		t.Fatalf("TakeOver = %v, want the lock", err)
	}
	if err := got.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTakeOverRespectsSomebodyElsesLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Now()
	l, err := Acquire(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Release() }()

	// Both halves of the comparison have to hold. A different pid is the
	// obvious case; the same pid with a different start time is the one that
	// matters after a process id has been reused, or after the same process
	// acquired again.
	for _, prev := range []LeftLock{
		{PID: l.PID + 1, Started: l.Started.Unix()},
		{PID: l.PID, Started: l.Started.Unix() + 1},
		{PID: l.PID, Started: l.Started.Unix() - 1},
	} {
		if _, err := TakeOver(dir, now, prev); err == nil {
			t.Fatalf("TakeOver took a lock that is not the run's: %+v", prev)
		}
	}
}

// Keep is the whole protection, not a note in a doc comment: a caller that
// releases a kept lock — or a defer it did not write — must not delete the
// file the next run has to respect.
func TestAKeptLockSurvivesRelease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Now()
	l, err := Acquire(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	l.Keep()
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	cur, ok, err := ReadLock(dir)
	if err != nil || !ok {
		t.Fatalf("ReadLock = %v, %v; Release deleted a kept lock", ok, err)
	}
	if cur.PID != l.PID {
		t.Fatalf("lock %+v is not the one that was kept", cur)
	}
}

// Two contenders read the same expired lock. One renames it aside and links
// its fresh lock; the other, arriving at its own rename only then, must find
// that fresh lock and respect it, not move it aside and hold the worktree
// too. afterExpiredRead stands the first contender, another process, in
// between this one's read and its rename.
func TestAcquireNeverDisplacesAFreshLockAnotherContenderTookOverWith(t *testing.T) {
	// Not parallel: sets afterExpiredRead for the package.
	dir := t.TempDir()
	path := filepath.Join(dir, LockName)
	now := time.Unix(1_700_000_000, 0)
	writeLock := func(name string, pid int, start time.Time) {
		t.Helper()
		body := fmt.Sprintf("pid=%d\nstart=%d\nowner=other\n", pid, start.Unix())
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeLock(path, 4242, now.Add(-LockExpiry-time.Minute))
	const otherPID = 4343
	afterExpiredRead = func() {
		afterExpiredRead = nil
		// The other contender, as it went: the expired lock aside and gone,
		// its own linked into place.
		if err := os.Rename(path, path+".stale.4343"); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(path + ".stale.4343")
		writeLock(path+".4343", otherPID, now)
		if err := os.Link(path+".4343", path); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(path + ".4343")
	}
	t.Cleanup(func() { afterExpiredRead = nil })
	l, err := Acquire(dir, now)
	var busy *LockHeld
	if !errors.As(err, &busy) || busy.PID != otherPID {
		if l != nil {
			_ = l.Release()
		}
		t.Fatalf("Acquire = %+v, %v; want the other contender's fresh lock held", l, err)
	}
	if got, ok, _ := ReadLock(dir); !ok || got.PID != otherPID {
		t.Fatalf("the other contender's lock was displaced: %+v ok=%v", got, ok)
	}
}

// A removal can take the worktree's git dir, lock and all, before the run's
// deferred release: there is nothing left to release then.
func TestReleaseAfterTheGitDirIsGoneIsANoOp(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "gitdir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}
