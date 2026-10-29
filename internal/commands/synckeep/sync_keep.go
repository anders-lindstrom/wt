// Package synckeep implements wt sync keep: a launchd job that keeps trunk
// and the worktrees current, and its runs. The state file, lock and job they
// share with wt sync and its doctor are in keepstate.
package synckeep

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
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synckeep/keepstate"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/git"
)

// KeepOptions tunes Run for callers and tests.
type KeepOptions struct {
	// Every is the job's interval, for the next-run line. Zero keeps what
	// the state file records, or keepstate.KeepDefaultInterval.
	Every time.Duration
	// Push is syncrun.PushAlways or syncrun.PushNever: a keeper has nobody to ask.
	Push syncrun.PushMode
	// NoFFTrunk leaves local trunk where it is after the fetch, as the user
	// setting ff_trunk false does.
	NoFFTrunk bool
	syncrun.VerbOptions
}

// writeKeepState writes the state file whole, through a rename, so a reader
// never sees half of it.
func writeKeepState(path string, st keepstate.KeepState) error {
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

// acquireKeepLock takes the pass lock without waiting, creating the file
// on a first pass. held is true when another pass has it; the file is
// returned open, and closing it releases the lock.
func acquireKeepLock(gitDir string) (f *os.File, held bool, err error) {
	return keepstate.TryKeepLock(gitDir, os.O_CREATE)
}

// Run is one unattended pass of the keeper: fetch trunk, and when
// its tip has moved since the last pass, rebase every ready worktree the way
// wt sync --rebase --yes does, leaving alone any worktree with a session in it,
// and push what finished with nothing owed. Every pass is recorded in the
// log, as it happens, and in the state file. The error is what synccmd.SyncRebase's
// would be.
func Run(ctx *kit.Context, opts KeepOptions, w io.Writer) error {
	gitDir, logPath, statePath, err := keepstate.KeepPaths(ctx)
	if err != nil {
		return err
	}
	now := opts.Clock()
	st, _, err := keepstate.ReadKeepState(statePath)
	if err != nil {
		return err
	}
	lock, held, err := acquireKeepLock(gitDir)
	if err != nil {
		return err
	}
	if held {
		if st.PID == 0 {
			return fmt.Errorf("a keeper pass is already running (it holds %s); nothing rebased", filepath.Join(gitDir, keepstate.KeepLockName))
		}
		return fmt.Errorf("a keeper pass is already running (pid %d since %s); nothing rebased", st.PID, keepstate.KeepClock(st.Started, now))
	}
	defer lock.Close()
	pass := keepPass{ctx: ctx, opts: opts, w: w, now: now, logPath: logPath, prev: st.Unpushed}
	// Signals from here on: the fetch is a git of its own, and a Ctrl-C or
	// a bootout during it has to take it down too. The run watches again
	// inside this, and its watch is the one a signal finds while it lasts.
	defer syncrun.WatchSignals(w, nil)()
	if opts.Every > 0 {
		st.Interval = keepstate.FmtEvery(opts.Every)
	} else if st.Interval == "" {
		st.Interval = keepstate.FmtEvery(keepstate.KeepDefaultInterval)
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
	st.LastRun, st.NextRun = now, now.Add(st.IntervalOrDefault())
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
	ctx     *kit.Context
	opts    KeepOptions
	w       io.Writer
	now     time.Time
	logPath string

	onto, before, after string
	result              string
	log                 *os.File
	facts               []string // the record's one-line facts, written at the end
	// prev is what earlier passes left unpushed; unpushed is what this one
	// leaves. ranRun says the run itself happened, acted that the run or a
	// retried push did: a pass that found nothing to do has done nothing.
	prev, unpushed []keepstate.UnpushedTarget
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

// openLog rotates the log once it is past keepstate.KeepLogLimit (the file becomes
// .1, replacing the previous .1) and opens it for this pass.
func (p *keepPass) openLog() error {
	if info, err := os.Stat(p.logPath); err == nil && info.Size() > keepstate.KeepLogLimit {
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

// agents lists the sessions for the fast-forward of local trunk the way the
// run does: an unattended pass has to know, so no claude to ask is an error.
func (p *keepPass) agents() ([]agents.Agent, error) {
	if p.opts.Agents != nil {
		return p.opts.Agents, nil
	}
	return p.ctx.ListAgentsRequired()
}

// header writes the record's first line: the time, and trunk as the fetch
// found it.
func (p *keepPass) header(line string) {
	fmt.Fprintf(p.log, "%s  %s\n", p.now.Format("2006-01-02 15:04:05"), line)
}

func (p *keepPass) pass(recorded string) error {
	ctx := p.ctx
	p.onto = "origin/" + ctx.Config.MainBranch
	if _, before, err := synccmd.TrunkTip(ctx); err == nil {
		p.before = before
	}
	p.after = p.before
	// Trunk, and the own remote of every worktree's branch: a pass takes
	// every ready one.
	own, err := syncrun.FetchRemotes(ctx, kit.NetworkTimeout, syncrun.OwnCandidates(ctx, nil))
	if err != nil {
		p.header(p.onto)
		p.result = "failed: fetch: " + kit.FetchReason(err)
		return fmt.Errorf("fetch: %w", err)
	}
	_, after, err := synccmd.TrunkTip(ctx)
	if err != nil {
		p.header(p.onto)
		p.result = "failed: " + err.Error()
		return err
	}
	p.after = after
	// Local trunk follows every fetch, whether or not trunk moved since the
	// last pass: one that was held back then may go now.
	trunkLine := syncrun.SyncLocalTrunk(ctx, after, syncrun.TrunkSyncOptions{Apply: true,
		OptedOut: p.opts.NoFFTrunk || !ctx.UserConfig().FFTrunk,
		Agents:   p.agents}).Line()
	noteTrunk := func() {
		if trunkLine != "" {
			fmt.Fprintln(p.w, trunkLine)
			fmt.Fprintf(p.log, "  %s\n", trunkLine)
		}
	}
	retry := stillUnpushed(ctx.Repo.MainRoot, p.prev)
	if after == recorded && len(retry) == 0 {
		p.result = "trunk unchanged"
		p.header(fmt.Sprintf("%s %s unchanged; nothing to do", p.onto, git.ShortID(after, 7)))
		fmt.Fprintf(p.w, "wt sync keep once  %s %s unchanged; nothing to do\n", p.onto, git.ShortID(after, 7))
		noteTrunk()
		return nil
	}
	p.acted = true
	// Everything the run and the pushes print goes to the log as it is
	// printed, indented under the header, so an interrupt leaves what
	// happened up to it and the way back on disk.
	w := io.MultiWriter(p.w, &indentWriter{w: p.log, prefix: "    ", atStart: true})
	defer syncrun.WatchSignals(w, nil)()
	r := &synccmd.RunPlan{}
	var rerr error
	if after == recorded {
		p.header(fmt.Sprintf("%s %s unchanged", p.onto, git.ShortID(after, 7)))
		fmt.Fprintf(p.w, "wt sync keep once  %s %s unchanged; %s to retry\n", p.onto, git.ShortID(after, 7), kit.Count(len(retry), "push", "pushes"))
		noteTrunk()
	} else {
		p.header(fmt.Sprintf("%s %s → %s", p.onto, git.ShortID(p.before, 7), git.ShortID(after, 7)))
		fmt.Fprintf(p.w, "wt sync keep once  %s %s → %s (fetched)\n", p.onto, git.ShortID(p.before, 7), git.ShortID(after, 7))
		noteTrunk()
		ropts := synccmd.RunOptions{NoFetch: true, Unattended: true, Own: own, VerbOptions: p.opts.VerbOptions, PushOptions: syncrun.PushOptions{Push: p.opts.Push}}
		ropts.Confirm = nil
		// An unattended pass has to know who is in a worktree: no claude to
		// ask is not nobody there, it is not knowing, and nothing moves.
		if ropts.Agents == nil {
			listed, aerr := ctx.ListAgentsRequired()
			if aerr != nil {
				rerr = fmt.Errorf("cannot list agent sessions (%w); an unattended pass needs them, nothing rebased", aerr)
				fmt.Fprintln(w, rerr.Error())
			}
			ropts.Agents = listed
			if ropts.Relist == nil {
				ropts.Relist = ctx.ListAgentsRequired
			}
		}
		if rerr == nil {
			r, rerr = synccmd.RunSync(ctx, nil, ropts, w)
			p.ranRun = true
		}
	}
	// A branch this run rebased again is pushed as this run's, not as an
	// earlier pass's; the rest of what was left unpushed is offered again.
	retry = slices.DeleteFunc(retry, func(u keepstate.UnpushedTarget) bool {
		return slices.ContainsFunc(r.Pushable, func(t syncrun.PushTarget) bool { return t.Work == u.Work })
	})
	targets := make([]syncrun.PushTarget, len(retry))
	for i, u := range retry {
		targets[i] = u.Target()
	}
	retried, failed, perr := syncrun.OfferPush(ctx, w, p.opts.Push, syncrun.PushOptions{}, targets)
	if perr != nil {
		rerr = perr
	}
	// What is still unpushed after this pass, at the tip it sits at now.
	p.unpushed = nil
	for _, t := range r.Pushable {
		if !contains(r.Pushed, t.Work) {
			tip, _ := git.Run(t.Path, "rev-parse", "HEAD")
			p.unpushed = append(p.unpushed, keepstate.UnpushedTarget{Work: t.Work, Branch: t.Branch, Path: t.Path, Tip: tip})
		}
	}
	for _, u := range retry {
		if !contains(retried, u.Work) {
			p.unpushed = append(p.unpushed, u)
		}
	}
	// A push that did not come off is named with the command that does it
	// by hand, in the log too; --no-push has syncrun.OfferPush print it already.
	if p.opts.Push == syncrun.PushAlways {
		for _, u := range p.unpushed {
			// One with nowhere to push has been named with why already.
			if to := syncrun.PushOf(ctx, u.Target()); to.Remote != "" {
				fmt.Fprintln(w, syncrun.PushLine(u.Target(), to))
			}
		}
	}
	rerr = withPushFailures(rerr, failed)
	pushed := append(slices.Clone(r.Pushed), retried...)
	var unpushedWorks []string
	for _, u := range p.unpushed {
		unpushedWorks = append(unpushedWorks, u.Work)
	}
	p.facts = []string{
		"rebased  " + orNone(r.Rebased),
		"pushed   " + orNone(pushed),
		"left     " + orNone(r.Left),
		"unpushed " + orNone(unpushedWorks),
	}
	if len(r.Forwarded) > 0 {
		p.facts = append(p.facts, "fast-forwarded "+orNone(r.Forwarded))
	}
	switch {
	case rerr != nil:
		p.result = "failed: " + rerr.Error()
	case !p.ranRun:
		p.result = fmt.Sprintf("trunk unchanged, pushed %d", len(pushed))
	case len(r.Rebased) == 0:
		p.result = fmt.Sprintf("nothing to rebase, left %d", len(r.Left))
		if len(pushed) > 0 {
			p.result += fmt.Sprintf(", pushed %d", len(pushed))
		}
	default:
		p.result = fmt.Sprintf("rebased %d, pushed %d, left %d", len(r.Rebased), len(pushed), len(r.Left))
	}
	if rerr == nil && len(r.Forwarded) > 0 {
		p.result += fmt.Sprintf(", fast-forwarded %d", len(r.Forwarded))
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
func stillUnpushed(mainRoot string, prev []keepstate.UnpushedTarget) []keepstate.UnpushedTarget {
	var still []keepstate.UnpushedTarget
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

// Status prints whether a keeper is installed, its interval, what
// the last pass did and when the next one is.
func Status(ctx *kit.Context, now time.Time, w io.Writer) error {
	k, err := keepstate.Keeper(ctx, true)
	if err != nil {
		return err
	}
	switch {
	case !k.Launchd:
		fmt.Fprintln(w, "keeper   no launchd on this platform; run wt sync keep once from cron")
	case k.Installed && k.Loaded:
		fmt.Fprintf(w, "keeper   installed, every %s, loaded · %s\n", keepstate.FmtEvery(k.St.IntervalOrDefault()), k.PlistPath)
	case k.Installed:
		fmt.Fprintf(w, "keeper   installed but not loaded · %s · wt sync keep stop, then start\n", k.PlistPath)
	case k.Loaded:
		fmt.Fprintf(w, "keeper   loaded but its plist is gone · wt sync keep stop, then start\n")
	case k.St.Job != "":
		fmt.Fprintln(w, "keeper   stopped · wt sync keep start")
	default:
		fmt.Fprintln(w, "keeper   not installed · wt sync keep start")
	}
	st := k.St
	// The lock says whether a pass is running; the pid in the json is what
	// the running one, or the interrupted one, was.
	switch {
	case k.Running && st.PID != 0:
		fmt.Fprintf(w, "pass     running, pid %d since %s\n", st.PID, keepstate.KeepClock(st.Started, now))
	case k.Running:
		fmt.Fprintln(w, "pass     running")
	case st.PID != 0:
		fmt.Fprintf(w, "pass     interrupted at %s (pid %d); the next one goes ahead\n", keepstate.KeepClock(st.Started, now), st.PID)
	}
	if st.LastRun.IsZero() {
		fmt.Fprintln(w, "last     never")
	} else {
		fmt.Fprintf(w, "last     %s  %s\n", keepstate.KeepClock(st.LastRun, now), st.Result)
	}
	if st.Problem != "" {
		fmt.Fprintf(w, "problem  since %s: %s\n", keepstate.KeepClock(st.ProblemAt, now), st.Problem)
	}
	if works := st.UnpushedWorks(); works != "" {
		fmt.Fprintf(w, "unpushed %s\n", works)
	}
	if k.Installed && !st.NextRun.IsZero() {
		overdue := ""
		if now.After(st.NextRun.Add(st.IntervalOrDefault())) {
			overdue = " (overdue)"
		}
		fmt.Fprintf(w, "next     %s%s\n", keepstate.KeepClock(st.NextRun, now), overdue)
	}
	if k.Kept {
		fmt.Fprintf(w, "log      %s\n", k.LogPath)
	}
	return nil
}
