package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The keeper's files, in the main checkout's git dir beside the worktrees'
// own state: the record of every pass, and what the last one came to.
const (
	KeepLogName   = "wt-sync-keep.log"
	KeepStateName = "wt-sync-keep.json"
	// keepLogLimit is the size past which the log is rotated: the file is
	// renamed to .1, replacing the previous .1, and a fresh one begun.
	keepLogLimit = 1 << 20
	// KeepDefaultInterval is how often a keeper runs unless told otherwise.
	KeepDefaultInterval = 30 * time.Minute
)

// keepState is wt-sync-keep.json: what the last pass came to, for the table
// line and the status, and the pass in progress, so two do not overlap.
type keepState struct {
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
	Unpushed []unpushedTarget `json:"unpushed,omitzero"`
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

// unpushedTarget is a pushTarget with the tip the pass left the branch at,
// which is what says whether it is still the pass's to push.
type unpushedTarget struct {
	Work   string `json:"worktree"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Tip    string `json:"tip"`
}

func (u unpushedTarget) target() pushTarget {
	return pushTarget{Work: u.Work, Branch: u.Branch, Path: u.Path}
}

// fmtEvery is an interval as a person writes it: 30m, 1h, 1h30m.
func fmtEvery(d time.Duration) string {
	s := d.Round(time.Second).String()
	// Only a whole zero component goes: 1m10s keeps its seconds.
	for _, zero := range []string{"m0s", "h0m"} {
		if strings.HasSuffix(s, zero) {
			s = strings.TrimSuffix(s, zero[1:])
		}
	}
	return s
}

// KeepOptions tunes SyncKeepRun for callers and tests.
type KeepOptions struct {
	// Every is the job's interval, for the next-run line. Zero keeps what
	// the state file records, or KeepDefaultInterval.
	Every time.Duration
	// Push is PushAlways or PushNever: a keeper has nobody to ask.
	Push PushMode
	verbOptions
}

// keepPaths is where the keeper keeps its files: the main checkout's git dir.
func keepPaths(ctx *Context) (gitDir, logPath, statePath string, err error) {
	gitDir, err = wtsync.GitDir(ctx.Repo.MainRoot)
	if err != nil {
		return "", "", "", err
	}
	return gitDir, filepath.Join(gitDir, KeepLogName), filepath.Join(gitDir, KeepStateName), nil
}

// readKeepState reads the state file; ok is false when there is none.
func readKeepState(path string) (st keepState, ok bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return keepState{}, false, nil
	}
	if err != nil {
		return keepState{}, false, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return keepState{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return st, true, nil
}

// writeKeepState writes the state file whole, through a rename, so a reader
// never sees half of it.
func writeKeepState(path string, st keepState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// unsignCommits appends commit.gpgsign=false to the GIT_CONFIG_* stack every
// git this process runs reads, on top of whatever a launcher put there. A
// pass makes commits, replayed and deferred, with nobody at the keyboard: a
// signer that asks (1Password's does) would hang a background job or fail
// it while the vault is locked.
func unsignCommits() {
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	_ = os.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", n), "commit.gpgsign")
	_ = os.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", n), "false")
	_ = os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n+1))
}

// interval is the state's interval as a duration, or the default.
func (st keepState) interval() time.Duration {
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

// acquireKeepLock takes the pass lock without waiting, creating the file
// on a first pass. held is true when another pass has it; the file is
// returned open, and closing it releases the lock.
func acquireKeepLock(gitDir string) (f *os.File, held bool, err error) {
	return tryKeepLock(gitDir, os.O_CREATE)
}

func tryKeepLock(gitDir string, flags int) (f *os.File, held bool, err error) {
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
	f, held, err := tryKeepLock(gitDir, 0)
	if err != nil {
		return false
	}
	if f != nil {
		_ = f.Close()
	}
	return held
}

// SyncKeepRun is one unattended pass of the keeper: fetch trunk, and when
// its tip has moved since the last pass, rebase every ready worktree the way
// wt sync --run --yes does, leaving alone any worktree with a session in it,
// and push what finished with nothing owed. Every pass is recorded in the
// log, as it happens, and in the state file. The error is what SyncRun's
// would be.
func SyncKeepRun(ctx *Context, opts KeepOptions, w io.Writer) error {
	gitDir, logPath, statePath, err := keepPaths(ctx)
	if err != nil {
		return err
	}
	now := opts.now()
	st, _, err := readKeepState(statePath)
	if err != nil {
		return err
	}
	lock, held, err := acquireKeepLock(gitDir)
	if err != nil {
		return err
	}
	if held {
		if st.PID == 0 {
			return fmt.Errorf("a keeper pass is already running (it holds %s); nothing rebased", filepath.Join(gitDir, KeepLockName))
		}
		return fmt.Errorf("a keeper pass is already running (pid %d since %s); nothing rebased", st.PID, keepClock(st.Started, now))
	}
	defer lock.Close()
	pass := keepPass{ctx: ctx, opts: opts, w: w, now: now, logPath: logPath, prev: st.Unpushed}
	// Signals from here on: the fetch is a git of its own, and a Ctrl-C or
	// a bootout during it has to take it down too. The run installs its own
	// handler for its length; pass.run swaps this one out around it.
	pass.unwatch = watchSignals(w, nil)
	defer func() { pass.unwatch() }()
	if opts.Every > 0 {
		st.Interval = fmtEvery(opts.Every)
	} else if st.Interval == "" {
		st.Interval = fmtEvery(KeepDefaultInterval)
	}
	// The pid is a diagnostic for the status; the lock is what keeps passes
	// apart.
	st.PID, st.Started = os.Getpid(), now
	if err := writeKeepState(statePath, st); err != nil {
		return err
	}
	unsignCommits()
	err = pass.run(st.TrunkSHA)
	st.Unpushed = pass.unpushed
	// Trunk is recorded only by a run that came off: a pass that failed on
	// something passing (a lock a person's run held, a fetch that timed out)
	// is tried again next interval, and trying again changes nothing a
	// finished rebase already settled. The problem goes only when a pass
	// ends with nothing owed and nothing left unpushed.
	switch {
	case err != nil:
		st.Problem, st.ProblemAt = pass.result, now
	case pass.ranRun:
		st.TrunkSHA = pass.after
	}
	if err == nil && pass.acted && len(pass.unpushed) == 0 {
		st.Problem, st.ProblemAt = "", time.Time{}
	}
	st.LastRun, st.NextRun = now, now.Add(st.interval())
	st.Result = pass.result
	st.PID, st.Started = 0, time.Time{}
	if werr := writeKeepState(statePath, st); werr != nil && err == nil {
		err = werr
	}
	return err
}

// keepPass is one pass as it happens: trunk before and after the fetch, what
// the run came to, and the log it writes as it goes.
type keepPass struct {
	ctx     *Context
	opts    KeepOptions
	w       io.Writer
	now     time.Time
	logPath string
	unwatch func()

	onto, before, after string
	result              string
	log                 *os.File
	facts               []string // the record's one-line facts, written at the end
	// prev is what earlier passes left unpushed; unpushed is what this one
	// leaves. ranRun says the run itself happened, acted that the run or a
	// retried push did: a pass that found nothing to do has done nothing.
	prev, unpushed []unpushedTarget
	ranRun, acted  bool
}

// run fetches, compares and, when trunk moved, runs; then it offers every
// push an earlier pass left unpushed again. It sets result whatever
// happens, and returns what the caller's exit code is. The log is written
// as the pass goes: the record's header once trunk is known, the run's own
// output indented under it as it is printed, the facts at the end. A log
// that cannot be written fails the pass before anything moves, and one that
// cannot be finished fails it after: the log is the promise a background
// job keeps.
func (p *keepPass) run(recorded string) error {
	if err := p.openLog(); err != nil {
		p.result = "failed: cannot write the log: " + err.Error()
		return fmt.Errorf("cannot write the log %s: %w; nothing rebased", p.logPath, err)
	}
	err := p.pass(recorded)
	if lerr := p.closeLog(err); lerr != nil && err == nil {
		p.result = "failed: log: " + lerr.Error()
		err = fmt.Errorf("log: %w", lerr)
	}
	return err
}

// openLog rotates the log once it is past keepLogLimit (the file becomes
// .1, replacing the previous .1) and opens it for this pass.
func (p *keepPass) openLog() error {
	if info, err := os.Stat(p.logPath); err == nil && info.Size() > keepLogLimit {
		if err := os.Rename(p.logPath, p.logPath+".1"); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(p.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	p.log = f
	return nil
}

// closeLog ends the record with its facts and closes the file, returning
// the first write that did not reach the disk.
func (p *keepPass) closeLog(err error) error {
	var lines []string
	if p.acted {
		lines = p.facts
	}
	lines = append(lines, "result   "+p.result)
	if !p.acted && err == nil && p.result == "trunk unchanged" {
		// The one-line record: the header said it all.
		lines = nil
	}
	var werr error
	for _, l := range lines {
		if _, e := fmt.Fprintf(p.log, "  %s\n", l); e != nil && werr == nil {
			werr = e
		}
	}
	if e := p.log.Close(); e != nil && werr == nil {
		werr = e
	}
	return werr
}

// header writes the record's first line: the time, and trunk as the fetch
// found it.
func (p *keepPass) header(line string) {
	fmt.Fprintf(p.log, "%s  %s\n", p.now.Format("2006-01-02 15:04:05"), line)
}

func (p *keepPass) pass(recorded string) error {
	ctx := p.ctx
	p.onto = "origin/" + ctx.Config.MainBranch
	if _, before, err := trunkTip(ctx); err == nil {
		p.before = before
	}
	p.after = p.before
	if _, err := git.RunTimeout(ctx.Repo.MainRoot, networkTimeout, "fetch", "--quiet", "origin", ctx.Config.MainBranch); err != nil {
		p.header(p.onto)
		p.result = "failed: fetch: " + fetchReason(err)
		return fmt.Errorf("fetch: %w", err)
	}
	_, after, err := trunkTip(ctx)
	if err != nil {
		p.header(p.onto)
		p.result = "failed: " + err.Error()
		return err
	}
	p.after = after
	retry := stillUnpushed(ctx.Repo.MainRoot, p.prev)
	if after == recorded && len(retry) == 0 {
		p.result = "trunk unchanged"
		p.header(fmt.Sprintf("%s %s unchanged; nothing to do", p.onto, git.ShortID(after, 7)))
		fmt.Fprintf(p.w, "wt sync keep run  %s %s unchanged; nothing to do\n", p.onto, git.ShortID(after, 7))
		return nil
	}
	p.acted = true
	// Everything the run and the pushes print goes to the log as it is
	// printed, indented under the header, so an interrupt leaves what
	// happened up to it and the way back on disk.
	w := io.MultiWriter(p.w, &indentWriter{w: p.log, prefix: "    ", atStart: true})
	r := &runPlan{}
	var rerr error
	if after == recorded {
		p.header(fmt.Sprintf("%s %s unchanged", p.onto, git.ShortID(after, 7)))
		fmt.Fprintf(p.w, "wt sync keep run  %s %s unchanged; %d push%s to retry\n", p.onto, git.ShortID(after, 7), len(retry), plural(len(retry)))
	} else {
		p.header(fmt.Sprintf("%s %s → %s", p.onto, git.ShortID(p.before, 7), git.ShortID(after, 7)))
		fmt.Fprintf(p.w, "wt sync keep run  %s %s → %s (fetched)\n", p.onto, git.ShortID(p.before, 7), git.ShortID(after, 7))
		ropts := RunOptions{NoFetch: true, Unattended: true, verbOptions: p.opts.verbOptions, pushOptions: pushOptions{Push: p.opts.Push}}
		ropts.Confirm = nil
		// An unattended pass has to know who is in a worktree: no claude to
		// ask is not nobody there, it is not knowing, and nothing moves.
		if ropts.Agents == nil {
			agents, aerr := wtsync.ListOtherAgentsRequired()
			if aerr != nil {
				rerr = fmt.Errorf("cannot list agent sessions (%v); an unattended pass needs them, nothing rebased", aerr)
				fmt.Fprintln(w, rerr.Error())
			}
			ropts.Agents = agents
			if ropts.Relist == nil {
				ropts.Relist = wtsync.ListOtherAgentsRequired
			}
		}
		if rerr == nil {
			p.unwatch()
			r, rerr = runSync(ctx, nil, ropts, w)
			p.unwatch = watchSignals(w, nil)
			p.ranRun = true
		}
	}
	// A branch this run rebased again is pushed as this run's, not as an
	// earlier pass's; the rest of what was left unpushed is offered again.
	retry = slices.DeleteFunc(retry, func(u unpushedTarget) bool {
		return slices.ContainsFunc(r.pushable, func(t pushTarget) bool { return t.Work == u.Work })
	})
	targets := make([]pushTarget, len(retry))
	for i, u := range retry {
		targets[i] = u.target()
	}
	retried, failed, perr := offerPush(w, p.opts.Push, nil, targets)
	if perr != nil {
		rerr = perr
	}
	// What is still unpushed after this pass, at the tip it sits at now.
	p.unpushed = nil
	for _, t := range r.pushable {
		if !contains(r.pushed, t.Work) {
			tip, _ := git.Run(t.Path, "rev-parse", "HEAD")
			p.unpushed = append(p.unpushed, unpushedTarget{Work: t.Work, Branch: t.Branch, Path: t.Path, Tip: tip})
		}
	}
	for _, u := range retry {
		if !contains(retried, u.Work) {
			p.unpushed = append(p.unpushed, u)
		}
	}
	// A push that did not come off is named with the command that does it
	// by hand, in the log too; --no-push has offerPush print it already.
	if p.opts.Push == PushAlways {
		for _, u := range p.unpushed {
			fmt.Fprintf(w, "push: git -C %s %s\n", u.Path, strings.Join(pushArgs(u.target()), " "))
		}
	}
	rerr = withPushFailures(rerr, failed)
	pushed := append(slices.Clone(r.pushed), retried...)
	var unpushedWorks []string
	for _, u := range p.unpushed {
		unpushedWorks = append(unpushedWorks, u.Work)
	}
	p.facts = []string{
		"rebased  " + orNone(r.rebased),
		"pushed   " + orNone(pushed),
		"left     " + orNone(r.left),
		"unpushed " + orNone(unpushedWorks),
	}
	switch {
	case rerr != nil:
		p.result = "failed: " + rerr.Error()
	case !p.ranRun:
		p.result = fmt.Sprintf("trunk unchanged, pushed %d", len(pushed))
	case len(r.rebased) == 0:
		p.result = fmt.Sprintf("nothing to rebase, left %d", len(r.left))
		if len(pushed) > 0 {
			p.result += fmt.Sprintf(", pushed %d", len(pushed))
		}
	default:
		p.result = fmt.Sprintf("rebased %d, pushed %d, left %d", len(r.rebased), len(pushed), len(r.left))
	}
	if rerr == nil && len(p.unpushed) > 0 {
		p.result += fmt.Sprintf(", unpushed %d", len(p.unpushed))
	}
	fmt.Fprintf(p.w, "kept: %s · log %s\n", p.result, p.logPath)
	return rerr
}

// indentWriter puts prefix at the start of every non-empty line it writes
// through, unbuffered, so what reaches it reaches the file at once.
type indentWriter struct {
	w       io.Writer
	prefix  string
	atStart bool
}

func (iw *indentWriter) Write(p []byte) (int, error) {
	var b bytes.Buffer
	for _, c := range p {
		if iw.atStart && c != '\n' {
			b.WriteString(iw.prefix)
		}
		b.WriteByte(c)
		iw.atStart = c == '\n'
	}
	if _, err := iw.w.Write(b.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}

// stillUnpushed is the part of prev that is still the keeper's to push:
// the branch is at the tip the pass left it at, and origin does not have
// that tip. A branch that moved, or a worktree that is gone, is somebody's
// now; one origin already holds was pushed by hand.
func stillUnpushed(mainRoot string, prev []unpushedTarget) []unpushedTarget {
	var still []unpushedTarget
	for _, u := range prev {
		head, err := git.Run(u.Path, "rev-parse", "--verify", "refs/heads/"+u.Branch)
		if err != nil || head != u.Tip {
			continue
		}
		if remote, err := git.Run(mainRoot, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+u.Branch); err == nil && remote == u.Tip {
			continue
		}
		still = append(still, u)
	}
	return still
}

// withPushFailures adds the retried pushes that failed to the run's error,
// in the not-completed form the run uses, so the exit code and the problem
// name them.
func withPushFailures(rerr error, failed []string) error {
	if len(failed) == 0 {
		return rerr
	}
	if rerr == nil {
		return errors.New("not completed: " + strings.Join(failed, ", "))
	}
	if rest, ok := strings.CutPrefix(rerr.Error(), "not completed: "); ok {
		return errors.New("not completed: " + rest + ", " + strings.Join(failed, ", "))
	}
	return fmt.Errorf("%w; not completed: %s", rerr, strings.Join(failed, ", "))
}

func orNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, " · ")
}

func contains(items []string, s string) bool {
	for _, i := range items {
		if i == s {
			return true
		}
	}
	return false
}

// keepClock is a time as the status and the table say it: the clock when it
// is today, the date too when it is not.
func keepClock(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

// keeperState is everything the table line, the status and the doctor row
// read: the state file, and whether a job is installed and loaded.
type keeperState struct {
	st        keepState
	kept      bool // the state file exists
	running   bool // a pass holds the lock now
	logPath   string
	plistPath string
	installed bool // the plist exists
	loaded    bool // launchd has it
	label     string
	launchd   bool // this platform has launchd
}

// keeper reads the keeper's state for ctx. loadedToo asks launchd whether
// the job is loaded, plist or no plist, which the table line skips: it is
// one more process on every wt sync.
func keeper(ctx *Context, loadedToo bool) (keeperState, error) {
	gitDir, logPath, statePath, err := keepPaths(ctx)
	if err != nil {
		return keeperState{}, err
	}
	k := keeperState{logPath: logPath, launchd: keepGOOS == "darwin", running: keepLockHeld(gitDir)}
	k.st, k.kept, err = readKeepState(statePath)
	if err != nil {
		return keeperState{}, err
	}
	if !k.launchd {
		return k, nil
	}
	job, err := keepJobFor(ctx)
	if err != nil {
		return keeperState{}, err
	}
	k.label, k.plistPath = job.Label, job.PlistPath
	if _, err := os.Stat(job.PlistPath); err == nil {
		k.installed = true
	}
	if loadedToo {
		k.loaded = launchctl.loaded(job.Label)
	}
	return k, nil
}

// unpushedWorks names what is left unpushed, comma-separated, "" for nothing.
func (st keepState) unpushedWorks() string {
	var works []string
	for _, u := range st.Unpushed {
		works = append(works, u.Work)
	}
	return strings.Join(works, ", ")
}

// problemSince is ", problem since HH:MM" for a keeper whose last failed
// pass no clean pass has followed, "" otherwise.
func (st keepState) problemSince(now time.Time) string {
	if st.Problem == "" {
		return ""
	}
	return ", problem since " + keepClock(st.ProblemAt, now)
}

// keptLine is the wt sync table's line for a kept repository, or "" when
// nothing has ever kept it.
func keptLine(ctx *Context, now time.Time) string {
	k, err := keeper(ctx, false)
	if err != nil || !k.kept {
		return ""
	}
	st := k.st
	tail := st.problemSince(now) + " · wt sync keep status"
	switch {
	case st.LastRun.IsZero() && k.installed:
		return "keeper installed, first pass at " + keepClock(st.NextRun, now) + tail
	case st.LastRun.IsZero():
		return ""
	case k.installed:
		return "kept at " + keepClock(st.LastRun, now) + ", next " + keepClock(st.NextRun, now) + tail
	case st.Job != "":
		return "kept at " + keepClock(st.LastRun, now) + ", keeper stopped" + tail
	}
	// Kept by hand, or from cron: no job to say when the next pass is.
	return "kept at " + keepClock(st.LastRun, now) + tail
}

// keeperCheck is the doctor's row: whether a keeper is installed, how its
// last pass went, and where the log is. A problem no clean pass has cleared
// is the one warning.
func keeperCheck(ctx *Context, now time.Time) wtsync.Check {
	k, err := keeper(ctx, true)
	if err != nil {
		return wtsync.Check{Name: "keeper", OK: false, Detail: err.Error()}
	}
	var parts []string
	switch {
	case k.installed && k.loaded:
		parts = append(parts, "installed, every "+fmtEvery(k.st.interval()))
	case k.installed:
		parts = append(parts, "installed but not loaded; wt sync keep stop, then start")
	case k.loaded:
		parts = append(parts, "loaded but its plist is gone; wt sync keep stop, then start")
	case k.st.Job != "":
		parts = append(parts, "stopped")
	case !k.launchd:
		parts = append(parts, "no launchd here; run wt sync keep run from cron")
	default:
		parts = append(parts, "not installed; wt sync keep start")
	}
	if !k.st.LastRun.IsZero() {
		parts = append(parts, "last pass "+keepClock(k.st.LastRun, now)+" "+k.st.Result)
	}
	if k.st.Problem != "" {
		parts = append(parts, "problem since "+keepClock(k.st.ProblemAt, now)+": "+k.st.Problem)
	}
	if works := k.st.unpushedWorks(); works != "" {
		parts = append(parts, "unpushed: "+works)
	}
	if k.kept {
		parts = append(parts, "log "+k.logPath)
	}
	return wtsync.Check{Name: "keeper", OK: k.st.Problem == "", Detail: strings.Join(parts, "; ")}
}

// SyncKeepStatus prints whether a keeper is installed, its interval, what
// the last pass did and when the next one is.
func SyncKeepStatus(ctx *Context, now time.Time, w io.Writer) error {
	k, err := keeper(ctx, true)
	if err != nil {
		return err
	}
	switch {
	case !k.launchd:
		fmt.Fprintln(w, "keeper   no launchd on this platform; run wt sync keep run from cron")
	case k.installed && k.loaded:
		fmt.Fprintf(w, "keeper   installed, every %s, loaded · %s\n", fmtEvery(k.st.interval()), k.plistPath)
	case k.installed:
		fmt.Fprintf(w, "keeper   installed but not loaded · %s · wt sync keep stop, then start\n", k.plistPath)
	case k.loaded:
		fmt.Fprintf(w, "keeper   loaded but its plist is gone · wt sync keep stop, then start\n")
	case k.st.Job != "":
		fmt.Fprintln(w, "keeper   stopped · wt sync keep start")
	default:
		fmt.Fprintln(w, "keeper   not installed · wt sync keep start")
	}
	st := k.st
	// The lock says whether a pass is running; the pid in the json is what
	// the running one, or the interrupted one, was.
	switch {
	case k.running && st.PID != 0:
		fmt.Fprintf(w, "pass     running, pid %d since %s\n", st.PID, keepClock(st.Started, now))
	case k.running:
		fmt.Fprintln(w, "pass     running")
	case st.PID != 0:
		fmt.Fprintf(w, "pass     interrupted at %s (pid %d); the next one goes ahead\n", keepClock(st.Started, now), st.PID)
	}
	if st.LastRun.IsZero() {
		fmt.Fprintln(w, "last     never")
	} else {
		fmt.Fprintf(w, "last     %s  %s\n", keepClock(st.LastRun, now), st.Result)
	}
	if st.Problem != "" {
		fmt.Fprintf(w, "problem  since %s: %s\n", keepClock(st.ProblemAt, now), st.Problem)
	}
	if works := st.unpushedWorks(); works != "" {
		fmt.Fprintf(w, "unpushed %s\n", works)
	}
	if k.installed && !st.NextRun.IsZero() {
		overdue := ""
		if now.After(st.NextRun.Add(st.interval())) {
			overdue = " (overdue)"
		}
		fmt.Fprintf(w, "next     %s%s\n", keepClock(st.NextRun, now), overdue)
	}
	if k.kept {
		fmt.Fprintf(w, "log      %s\n", k.logPath)
	}
	return nil
}
