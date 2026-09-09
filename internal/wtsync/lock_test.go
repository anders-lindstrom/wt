package wtsync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireWritesTheLockAndReleaseRemovesIt(t *testing.T) {
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

func TestGitDirOfAWorktreeIsItsOwnDirectoryUnderTheMainGitDir(t *testing.T) {
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
