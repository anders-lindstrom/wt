package keepstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The keeper's files, in the main checkout's git dir beside the worktrees'
// own state: the record of every pass, and what the last one came to.
const (
	KeepLogName   = "wt-sync-keep.log"
	KeepStateName = "wt-sync-keep.json"
	// KeepLogLimit is the size past which the log is rotated: the file is
	// renamed to .1, replacing the previous .1, and a fresh one begun.
	KeepLogLimit = 1 << 20
	// KeepDefaultInterval is how often a keeper runs unless told otherwise.
	KeepDefaultInterval = 30 * time.Minute
)

// KeepState is wt-sync-keep.json: what the last pass came to, for the table
// line and the status, and the pass in progress, so two do not overlap.
type KeepState struct {
	// TrunkSHA is the trunk tip the last completed pass saw; a pass that
	// finds trunk still there does nothing.
	TrunkSHA string    `json:"trunk_sha"`
	LastRun  time.Time `json:"last_run,omitzero"`
	NextRun  time.Time `json:"next_run,omitzero"`
	Interval string    `json:"interval"`
	Result   string    `json:"result"`
	// Problem is what the last pass that went wrong came to, and ProblemAt
	// when. A pass that finds trunk unchanged leaves them alone, so a refused
	// push is not forgotten one interval later; only a pass that ran and
	// finished clean clears them.
	Problem   string    `json:"problem,omitzero"`
	ProblemAt time.Time `json:"problem_at,omitzero"`
	// Unpushed is every worktree a pass rebased and did not push: the lease
	// refused, or --no-push left it to a person. Each is offered again every
	// pass until the push goes through, origin has that tip, or the branch
	// is not at it any more.
	Unpushed []UnpushedTarget `json:"unpushed,omitzero"`
	// Job is the launchd label start installed. It stays after stop: a
	// repository whose plist is gone was kept by a job once, and the table
	// says stopped rather than nothing.
	Job string `json:"job,omitzero"`
	// PID and Started name the pass running now; PID is 0 between passes.
	// A pass that died leaves them behind, so a pid is believed only while
	// the process is alive.
	PID     int       `json:"pid,omitzero"`
	Started time.Time `json:"started,omitzero"`
}

// UnpushedTarget is a PushTarget with the tip the pass left the branch at,
// which is what says whether it is still the pass's to push.
type UnpushedTarget struct {
	Work   string `json:"worktree"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Tip    string `json:"tip"`
}

// Target is the push target the work names.
func (u UnpushedTarget) Target() syncrun.PushTarget {
	return syncrun.PushTarget{Work: u.Work, Branch: u.Branch, Path: u.Path}
}

// FmtEvery is an interval as a person writes it: 30m, 1h, 1h30m.
func FmtEvery(d time.Duration) string {
	s := d.Round(time.Second).String()
	// Only a whole zero component goes: 1m10s keeps its seconds.
	for _, zero := range []string{"m0s", "h0m"} {
		if strings.HasSuffix(s, zero) {
			s = strings.TrimSuffix(s, zero[1:])
		}
	}
	return s
}

// KeepPaths is where the keeper keeps its files: the main checkout's git dir.
func KeepPaths(ctx *kit.Context) (gitDir, logPath, statePath string, err error) {
	gitDir, err = wtsync.GitDir(ctx.Repo.MainRoot)
	if err != nil {
		return "", "", "", err
	}
	return gitDir, filepath.Join(gitDir, KeepLogName), filepath.Join(gitDir, KeepStateName), nil
}

// ReadKeepState reads the state file; ok is false when there is none.
func ReadKeepState(path string) (st KeepState, ok bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return KeepState{}, false, nil
	}
	if err != nil {
		return KeepState{}, false, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return KeepState{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return st, true, nil
}

// IntervalOrDefault is the state's interval as a duration, or the default.
func (st KeepState) IntervalOrDefault() time.Duration {
	if d, err := time.ParseDuration(st.Interval); err == nil && d > 0 {
		return d
	}
	return KeepDefaultInterval
}

// KeepLockName is the file a pass holds, with an exclusive flock, for its
// whole length, state and log writes included, so two passes never
// interleave. The kernel drops it with the process, so an interrupted pass
// leaves nothing to expire.
const KeepLockName = "wt-sync-keep.lock"

// TryKeepLock opens the keeper's lock in gitDir and reports whether another
// process holds it.
func TryKeepLock(gitDir string, flags int) (f *os.File, held bool, err error) {
	f, err = os.OpenFile(filepath.Join(gitDir, KeepLockName), flags|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return f, false, nil
}

// keepLockHeld reports whether a pass holds the lock now. It creates
// nothing: a repository no pass has kept has no lock file, and a look at
// the table or the status must not leave one behind.
func keepLockHeld(gitDir string) bool {
	f, held, err := TryKeepLock(gitDir, 0)
	if err != nil {
		return false
	}
	if f != nil {
		_ = f.Close()
	}
	return held
}

// KeepClock is a time as the status and the table say it: the clock when it
// is today, the date too when it is not.
func KeepClock(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

// KeeperState is everything the table line, the status and the doctor row
// read: the state file, and whether a job is installed and loaded.
type KeeperState struct {
	St        KeepState
	Kept      bool // the state file exists
	Running   bool // a pass holds the lock now
	LogPath   string
	PlistPath string
	Installed bool // the plist exists
	Loaded    bool // launchd has it
	label     string
	Launchd   bool // this platform has launchd
}

// Keeper reads the keeper's state for ctx. loadedToo asks launchd whether
// the job is loaded, plist or no plist, which the table line skips: it is
// one more process on every wt sync.
func Keeper(ctx *kit.Context, loadedToo bool) (KeeperState, error) {
	gitDir, logPath, statePath, err := KeepPaths(ctx)
	if err != nil {
		return KeeperState{}, err
	}
	k := KeeperState{LogPath: logPath, Launchd: HasLaunchd(ctx.Machine), Running: keepLockHeld(gitDir)}
	k.St, k.Kept, err = ReadKeepState(statePath)
	if err != nil {
		return KeeperState{}, err
	}
	if !k.Launchd {
		return k, nil
	}
	job, err := KeepJobFor(ctx)
	if err != nil {
		return KeeperState{}, err
	}
	k.label, k.PlistPath = job.Label, job.PlistPath
	if _, err := os.Stat(job.PlistPath); err == nil {
		k.Installed = true
	}
	if loadedToo {
		k.Loaded = job.Launchctl.Loaded(job.Label)
	}
	return k, nil
}

// UnpushedWorks names what is left unpushed, comma-separated, "" for nothing.
func (st KeepState) UnpushedWorks() string {
	var works []string
	for _, u := range st.Unpushed {
		works = append(works, u.Work)
	}
	return strings.Join(works, ", ")
}

// problemSince is ", problem since HH:MM" for a keeper whose last failed
// pass no clean pass has followed, "" otherwise.
func (st KeepState) problemSince(now time.Time) string {
	if st.Problem == "" {
		return ""
	}
	return ", problem since " + KeepClock(st.ProblemAt, now)
}

// KeptLine is the wt sync table's line for a kept repository, or "" when
// nothing has ever kept it.
func KeptLine(ctx *kit.Context, now time.Time) string {
	k, err := Keeper(ctx, false)
	if err != nil || !k.Kept {
		return ""
	}
	st := k.St
	tail := st.problemSince(now) + " · wt sync keep status"
	switch {
	case st.LastRun.IsZero() && k.Installed:
		return "keeper installed, first pass at " + KeepClock(st.NextRun, now) + tail
	case st.LastRun.IsZero():
		return ""
	case k.Installed:
		return "kept at " + KeepClock(st.LastRun, now) + ", next " + KeepClock(st.NextRun, now) + tail
	case st.Job != "":
		return "kept at " + KeepClock(st.LastRun, now) + ", keeper stopped" + tail
	}
	// Kept by hand, or from cron: no job to say when the next pass is.
	return "kept at " + KeepClock(st.LastRun, now) + tail
}

// KeeperCheck is the doctor's row: whether a keeper is installed, how its
// last pass went, and where the log is. A problem no clean pass has cleared
// is the one warning.
func KeeperCheck(ctx *kit.Context, now time.Time) wtsync.Check {
	k, err := Keeper(ctx, true)
	if err != nil {
		return wtsync.Check{Name: "keeper", OK: false, Detail: err.Error()}
	}
	return k.Check(now)
}

// Check is the keeper as the doctor's row says it, and as the status across
// repositories says it on one line.
func (k KeeperState) Check(now time.Time) wtsync.Check {
	var parts []string
	switch {
	case k.Installed && k.Loaded:
		parts = append(parts, "installed, every "+FmtEvery(k.St.IntervalOrDefault()))
	case k.Installed:
		parts = append(parts, "installed but not loaded; wt sync keep stop, then start")
	case k.Loaded:
		parts = append(parts, "loaded but its plist is gone; wt sync keep stop, then start")
	case k.St.Job != "":
		parts = append(parts, "stopped")
	case !k.Launchd:
		parts = append(parts, "no launchd here; run wt sync keep once from cron")
	default:
		parts = append(parts, "not installed; wt sync keep start")
	}
	if !k.St.LastRun.IsZero() {
		parts = append(parts, "last pass "+KeepClock(k.St.LastRun, now)+" "+k.St.Result)
	}
	if k.St.Problem != "" {
		parts = append(parts, "problem since "+KeepClock(k.St.ProblemAt, now)+": "+k.St.Problem)
	}
	if works := k.St.UnpushedWorks(); works != "" {
		parts = append(parts, "unpushed: "+works)
	}
	if k.Kept {
		parts = append(parts, "log "+k.LogPath)
	}
	return wtsync.Check{Name: "keeper", OK: k.St.Problem == "", Detail: strings.Join(parts, "; ")}
}
