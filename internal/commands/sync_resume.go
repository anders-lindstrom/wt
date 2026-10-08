package commands

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// ResumeOptions tunes SyncResume for callers and tests. Resume asks only when
// idle sessions are in the worktree.
type ResumeOptions struct {
	// Journal records what the resume does, for --json; nil records
	// nothing.
	Journal *RunJournal
	verbOptions
	pushOptions
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
func SyncResume(ctx *Context, work string, opts ResumeOptions, w io.Writer) error {
	return journaled(opts.Journal, func() error { return syncResume(ctx, work, opts, w) })
}

func syncResume(ctx *Context, work string, opts ResumeOptions, w io.Writer) (err error) {
	tracker := &rebaseTracker{}
	defer watchSignals(w, tracker)()

	target, err := locateBranch(ctx, work)
	if err != nil {
		return err
	}
	gitDir, err := wtsync.GitDir(target.Path)
	if err != nil {
		return err
	}
	st, ok, err := wtsync.ReadState(gitDir)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s was not left mid-rebase by wt sync rebase: nothing to resume", work)
	}
	if st.Branch != target.Branch {
		return fmt.Errorf("the handover in %s is for %s, not %s; nothing is resumed", gitDir, st.Branch, target.Branch)
	}
	// One name from here on: the one the run recorded, so every message and
	// a fresh handover say what the run said, whichever form was typed. An
	// old sidecar without one falls back to the argument, never the branch.
	st.Work = cmp.Or(st.Work, work)
	name := st.Work
	j := opts.Journal
	j.repo(ctx.Repo.MainRoot)
	j.trunk(strings.TrimPrefix(st.TrunkRef, "origin/"), nil, st.TrunkRef, st.Trunk, false)
	// The branch as resume finds it: the run's old tip while the rebase
	// waits, the rebased tip when a person finished it by hand.
	found, _ := ctx.Repo.ResolveRef("refs/heads/" + st.Branch)
	j.join(name, st.Branch, target.Path, found, nil)
	j.setSync(st.Branch, func(p *SyncParticipant) { p.SafetyRef, p.PlanFile = strp(st.Safety), planFileOf(target.Path) })
	// Whatever returns an error before the rebase is touched refused it;
	// every other end says what it came to on the way out.
	defer func() {
		after, _ := ctx.Repo.ResolveRef("refs/heads/" + st.Branch)
		j.setSync(st.Branch, func(p *SyncParticipant) {
			p.After = strp(after)
			if p.Result == ResultNotRun && err != nil {
				p.Result, p.Reason = ResultRefused, strp(err.Error())
			}
		})
	}()
	// The sidecar must describe the run it claims to: a safety ref built
	// from a different branch or epoch is somebody else's, and pinning the
	// wrong old tip would make undo restore to the wrong commit.
	if want := wtsync.SafetyRef(st.Branch, st.Epoch); st.Safety != want {
		return fmt.Errorf("the handover names %s, not %s; nothing is resumed", st.Safety, want)
	}
	tip, err := git.Run(ctx.Repo.MainRoot, "rev-parse", "--verify", st.Safety)
	if err != nil {
		return fmt.Errorf("the safety ref %s is gone; nothing is resumed", st.Safety)
	}
	// What the safety ref pins: the tip the rebase started from, or the one
	// the run found before it fast-forwarded the branch to its own remote.
	pinned := cmp.Or(st.Found, st.OldTip)
	if tip != pinned {
		return fmt.Errorf("%s pins %s but the handover says %s; nothing is resumed", st.Safety, git.ShortID(tip, 7), git.ShortID(pinned, 7))
	}
	agents, err := opts.agents(resumedNothing)
	if err != nil {
		return err
	}
	sessions := wtsync.SessionsAt(agents, target.Path)
	if len(sessions.Busy()) > 0 {
		return fmt.Errorf("an agent session is busy in %s: %s; nothing is resumed", name, sessions.Label(sessionLabel))
	}
	// An idle session is named and asked about before the handover is
	// verified: a person can go on editing while the question waits, so the
	// verification has to see what they left after answering.
	var landed int
	if len(sessions) > 0 {
		behind, _, err := wtsync.BehindAhead(ctx.Repo.MainRoot, st.Trunk, st.OldTip)
		if err != nil {
			return fmt.Errorf("%s: counting what landed: %w; nothing is resumed", name, err)
		}
		landed = behind
		fmt.Fprintln(w, idleNotice(name, sessions))
		told := []idle{{label: name, path: target.Path, sessions: sessions}}
		ok, _, err := askIdle(w, opts.verbOptions, []string{name}, told, agents, resumedNothing)
		if err != nil {
			return err
		}
		if !ok {
			j.set(st.Branch, func(p *UpParticipant) {
				p.Result, p.Reason = ResultRefused, strp("not confirmed: "+resumedNothing.line)
			})
			j.fail(errors.New("not confirmed: " + resumedNothing.line))
			return nil
		}
	}
	// The run's trunk, not today's: a resume that read a newer declaration
	// would apply strategies the stopped rebase was never planned with.
	cfg, err := wtsync.LoadFromRef(ctx.Repo.MainRoot, st.Trunk)
	if err != nil {
		return err
	}
	busy, err := wtsync.RebaseInProgress(target.Path)
	if err != nil {
		return err
	}
	if busy {
		if err := wtsync.VerifyLeft(target.Path, st); err != nil {
			return fmt.Errorf("%s: %w; nothing is resumed", name, err)
		}
		if err := verifyHandover(target.Path, st, w); err != nil {
			return err
		}
	} else {
		if err := wtsync.VerifyFinished(target.Path, st.Branch, name, st.Onto, st.OldTip, st.Total); err != nil {
			return fmt.Errorf("%s: %w; nothing is resumed", name, err)
		}
		fmt.Fprintf(w, "%s  the rebase is already finished; running what is left\n", name)
		if len(st.Resolved)+len(st.Deleted) > 0 {
			noteNotRechecked(w, fmt.Sprintf("it was finished by hand from the %d/%d the plan describes", st.Stop, st.Total), st)
		}
	}

	lock, err := wtsync.TakeOver(gitDir, opts.now(), st.Lock)
	if err != nil {
		return fmt.Errorf("%s: %w; nothing is resumed", name, err)
	}
	defer func() {
		if lock != nil {
			_ = lock.Release()
		}
	}()
	// The checks above ran without the lock. Another wt that ended this run
	// in between had to hold it to do so, so with the lock in hand the
	// handover is either still the one they read or gone.
	if cur, ok, err := wtsync.ReadState(gitDir); err != nil {
		return err
	} else if !ok || cur.Epoch != st.Epoch {
		return fmt.Errorf("the handover in %s changed while resume was checking it; nothing is resumed", name)
	}

	// Stacked is deliberately not set. It exists so a run does not leave a
	// branch its children are about to be rebased onto waiting on a person;
	// a resume rebases exactly one worktree, so there is no child in flight
	// to strand, and a later unclaimed stop is better answered with a fresh
	// plan file than with a refusal whose only remedy is undo.
	req := wtsync.Request{
		Path: target.Path, Branch: st.Branch, Trunk: st.Trunk,
		Onto: st.Onto, Upstream: st.Upstream, Epoch: st.Epoch, Work: name, Total: st.Total,
	}
	safety := wtsync.Safety{Branch: st.Branch, Epoch: st.Epoch, Ref: st.Safety, Tip: pinned}
	if busy {
		fmt.Fprintf(w, "%s  %s  resuming at %d/%d\n", name, st.Branch, st.Stop, st.Total)
	}
	// Resuming, not rebasing: an interrupt must point back at resume, since
	// the abort that is right for a run would discard a person's resolution.
	res, rerr := trackedRebase(tracker, &rebaseInFlight{work: name, path: target.Path, safety: st.Safety, resuming: true}, func() (wtsync.Result, error) {
		return wtsync.Resume(ctx.Repo.MainRoot, cfg, req, st.OldTip, safety, w)
	})
	if rerr != nil {
		recordResumeFailed(j, st, target.Path, rerr)
		return fmt.Errorf("%s: %w", name, rerr)
	}
	if res.Left != nil {
		fmt.Fprintf(w, "  ⚠ stopped again at %d/%d\n", res.Left.Index, res.Left.Total)
		herr := handOver(ctx, w, handoverInput{
			Work: name, Branch: st.Branch, Path: target.Path, TrunkRef: st.TrunkRef, TrunkSHA: st.Trunk,
			Onto: st.Onto, Upstream: st.Upstream, Epoch: st.Epoch, Cfg: cfg, Res: res, Lock: lock,
			Found: st.Found, Earlier: st.Stopped, Tracker: tracker,
		})
		if herr != nil {
			fmt.Fprintf(w, "  ✗ failed: %v\n", herr)
			// As in run: a brief and a sidecar that may now describe different
			// stops both go, and the person is told where the worktree is. The
			// branch ref has not moved since the run, so the abort is the whole
			// of putting it back; undo refuses a mid-rebase worktree.
			clearHandover(w, target.Path)
			way := wtsync.WayOut(wtsync.Way{Work: name, Path: target.Path, Rebasing: true})
			fmt.Fprintf(w, "  ⚠ %s is left mid-rebase with no plan: %s\n", name, way)
			j.setSync(st.Branch, func(p *SyncParticipant) {
				p.Result, p.Reason, p.Recovery, p.PlanFile = ResultNeedsRecovery, strp(herr.Error()), strp(way), nil
			})
			return fmt.Errorf("not completed: %s (failed)", name)
		}
		lock = nil // kept on purpose
		j.setSync(st.Branch, func(p *SyncParticipant) {
			p.Result, p.Reason = ResultHandedOver, strp("stopped again at a conflict that is yours")
			p.Recovery = strp(wtsync.WayOut(wtsync.Way{Work: name, Plan: true, Rebasing: true, OwesAdd: true}))
			p.UndoCommand = undoCommand(name)
		})
		return fmt.Errorf("not completed: %s (needs you)", name)
	}
	fmt.Fprintf(w, "  ✓ rebased %d commit%s\n", res.Replayed, plural(res.Replayed))
	_, ran, cerr := completeRun(ctx, w, cfg, tracker, name, target.Path, func() completeInput {
		// As a run does: the tip the run found is what the deferred steps
		// compare with and what the undo line names.
		done := res
		done.OldTip = pinned
		return completeInput{
			Branch: st.Branch, Epoch: st.Epoch, Res: done,
			Tell: sessions, TrunkName: strings.TrimPrefix(st.TrunkRef, "origin/"), Landed: landed,
			Check: pathsOnce(st.Stopped, st.Left, st.ResolvedPaths(), st.Deleted, wtsync.StopPaths(res.Stops)),
		}
	})
	owed := owedSteps(ran)
	j.setSync(st.Branch, func(p *SyncParticipant) {
		p.Result, p.PlanFile, p.UndoCommand, p.Deferred = ResultRebased, nil, undoCommand(name), deferredSteps(ran)
		switch {
		case cerr != nil:
			p.Result, p.FailedSteps = ResultRebasedStepFailed, append(p.FailedSteps, "finish: "+cerr.Error())
		case len(owed) > 0:
			p.Result, p.FailedSteps = ResultRebasedStepFailed, append(p.FailedSteps, owed...)
		}
	})
	if cerr != nil {
		return cerr
	}
	if len(owed) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(owedBy(name, owed), ", "))
	}
	t := pushTarget{Work: name, Branch: st.Branch, Path: target.Path}
	j.set(st.Branch, func(p *UpParticipant) { p.PushCommand = append([]string{"git", "-C", t.Path}, pushArgs(t)...) })
	pushed, failed, err := offerPush(w, opts.Push, opts.ConfirmPush, []pushTarget{t})
	if err != nil {
		return err
	}
	j.set(st.Branch, func(p *UpParticipant) { p.Pushed = len(pushed) > 0 })
	if len(failed) > 0 {
		return fmt.Errorf("not completed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// recordResumeFailed records a resume whose rebase failed, by what it left:
// the handover still standing, which resume or undo takes on from, or a
// worktree that needs putting back by hand.
func recordResumeFailed(j *RunJournal, st wtsync.State, path string, rerr error) {
	busy, _ := wtsync.RebaseInProgress(path)
	gitDir, _ := wtsync.GitDir(path)
	_, planned, _ := wtsync.ReadState(gitDir)
	j.setSync(st.Branch, func(p *SyncParticipant) {
		p.Reason = strp(rerr.Error())
		if busy && planned {
			p.Result, p.UndoCommand = ResultHandedOver, undoCommand(st.Work)
			p.Recovery = strp(wtsync.WayOut(wtsync.Way{Work: st.Work, Plan: true, Rebasing: true}))
			return
		}
		p.Result, p.PlanFile = ResultNeedsRecovery, nil
		p.Recovery = strp(wtsync.WayOut(wtsync.Way{Work: st.Work, Path: path, Rebasing: busy, Safety: st.Safety}))
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
		return fmt.Errorf("%d file%s under \"never hand-merge here\" changed: %s; nothing is resumed",
			len(changed), plural(len(changed)), strings.Join(named, ", "))
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
