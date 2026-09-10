package wtsync

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LockName is the file a run holds in the worktree's own git dir
// (.git/worktrees/<name>/), so a second run, or the watcher, sees it.
const LockName = "wt-sync.lock"

// LockExpiry is how long a lock is believed. A run that died without
// releasing must not block its worktree forever.
const LockExpiry = 30 * time.Minute

// Lock is a held or observed lock.
type Lock struct {
	Path    string
	PID     int
	Started time.Time
	Owner   string
	// kept is set by Keep and read by Release, both under held's mutex. A
	// kept lock is the run's deliberate leftover, so Release must leave the
	// file alone however the caller reaches it — a deferred release two
	// call frames up included.
	kept bool
}

// held is every lock this process has acquired and not yet released. A
// signal handler runs on its own goroutine with no access to the caller's
// bookkeeping, so the locks are tracked here instead of being threaded
// through every caller down to it.
var held = struct {
	sync.Mutex
	locks map[string]*Lock
}{locks: map[string]*Lock{}}

// HeldLocks is every lock this process currently holds, in no order.
func HeldLocks() []*Lock {
	held.Lock()
	defer held.Unlock()
	out := make([]*Lock, 0, len(held.locks))
	for _, l := range held.locks {
		out = append(out, l)
	}
	return out
}

// LockHeld is the error for a live lock held by someone else.
type LockHeld struct{ Lock }

func (e *LockHeld) Error() string {
	return fmt.Sprintf("locked by pid %d (%s) since %s", e.PID, e.Owner, e.Started.Format(time.RFC3339))
}

// GitDir is the worktree's own git dir, absolute.
func GitDir(wtPath string) (string, error) {
	return gitEnv(wtPath, nil, nil, "rev-parse", "--absolute-git-dir")
}

// Acquire takes the lock atomically: the content is written to a private
// temp file and hard-linked into place, so a contender never sees a
// half-written lock. An existing lock younger than LockExpiry is respected;
// an older one is renamed aside (atomic, so only one contender wins) and
// removed.
func Acquire(gitDir string, now time.Time) (*Lock, error) {
	path := filepath.Join(gitDir, LockName)
	owner := "?"
	if u, err := user.Current(); err == nil {
		owner = u.Username
	}
	// Started is truncated to the second the file records. Release compares
	// it against what ReadLock parses back, so keeping the caller's
	// nanoseconds here would make every release a silent no-op and leave
	// the lock behind for LockExpiry.
	l := &Lock{Path: path, PID: os.Getpid(), Started: time.Unix(now.Unix(), 0), Owner: owner}
	tmp := fmt.Sprintf("%s.%d", path, l.PID)
	body := fmt.Sprintf("pid=%d\nstart=%d\nowner=%s\n", l.PID, now.Unix(), owner)
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return nil, err
	}
	defer os.Remove(tmp) //nolint:errcheck // best effort: the temp file is ours alone
	for attempt := 0; attempt < 2; attempt++ {
		err := os.Link(tmp, path)
		if err == nil {
			held.Lock()
			held.locks[path] = l
			held.Unlock()
			return l, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		existing, ok, rerr := ReadLock(gitDir)
		if rerr != nil {
			return nil, rerr
		}
		if ok && now.Sub(existing.Started) < LockExpiry {
			return nil, &LockHeld{*existing}
		}
		// Expired, or gone between the link and the read: take it over.
		// Rename is atomic, so of two contenders exactly one succeeds here;
		// the other retries and finds the winner's fresh lock.
		stale := fmt.Sprintf("%s.stale.%d", path, l.PID)
		if err := os.Rename(path, stale); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		_ = os.Remove(stale)
	}
	return nil, fmt.Errorf("could not acquire %s", path)
}

// Keep detaches the lock from this process without removing the file: a run
// that leaves a worktree mid-rebase leaves its lock behind, so a second run
// does not start where somebody has to finish first. It is not a forever
// lock — LockExpiry still frees it — and the sidecar, not this, is the
// durable marker that a run is waiting.
//
// A kept lock is also proof against Release: a caller that keeps a lock and
// then releases it, or that keeps one under a defer it did not write, must
// not silently delete the file the next run has to respect.
func (l *Lock) Keep() {
	held.Lock()
	delete(held.locks, l.Path)
	l.kept = true
	held.Unlock()
}

// TakeOver acquires the lock, displacing the one a run left behind when it
// handed a stop over. It tries an ordinary Acquire first and only displaces
// a lock whose pid and start time are exactly what the run recorded in its
// sidecar, so a live run, or any lock that is not this handover's, is
// respected the way Acquire respects it.
//
// The window Acquire's expiry path already has is not closed here: between
// reading the lock and renaming it aside, another process continuing the
// same handover could acquire, and would then be displaced. Two concurrent
// resumes of one worktree is a user error, and both would be driving the
// same rebase; nothing else can reach this path, because nothing else knows
// the recorded pid.
func TakeOver(gitDir string, now time.Time, prev LeftLock) (*Lock, error) {
	l, err := Acquire(gitDir, now)
	if err == nil {
		return l, nil
	}
	var busy *LockHeld
	if !errors.As(err, &busy) || prev.PID == 0 || busy.PID != prev.PID || busy.Started.Unix() != prev.Started {
		return nil, err
	}
	stale := fmt.Sprintf("%s.stale.%d", busy.Path, os.Getpid())
	if rerr := os.Rename(busy.Path, stale); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return nil, rerr
	}
	_ = os.Remove(stale)
	return Acquire(gitDir, now)
}

// Release removes the lock, but only while it is still this acquisition's: a
// displaced owner must not delete its replacement's lock. PID alone is not
// enough to tell the two apart when the same process re-acquires its own
// expired lock, so Started (set once, at Acquire) is compared too. A lock
// Keep was called on is never removed.
func (l *Lock) Release() error {
	held.Lock()
	if held.locks[l.Path] == l {
		delete(held.locks, l.Path)
	}
	kept := l.kept
	held.Unlock()
	if kept {
		return nil
	}
	cur, ok, err := ReadLock(filepath.Dir(l.Path))
	if err != nil {
		return err
	}
	if !ok || cur.PID != l.PID || !cur.Started.Equal(l.Started) {
		return nil
	}
	err = os.Remove(l.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ReadLock parses an existing lock file; ok is false when there is none.
func ReadLock(gitDir string) (*Lock, bool, error) {
	path := filepath.Join(gitDir, LockName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	l := &Lock{Path: path}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "pid":
			l.PID, _ = strconv.Atoi(v)
		case "start":
			sec, _ := strconv.ParseInt(v, 10, 64)
			l.Started = time.Unix(sec, 0)
		case "owner":
			l.Owner = v
		}
	}
	return l, true, nil
}
