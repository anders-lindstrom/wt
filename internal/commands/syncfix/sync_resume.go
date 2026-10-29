// Package syncfix implements wt sync resume and wt sync undo: finishing a run
// a person took over, and putting back what a run did.
package syncfix

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/jsonout"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// ResumeOptions tunes SyncResume for callers and tests. Resume asks only when
// idle sessions are in the worktree.
type ResumeOptions struct {
	// Journal records what the resume does, for --json; nil records
	// nothing.
	Journal *syncrun.RunJournal
	syncrun.VerbOptions
	syncrun.PushOptions
}

// SyncResume continues the rebase a run left at a stop a person owned. It
// verifies the worktree is still what the run left, then drives the same
// loop to the end: the strategies at any later stop, the deferred steps, the
// result ref, the push. A later stop a person owns is handed over again,
// with a fresh plan file.
//
// A rebase somebody finished themselves with `git rebase --continue` is
// tolerated: there is nothing to continue, so only what follows the rebase
// runs — after checking that it really finished rather than being aborted.
// Nothing here ever resets the worktree: a person's own resolution is in it.
//
// Every refusal comes before the lock is taken over. Taking it and then
// refusing would release the lock the handover kept, so a refusal would not
// change nothing after all.
func SyncResume(ctx *kit.Context, work string, opts ResumeOptions, w io.Writer) error {
	return syncrun.Journaled(opts.Journal, func() error { return syncResume(ctx, work, opts, w) })
}

func syncResume(ctx *kit.Context, work string, opts ResumeOptions, w io.Writer) (err error) {
	r := &resume{ctx: ctx, opts: opts, w: w, j: opts.Journal, tracker: &syncrun.RebaseTracker{}}
	defer syncrun.WatchSignals(w, r.tracker)()

	if err := r.readHandover(work); err != nil {
		return err
	}
	r.joinJournal()
	// Whatever returns an error before the rebase is touched refused it;
	// every other end says what it came to on the way out.
	defer func() { r.recordEnd(err) }()
	if err := r.checkSafety(); err != nil {
		return err
	}
	if ok, err := r.askSessions(); err != nil || !ok {
		return err
	}
	if err := r.verify(); err != nil {
		return err
	}
	// Released however the resume ends, the refusal after the take-over
	// included, unless a fresh handover keeps it.
	defer func() {
		if r.lock != nil {
			_ = r.lock.Release()
		}
	}()
	if err := r.takeLock(); err != nil {
		return err
	}
	res, err := r.rebase()
	if err != nil {
		return err
	}
	if res.Left != nil {
		return r.handOverAgain(res)
	}
	if err := r.complete(res); err != nil {
		return err
	}
	return r.push()
}

// resume is one wt sync resume under way: the handover it continues, and
// what each step learns for the steps after it.
type resume struct {
	ctx     *kit.Context
	opts    ResumeOptions
	w       io.Writer
	j       *syncrun.RunJournal
	tracker *syncrun.RebaseTracker

	target repo.Worktree
	gitDir string
	st     wtsync.State
	// name is the work as the run recorded it.
	name string
	// pinned is the tip the safety ref pins.
	pinned   string
	sessions agents.Sessions
	// landed is how many commits trunk gained since the run, counted for
	// an idle session's notice.
	landed int
	cfg    *wtsync.Config
	// busy is the rebase still in progress, rather than finished by hand.
	busy bool
	// lock is the handover's lock, taken over; nil once a fresh handover
	// keeps it.
	lock *wtsync.Lock
}

// joinJournal records the run's repository and trunk, and the branch as
// resume finds it: the run's old tip while the rebase waits, the rebased
// tip when a person finished it by hand.
func (r *resume) joinJournal() {
	st := r.st
	r.j.Repo(r.ctx.Repo.MainRoot)
	r.j.Trunk(strings.TrimPrefix(st.TrunkRef, "origin/"), nil, st.TrunkRef, st.Trunk, false)
	// The journal's record: a tip git cannot read is recorded as none.
	found, _, _ := r.ctx.Repo.ResolveRef("refs/heads/" + st.Branch)
	r.j.Join(r.name, st.Branch, r.target.Path, found, nil)
	r.j.SetSync(st.Branch, func(p *syncrun.SyncParticipant) {
		p.SafetyRef, p.PlanFile = jsonout.Strp(st.Safety), syncrun.PlanFileOf(r.target.Path)
	})
}

// recordEnd records where the branch is as the resume ends, and a resume
// that ended in err before anything else was recorded as refused.
func (r *resume) recordEnd(err error) {
	after, _, _ := r.ctx.Repo.ResolveRef("refs/heads/" + r.st.Branch) // recorded as none when unreadable
	r.j.SetSync(r.st.Branch, func(p *syncrun.SyncParticipant) {
		p.After = jsonout.Strp(after)
		if p.Result == syncrun.ResultNotRun && err != nil {
			p.Result, p.Reason = syncrun.ResultRefused, jsonout.Strp(err.Error())
		}
	})
}

// readHandover finds work's worktree and the handover the run left in it.
func (r *resume) readHandover(work string) error {
	target, err := kit.LocateBranch(r.ctx, work)
	if err != nil {
		return err
	}
	r.target = target
	if r.gitDir, err = wtsync.GitDir(target.Path); err != nil {
		return err
	}
	st, ok, err := wtsync.ReadState(r.gitDir)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s was not left mid-rebase by wt sync rebase: nothing to resume", work)
	}
	if st.Branch != target.Branch {
		return fmt.Errorf("the handover in %s is for %s, not %s; nothing is resumed", r.gitDir, st.Branch, target.Branch)
	}
	// One name from here on: the one the run recorded, so every message and
	// a fresh handover say what the run said, whichever form was typed. An
	// old sidecar without one falls back to the argument, never the branch.
	st.Work = cmp.Or(st.Work, work)
	r.st, r.name = st, st.Work
	return nil
}

// checkSafety checks the handover's safety ref is the run's and pins the
// tip the handover says.
func (r *resume) checkSafety() error {
	st := r.st
	// The sidecar must describe the run it claims to: a safety ref built
	// from a different branch or epoch is somebody else's, and pinning the
	// wrong old tip would make undo restore to the wrong commit.
	if want := wtsync.SafetyRef(st.Branch, st.Epoch); st.Safety != want {
		return fmt.Errorf("the handover names %s, not %s; nothing is resumed", st.Safety, want)
	}
	tip, err := git.Run(r.ctx.Repo.MainRoot, "rev-parse", "--verify", st.Safety)
	if err != nil {
		return fmt.Errorf("the safety ref %s is gone; nothing is resumed", st.Safety)
	}
	// What the safety ref pins: the tip the rebase started from, or the one
	// the run found before it fast-forwarded the branch to its own remote.
	r.pinned = cmp.Or(st.Found, st.OldTip)
	if tip != r.pinned {
		return fmt.Errorf("%s pins %s but the handover says %s; nothing is resumed", st.Safety, git.ShortID(tip, 7), git.ShortID(r.pinned, 7))
	}
	return nil
}

// askSessions refuses a busy session in the worktree and asks about an idle
// one. ok is false when the answer was no, which the journal records.
func (r *resume) askSessions() (ok bool, err error) {
	ctx, st, name := r.ctx, r.st, r.name
	listed, err := r.opts.SessionsNow(ctx, syncrun.ResumedNothing)
	if err != nil {
		return false, err
	}
	r.sessions = agents.SessionsAt(listed, r.target.Path)
	if len(r.sessions.Busy()) > 0 {
		return false, fmt.Errorf("an agent session is busy in %s: %s; nothing is resumed", name, r.sessions.Label(kit.SessionLabel))
	}
	// An idle session is named and asked about before the handover is
	// verified: a person can go on editing while the question waits, so the
	// verification has to see what they left after answering.
	if len(r.sessions) == 0 {
		return true, nil
	}
	behind, _, err := wtsync.BehindAhead(ctx.Repo.MainRoot, st.Trunk, st.OldTip)
	if err != nil {
		return false, fmt.Errorf("%s: counting what landed: %w; nothing is resumed", name, err)
	}
	r.landed = behind
	fmt.Fprintln(r.w, syncrun.IdleNotice(name, r.sessions))
	told := []syncrun.Idle{{Label: name, Path: r.target.Path, Sessions: r.sessions}}
	ok, _, err = syncrun.AskIdle(ctx, r.w, r.opts.VerbOptions, []string{name}, told, listed, syncrun.ResumedNothing)
	if err != nil {
		return false, err
	}
	if !ok {
		r.j.Set(st.Branch, func(p *syncrun.UpParticipant) {
			p.Result, p.Reason = syncrun.ResultRefused, jsonout.Strp("not confirmed: "+syncrun.ResumedNothing.Line)
		})
		r.j.FailWith(errors.New("not confirmed: " + syncrun.ResumedNothing.Line))
	}
	return ok, nil
}

// verify loads the run's declaration and checks the worktree is what the
// run left: the rebase it handed over, or that rebase finished by hand.
func (r *resume) verify() error {
	st, name := r.st, r.name
	// The run's trunk, not today's: a resume that read a newer declaration
	// would apply strategies the stopped rebase was never planned with.
	cfg, err := wtsync.LoadFromRef(r.ctx.Repo.MainRoot, st.Trunk)
	if err != nil {
		return err
	}
	r.cfg = cfg
	if r.busy, err = wtsync.RebaseInProgress(r.target.Path); err != nil {
		return err
	}
	if r.busy {
		if err := wtsync.VerifyLeft(r.target.Path, st); err != nil {
			return fmt.Errorf("%s: %w; nothing is resumed", name, err)
		}
		return verifyHandover(r.target.Path, st, r.w)
	}
	if err := wtsync.VerifyFinished(r.target.Path, st.Branch, name, st.Onto, st.OldTip, st.Total); err != nil {
		return fmt.Errorf("%s: %w; nothing is resumed", name, err)
	}
	fmt.Fprintf(r.w, "%s  the rebase is already finished; running what is left\n", name)
	if len(st.Resolved)+len(st.Deleted) > 0 {
		noteNotRechecked(r.w, fmt.Sprintf("it was finished by hand from the %d/%d the plan describes", st.Stop, st.Total), st)
	}
	return nil
}

// takeLock takes over the lock the handover kept, and checks the handover
// did not change while the checks ran without it.
func (r *resume) takeLock() error {
	lock, err := wtsync.TakeOver(r.gitDir, r.opts.Clock(), r.st.Lock)
	if err != nil {
		return fmt.Errorf("%s: %w; nothing is resumed", r.name, err)
	}
	r.lock = lock
	// The checks ran without the lock. Another wt that ended this run in
	// between had to hold it to do so, so with the lock in hand the handover
	// is either still the one they read or gone.
	if cur, ok, err := wtsync.ReadState(r.gitDir); err != nil {
		return err
	} else if !ok || cur.Epoch != r.st.Epoch {
		return fmt.Errorf("the handover in %s changed while resume was checking it; nothing is resumed", r.name)
	}
	return nil
}

// rebase drives the rebase on from where the handover left it.
func (r *resume) rebase() (wtsync.Result, error) {
	st, name := r.st, r.name
	// Stacked is deliberately not set. It exists so a run does not leave a
	// branch its children are about to be rebased onto waiting on a person;
	// a resume rebases exactly one worktree, so there is no child in flight
	// to strand, and a later unclaimed stop is better answered with a fresh
	// plan file than with a refusal whose only remedy is undo.
	req := wtsync.Request{
		Path: r.target.Path, Branch: st.Branch, Trunk: st.Trunk,
		Onto: st.Onto, Upstream: st.Upstream, Epoch: st.Epoch, Work: name, Total: st.Total,
	}
	safety := wtsync.Safety{Branch: st.Branch, Epoch: st.Epoch, Ref: st.Safety, Tip: r.pinned}
	if r.busy {
		fmt.Fprintf(r.w, "%s  %s  resuming at %d/%d\n", name, st.Branch, st.Stop, st.Total)
	}
	// Resuming, not rebasing: an interrupt must point back at resume, since
	// the abort that is right for a run would discard a person's resolution.
	res, rerr := syncrun.TrackedRebase(r.tracker, &syncrun.RebaseInFlight{Work: name, Path: r.target.Path, Safety: st.Safety, Resuming: true}, func() (wtsync.Result, error) {
		return wtsync.Resume(r.ctx.Repo.MainRoot, r.cfg, req, st.OldTip, safety, r.w)
	})
	if rerr != nil {
		recordResumeFailed(r.j, st, r.target.Path, rerr)
		return res, fmt.Errorf("%s: %w", name, rerr)
	}
	return res, nil
}

// handOverAgain hands a rebase that stopped again at a stop a person owns
// back to them, with a fresh plan file, keeping the lock.
func (r *resume) handOverAgain(res wtsync.Result) error {
	st, name, w := r.st, r.name, r.w
	fmt.Fprintf(w, "  ⚠ stopped again at %d/%d\n", res.Left.Index, res.Left.Total)
	herr := syncrun.HandOver(r.ctx, w, syncrun.HandoverInput{
		Work: name, Branch: st.Branch, Path: r.target.Path, TrunkRef: st.TrunkRef, TrunkSHA: st.Trunk,
		Onto: st.Onto, Upstream: st.Upstream, Epoch: st.Epoch, Cfg: r.cfg, Res: res, Lock: r.lock,
		Found: st.Found, Earlier: st.Stopped, Tracker: r.tracker,
	})
	if herr != nil {
		fmt.Fprintf(w, "  ✗ failed: %v\n", herr)
		// As in run: a brief and a sidecar that may now describe different
		// stops both go, and the person is told where the worktree is. The
		// branch ref has not moved since the run, so the abort is the whole
		// of putting it back; undo refuses a mid-rebase worktree.
		syncrun.ClearHandover(w, r.target.Path)
		way := wtsync.WayOut(wtsync.Way{Work: name, Path: r.target.Path, Rebasing: true})
		fmt.Fprintf(w, "  ⚠ %s is left mid-rebase with no plan: %s\n", name, way)
		r.j.SetSync(st.Branch, func(p *syncrun.SyncParticipant) {
			p.Result, p.Reason, p.Recovery, p.PlanFile = syncrun.ResultNeedsRecovery, jsonout.Strp(herr.Error()), jsonout.Strp(way), nil
		})
		return fmt.Errorf("not completed: %s (failed)", name)
	}
	r.lock = nil // kept on purpose
	r.j.SetSync(st.Branch, func(p *syncrun.SyncParticipant) {
		p.Result, p.Reason = syncrun.ResultHandedOver, jsonout.Strp("stopped again at a conflict that is yours")
		p.Recovery = jsonout.Strp(wtsync.WayOut(wtsync.Way{Work: name, Plan: true, Rebasing: true, OwesAdd: true}))
		p.UndoCommand = syncrun.UndoCommand(name)
	})
	return fmt.Errorf("not completed: %s (needs you)", name)
}

// complete runs what follows a finished rebase, as a run does: the deferred
// steps, the result ref, the handover's removal.
func (r *resume) complete(res wtsync.Result) error {
	st, name := r.st, r.name
	fmt.Fprintf(r.w, "  ✓ rebased %s\n", kit.Count(res.Replayed, "commit"))
	_, ran, cerr := syncrun.CompleteRun(r.ctx, r.w, r.cfg, r.tracker, name, r.target.Path, func() syncrun.CompleteInput {
		// As a run does: the tip the run found is what the deferred steps
		// compare with and what the undo line names.
		done := res
		done.OldTip = r.pinned
		return syncrun.CompleteInput{
			Branch: st.Branch, Epoch: st.Epoch, Res: done,
			Tell: r.sessions, TrunkName: strings.TrimPrefix(st.TrunkRef, "origin/"), Landed: r.landed,
			Check: syncrun.PathsOnce(st.Stopped, st.Left, st.ResolvedPaths(), st.Deleted, wtsync.StopPaths(res.Stops)),
		}
	})
	owed := syncrun.OwedSteps(ran)
	r.j.SetSync(st.Branch, func(p *syncrun.SyncParticipant) {
		p.Result, p.PlanFile, p.UndoCommand, p.Deferred = syncrun.ResultRebased, nil, syncrun.UndoCommand(name), syncrun.DeferredSteps(ran)
		switch {
		case cerr != nil:
			p.Result, p.FailedSteps = syncrun.ResultRebasedStepFailed, append(p.FailedSteps, "finish: "+cerr.Error())
		case len(owed) > 0:
			p.Result, p.FailedSteps = syncrun.ResultRebasedStepFailed, append(p.FailedSteps, owed...)
		}
	})
	if cerr != nil {
		return cerr
	}
	if len(owed) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(syncrun.OwedBy(name, owed), ", "))
	}
	return nil
}

// push offers the rebased branch's push, as a run does.
func (r *resume) push() error {
	ctx, st := r.ctx, r.st
	t := syncrun.PushTarget{Work: r.name, Branch: st.Branch, Path: r.target.Path}
	r.j.Set(st.Branch, func(p *syncrun.UpParticipant) {
		p.PushCommand, p.NoPushReason, p.FixCommand = syncrun.PushJSON(t, syncrun.PushOf(ctx, t))
	})
	pushed, failed, err := syncrun.OfferPush(ctx, r.w, r.opts.Push, r.opts.PushOptions, []syncrun.PushTarget{t})
	if err != nil {
		return err
	}
	r.j.Set(st.Branch, func(p *syncrun.UpParticipant) {
		// An answer to the push question may have given it a destination.
		if p.NoPushReason != nil {
			p.PushCommand, p.NoPushReason, p.FixCommand = syncrun.PushJSON(t, syncrun.PushOf(ctx, t))
		}
		p.Pushed = len(pushed) > 0
	})
	if len(failed) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// recordResumeFailed records a resume whose rebase failed, by what it left:
// the handover still standing, which resume or undo takes on from, or a
// worktree that needs putting back by hand.
func recordResumeFailed(j *syncrun.RunJournal, st wtsync.State, path string, rerr error) {
	busy, _ := wtsync.RebaseInProgress(path)
	gitDir, _ := wtsync.GitDir(path)
	_, planned, _ := wtsync.ReadState(gitDir)
	j.SetSync(st.Branch, func(p *syncrun.SyncParticipant) {
		p.Reason = jsonout.Strp(rerr.Error())
		if busy && planned {
			p.Result, p.UndoCommand = syncrun.ResultHandedOver, syncrun.UndoCommand(st.Work)
			p.Recovery = jsonout.Strp(wtsync.WayOut(wtsync.Way{Work: st.Work, Plan: true, Rebasing: true}))
			return
		}
		p.Result, p.PlanFile = syncrun.ResultNeedsRecovery, nil
		p.Recovery = jsonout.Strp(wtsync.WayOut(wtsync.Way{Work: st.Work, Path: path, Rebasing: busy, Safety: st.Safety}))
	})
}

// verifyHandover refuses to continue a rebase that is not what the run left.
// None of what it finds is something the tool may repair by re-running a
// strategy over the recorded stop: that would overwrite a person's work.
//
//   - A tracked file changed but not staged makes git refuse to continue,
//     and the loop's did-not-advance guard would read that refusal as a
//     stuck rebase. In a run that means a restore; here it would mean
//     throwing away the resolution. This is refused wherever the rebase is.
//   - At the recorded stop, something still unmerged means the person is not
//     done — unless a strategy resolved it, in which case merging it is not
//     the person's to do and they are pointed at undo instead.
//   - At the recorded stop, a different blob under a strategy's name means a
//     file the brief said never to hand-merge was hand-merged.
//
// Past the recorded stop, reached by a person's own git rebase --continue,
// whatever is unmerged belongs to a stop the strategies have never seen. The
// loop gives it to them exactly as the run would have and hands over afresh
// what they do not claim; telling the person to resolve it would invite a
// hand-merge of a file a strategy owns. The recorded stop's blobs are
// committed by then, so they are not compared, and that is said out loud.
func verifyHandover(wtPath string, st wtsync.State, w io.Writer) error {
	// --untracked-files=no: an untracked file never blocks a rebase, and a
	// script may have left one.
	dirty, err := git.Run(wtPath, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	var unstaged []string
	for _, line := range strings.Split(dirty, "\n") {
		// The second status column is the worktree against the index: any
		// mark there is a change git will refuse to continue over. An
		// unmerged path marks both columns and is not a change of that kind.
		if len(line) > 3 && line[1] != ' ' && !unmergedStatus(line[:2]) {
			unstaged = append(unstaged, strings.TrimSpace(line[3:]))
		}
	}
	add := wtsync.WayOut(wtsync.Way{Work: st.Work, Plan: true, Rebasing: true, OwesAdd: true})
	undo := wtsync.WayOut(wtsync.Way{Work: st.Work, Plan: true, Rebasing: true, Restart: true})
	if len(unstaged) > 0 {
		return fmt.Errorf("changed but not staged: %s; git refuses to continue over that, so %s", strings.Join(unstaged, ", "), add)
	}
	p, err := wtsync.RebaseProgress(wtPath)
	if err != nil {
		return err
	}
	if p.Index != st.Stop {
		noteNotRechecked(w, fmt.Sprintf("the rebase is at %d/%d, not the %d/%d the plan describes", p.Index, p.Total, st.Stop, st.Total), st)
		return nil
	}
	unmerged, err := wtsync.StagedConflicts(wtPath)
	if err != nil {
		return err
	}
	if len(unmerged) > 0 {
		var owned, yours []string
		for _, c := range unmerged {
			if s := st.Strategy[c.Path]; s != "" {
				owned = append(owned, c.Path+" ("+s+")")
			} else {
				yours = append(yours, c.Path)
			}
		}
		if len(owned) > 0 {
			return fmt.Errorf("unmerged again after a strategy resolved it: %s; that is not yours to merge: %s", strings.Join(owned, ", "), undo)
		}
		return fmt.Errorf("still unmerged: %s; %s", strings.Join(yours, ", "), add)
	}
	var changed []string
	for path := range st.Resolved {
		cur, err := git.Run(wtPath, "rev-parse", "--verify", ":0:"+path)
		if err != nil {
			return fmt.Errorf("%s is no longer staged and %s owns it: %s", path, st.Strategy[path], undo)
		}
		if cur != st.Resolved[path] {
			changed = append(changed, path)
		}
	}
	for _, path := range st.Deleted {
		if _, err := git.Run(wtPath, "rev-parse", "--verify", ":0:"+path); err == nil {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	var named []string
	for _, p := range changed {
		fmt.Fprintf(w, "  %s was hand-merged; %s owns it\n", p, st.Strategy[p])
		if oid := st.Resolved[p]; oid != "" {
			fmt.Fprintf(w, "    put it back: git -C %s show %s > %s && git -C %s add -- %s\n",
				wtPath, oid, filepath.Join(wtPath, p), wtPath, p)
		} else {
			fmt.Fprintf(w, "    put it back: git -C %s rm --cached -- %s && rm %s\n", wtPath, p, filepath.Join(wtPath, p))
		}
		named = append(named, p+" ("+st.Strategy[p]+")")
	}
	if len(changed) > 0 {
		return fmt.Errorf("%s under \"never hand-merge here\" changed: %s; nothing is resumed",
			kit.Count(len(changed), "file"), strings.Join(named, ", "))
	}
	return nil
}

// unmergedStatus reports whether a porcelain v1 XY pair marks an unmerged
// path: DD, AU, UD, UA, DU, AA or UU.
func unmergedStatus(xy string) bool {
	return xy[0] == 'U' || xy[1] == 'U' || xy == "AA" || xy == "DD"
}

// noteNotRechecked says, where the recorded stop is already committed, which
// of the strategies' answers there resume can no longer compare.
func noteNotRechecked(w io.Writer, where string, st wtsync.State) {
	ps := append(st.ResolvedPaths(), st.Deleted...)
	sort.Strings(ps)
	line := "  note: " + where + "; what the strategies staged there is already committed and is not re-checked"
	for i, p := range ps {
		ps[i] = p + " (" + st.Strategy[p] + ")"
	}
	if len(ps) > 0 {
		line += ": " + strings.Join(ps, ", ")
	}
	fmt.Fprintln(w, line)
}
