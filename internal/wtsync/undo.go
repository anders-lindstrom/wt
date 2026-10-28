package wtsync

import (
	"cmp"
	"errors"
	"fmt"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Restored is one ref put back.
type Restored struct {
	Branch, From, To, Ref string
	Path                  string
	// Aborted is a branch whose handed-over rebase was aborted to put it
	// back: the ref itself never moved during the run.
	Aborted bool
	// NotRewound is an aborted branch that still had to be rewound to To
	// when a failure stopped the undo: From is where it is now, and the
	// rewind never happened.
	NotRewound bool
	// Kept is the safety ref a forced undo wrote for this branch, pinning
	// KeptTip: the tip it discarded, or the detached HEAD of the rebase it
	// aborted. Ref never names it: Ref is the run's own ref, the one that
	// supplied To. Empty for a plain undo.
	Kept, KeptTip string
}

// Undo resets every ref the newest run touching branch moved: all safety
// refs sharing that run's epoch. Every checkout involved, the main one
// included, is locked and checked (clean, not mid-rebase, no busy session) before
// anything is reset, and every branch is checked against where the run left
// it: a branch that has moved since is refused rather than rewound, because
// the reset would discard commits the run never made. force turns that
// refusal into a fresh safety ref at the current tip, so the forced undo is
// itself undoable; a branch with a later run is refused either way.
//
// A rebase a run handed over is the one mid-rebase checkout undo accepts:
// its lock is taken over rather than waited out, and the rebase is aborted
// and its handover removed only after every branch has passed every check.
// A rebase with no handover is still refused.
//
// An idle session is not a refusal here: naming it, and asking about it
// before a single lock is taken, is the caller's.
func Undo(mainRoot string, worktrees []repo.Worktree, listed []agents.Agent, branch string, now time.Time, force bool) ([]Restored, error) {
	run, all, err := newestRun(mainRoot, branch)
	if err != nil {
		return nil, err
	}
	u := newUndo(mainRoot, run, worktrees, now)
	defer u.release()
	if err := u.lockCheckouts(listed, now, force); err != nil {
		return nil, err
	}
	if err := u.checkBranches(all, force); err != nil {
		return nil, err
	}
	if err := WriteRun(mainRoot, u.forceEpoch, u.pins); err != nil {
		return nil, err
	}
	// Only now, with every branch of the run checked and the forced-undo
	// pins written, does anything change: aborting is a mutation, and doing
	// it earlier would let a refusal raised by a later branch leave an
	// already-discarded resolution behind.
	if err := u.abortHandovers(); err != nil {
		return u.stopped(run, err)
	}
	if err := u.clearStaleHandovers(); err != nil {
		return u.stopped(run, err)
	}
	out, err := u.resetBranches()
	if err != nil {
		return out, err
	}
	return out, u.disown()
}

// undo is one Undo under way: the run it puts back, and what each phase
// learns about its branches for the phases after it.
type undo struct {
	mainRoot string
	run      []Safety
	byBranch map[string]repo.Worktree
	// Each worktree's git dir, read once and reused: an undo asks for the
	// same one at three points — to take the lock over, to remove an aborted
	// handover, and to clear a stale one — and git answers the same each time.
	gitDirs map[string]string
	locks   []*Lock
	// The branches whose handed-over rebase is to be aborted, collected
	// by lockCheckouts and aborted only once every branch has passed every
	// check.
	aborting map[string]bool
	// Of those, the ones whose rebase carries commits a person made inside
	// it, and the HEAD the abort will discard: a forced undo pins it.
	insideHead map[string]string
	// Each checkout's sidecar, for the branch checks: a handover whose
	// rebase is gone may have been finished by hand, which is named as such
	// rather than as a branch that moved.
	states map[string]State
	// Where each branch is now, read once under the locks by checkBranches
	// and reused by the resets: nothing may move between the check and the
	// reset.
	tips map[string]string
	// What a forced undo will pin. Collected by checkBranches and written
	// only once every branch has passed: a refusal raised by a later branch
	// of the same run must not leave a stray safety ref behind, which would
	// make every later plain undo report "already at" and strand the run.
	pins       []Pin
	forceEpoch int64
	aborted    map[string]bool
	// The branches whose pinned tip this undo has actually discarded: reset
	// past it, or aborted a rebase that carried it. A failure past the pins
	// must leave a forced-undo pin for exactly these and no other. A pin left
	// for a branch that never moved would be the newest run touching it, with
	// its safety at the branch's own tip, so every later plain undo would say
	// "already at" and the run it was meant to supersede would be out of
	// reach.
	moved map[string]bool
}

func newUndo(mainRoot string, run []Safety, worktrees []repo.Worktree, now time.Time) *undo {
	u := &undo{mainRoot: mainRoot, run: run, byBranch: map[string]repo.Worktree{}, gitDirs: map[string]string{},
		aborting: map[string]bool{}, insideHead: map[string]string{}, states: map[string]State{},
		tips: map[string]string{}, forceEpoch: now.UnixNano(), aborted: map[string]bool{}, moved: map[string]bool{}}
	for _, wt := range worktrees {
		if wt.Branch != "" && !wt.Detached {
			u.byBranch[wt.Branch] = wt
		}
	}
	return u
}

func (u *undo) gitDirOf(wt repo.Worktree) (string, error) {
	if dir, ok := u.gitDirs[wt.Path]; ok {
		return dir, nil
	}
	dir, err := GitDir(wt.Path)
	if err != nil {
		return "", err
	}
	u.gitDirs[wt.Path] = dir
	return dir, nil
}

func (u *undo) release() {
	for _, l := range u.locks {
		_ = l.Release()
	}
}

// lockCheckouts takes the lock of every checkout the run touched and checks
// each under it: no busy session, no tracked changes, and no rebase but one
// the run handed over, which it marks for aborting.
func (u *undo) lockCheckouts(listed []agents.Agent, now time.Time, force bool) error {
	for _, s := range u.run {
		wt, ok := u.byBranch[s.Branch]
		if !ok {
			continue
		}
		gitDir, err := u.gitDirOf(wt)
		if err != nil {
			return err
		}
		// A handover left its lock behind; undo is one of the two commands
		// entitled to take that exact lock over. A sidecar that cannot be
		// read is a refusal: the lock it records cannot be recognised, and
		// the handover it marks cannot be trusted enough to abort.
		st, stateOK, err := ReadState(gitDir)
		if err != nil {
			return fmt.Errorf("%s: %w; nothing undone", s.Branch, err)
		}
		var prev LeftLock
		if stateOK {
			prev = st.Lock
			u.states[s.Branch] = st
		}
		lock, err := TakeOver(gitDir, now, prev)
		if err != nil {
			return fmt.Errorf("%s: %w; nothing undone", s.Branch, err)
		}
		u.locks = append(u.locks, lock)
		// The busy session first, from the listing alone: a worktree refused
		// for it is not read at all, so an interrupt during the reads below
		// cannot land on one that was already refused.
		if sessions := agents.SessionsAt(listed, wt.Path); len(sessions.Busy()) > 0 {
			return fmt.Errorf("%s: an agent session is busy in it: %s; nothing undone", s.Branch, sessions.Label(agentLabel))
		}
		// Read under the lock, so what the abort and the reset later trust
		// is what is there. The sidecar read above is the handover marker
		// this loop goes by; the lock is what keeps it true.
		stand := RecheckGit(wt.Path, gitDir)
		if stand.Err != nil {
			return stand.Err
		}
		if stand.Rebasing {
			if err := u.checkHandover(s, wt, st, stateOK, force); err != nil {
				return err
			}
			continue
		}
		if stand.Dirty {
			return fmt.Errorf("%s has tracked changes; nothing undone", s.Branch)
		}
	}
	return nil
}

// checkHandover checks a checkout found mid-rebase: only a rebase the run
// handed over may be aborted, and only a forced undo aborts one that
// carries a person's commits, pinning them. It marks the branch for
// aborting.
func (u *undo) checkHandover(s Safety, wt repo.Worktree, st State, stateOK, force bool) error {
	// The sidecar lockCheckouts read is the handover marker; a rebase
	// without one is somebody's own.
	if !stateOK {
		return fmt.Errorf("%s is mid-rebase; nothing undone", s.Branch)
	}
	// A sidecar beside a rebase does not make the rebase the run's:
	// a person may have aborted the run's and started their own,
	// from a commit of theirs, leaving the sidecar behind. Aborting
	// that would discard the commit, and no pin covers it (the
	// branch ref never moved), so force does not lift this refusal.
	foreign := Way{Work: cmp.Or(st.Work, s.Branch), Path: wt.Path, Plan: true, Rebasing: true, Foreign: true}
	if err := VerifyLeft(wt.Path, st); err != nil {
		return fmt.Errorf("%s: %w; %s; nothing undone", s.Branch, err, WayOut(foreign))
	}
	// The run's own rebase may still carry a person's commits: the
	// stop's resolution committed by hand, or a pick their own
	// rebase --continue made before stopping again. The abort would
	// discard them and nothing but a reflog would name them, so a
	// plain undo refuses and a forced one pins the HEAD it discards.
	// A handover from before the head was recorded cannot prove
	// there are none, and is treated as carrying some.
	in, err := CommittedInside(wt.Path, st)
	if err != nil {
		foreign.Inside = 1 // commits it cannot name, which an abort loses
		return fmt.Errorf("%s: %w; %s; nothing undone", s.Branch, err, WayOut(foreign))
	}
	if len(in.Commits) > 0 || in.Unproven {
		if !force {
			work := cmp.Or(st.Work, s.Branch)
			if in.Unproven {
				return unprovenRefusal(work, len(in.Commits))
			}
			return insideRefusal(work, in.Commits)
		}
		u.insideHead[s.Branch] = in.Head
	}
	// A rebase this tool left: aborting it is what undo is for, and
	// its staged conflicts are not dirt. The abort happens later,
	// once every branch has passed every check.
	u.aborting[s.Branch] = true
	return nil
}

// checkBranches reads where each branch of the run is now and checks it
// against where the run left it, collecting the pins a forced undo writes.
// all is every safety ref there is, for the later runs that refuse.
func (u *undo) checkBranches(all []Safety, force bool) error {
	for _, s := range u.run {
		for _, other := range all {
			if other.Branch == s.Branch && other.Epoch > s.Epoch {
				return fmt.Errorf("%s has a later run (%d); undo that first", s.Branch, other.Epoch)
			}
		}
		tip, err := gitEnv(u.mainRoot, nil, nil, "rev-parse", "--verify", "refs/heads/"+s.Branch)
		if err != nil {
			return err
		}
		u.tips[s.Branch] = tip
		// The run's own result is where the branch should still be. Without
		// one the run never finished for this branch: it is then where the
		// run fast-forwarded it to, when it did, and otherwise any movement
		// at all is somebody else's.
		want, ok, err := ResultTip(u.mainRoot, s.Branch, s.Epoch)
		if err != nil {
			return err
		}
		if !ok {
			if want, ok, err = ForwardTip(u.mainRoot, s.Branch, s.Epoch); err != nil {
				return err
			}
		}
		if !ok {
			want = s.Tip
		}
		if tip == s.Tip || tip == want {
			// Where the run found it or where the run left it: nothing of
			// the branch's own to discard — unless the abort will discard a
			// person's commits inside the rebase. The forced undo then pins
			// that HEAD as what it discards and s.Tip as where it leaves the
			// branch, exactly as for a moved branch, so a plain undo of the
			// forced undo puts those commits back. The branch ref is at the
			// old tip under a rebase the run started from there, and at the
			// remote's commit under one it fast-forwarded first: the pin is
			// the same for both.
			if h := u.insideHead[s.Branch]; h != "" {
				u.pins = append(u.pins, Pin{Branch: s.Branch, Safety: h, Result: s.Tip})
			}
			continue
		}
		if !force {
			return u.movedRefusal(s, want, tip)
		}
		// A branch moved by hand whose rebase also carries a person's
		// commits is two things to keep, and a forced undo pins one.
		if u.insideHead[s.Branch] != "" {
			yours := WayOut(Way{Work: cmp.Or(u.states[s.Branch].Work, s.Branch), Path: u.byBranch[s.Branch].Path, Plan: true, Rebasing: true, Foreign: true, Moved: true, Inside: 1})
			return fmt.Errorf("%s has moved since that run and carries commits made inside its handed-over rebase; one safety ref cannot keep both, so %s; nothing undone", s.Branch, yours)
		}
		// The forced undo is itself a run: it moves the branch from tip to
		// s.Tip, so those are its safety and its result. Pinning the result
		// too is what lets a plain undo of the forced undo see the branch
		// where it was left and put the discarded commits back.
		u.pins = append(u.pins, Pin{Branch: s.Branch, Safety: tip, Result: s.Tip})
	}
	return nil
}

// movedRefusal is a plain undo's answer to a branch that is neither where
// the run found it nor where the run left it.
func (u *undo) movedRefusal(s Safety, want, tip string) error {
	// A handed-over rebase somebody finished with git alone leaves
	// the branch rebased and the handover behind: the run's own
	// rebase, which resume runs the rest of, not a branch that
	// moved. Read-only, and only where a handover has no rebase.
	st, stateOK := u.states[s.Branch]
	if stateOK && !u.aborting[s.Branch] {
		wt := u.byBranch[s.Branch]
		if VerifyFinished(wt.Path, s.Branch, cmp.Or(st.Work, s.Branch), st.Onto, st.OldTip, st.Total) == nil {
			return fmt.Errorf("%s: %s; nothing undone", s.Branch, WayOut(Way{Work: cmp.Or(st.Work, s.Branch), Path: wt.Path, Plan: true, Finished: true}))
		}
	}
	// Also what a rebase finished by hand with a commit made inside
	// it comes to: the count refuses it as the run's, so the way out
	// is the forced undo, and the refusal has to say so.
	moved := WayOut(Way{Work: cmp.Or(st.Work, s.Branch), Plan: stateOK, Moved: true})
	return fmt.Errorf("%s has moved since that run (%s commits ahead of it); undo would discard them: %s", s.Branch, aheadCount(u.mainRoot, want, tip), moved)
}

// abortHandovers aborts every handed-over rebase lockCheckouts marked, and
// removes its handover.
//
// The aborts are a pass of their own, before any branch is reset. A leaf
// handed over onto a parent the run rebased must never be left mid-rebase
// onto a parent that has already been put back, so if an abort fails the
// undo stops there, with nothing reset, and reports the aborts that did
// happen.
func (u *undo) abortHandovers() error {
	for _, s := range u.run {
		if !u.aborting[s.Branch] {
			continue
		}
		wt := u.byBranch[s.Branch]
		if err := abortRebase(wt); err != nil {
			return err
		}
		u.aborted[s.Branch] = true
		// The abort resets the branch to the tip the rebase started from.
		// That discards what the pin keeps in two cases: the commits made
		// inside the rebase, and a branch ref moved by hand meanwhile, which
		// the abort puts back before any reset would have.
		if u.insideHead[s.Branch] != "" || u.tips[s.Branch] != s.Tip {
			u.moved[s.Branch] = true
		}
		gitDir, err := u.gitDirOf(wt)
		if err != nil {
			return err
		}
		if err := RemovePlan(gitDir); err != nil {
			return fmt.Errorf("%s: the rebase is aborted but its handover was not removed: %w", s.Branch, err)
		}
	}
	return nil
}

// clearStaleHandovers removes the handover of every other branch of the
// run. A branch that was handed over may still have a stale handover even
// when no rebase is in progress: somebody finished or aborted it by hand.
// Undoing the run is the end of that handover either way.
func (u *undo) clearStaleHandovers() error {
	for _, s := range u.run {
		wt, ok := u.byBranch[s.Branch]
		if !ok || u.aborted[s.Branch] {
			continue
		}
		gitDir, err := u.gitDirOf(wt)
		if err != nil {
			return err
		}
		if err := RemovePlan(gitDir); err != nil {
			return err
		}
	}
	return nil
}

// resetBranches puts every branch of the run back at its safety tip. A
// failure here returns the rows already put back, and every abort among the
// branches not reached yet, so a partial restore is visible.
func (u *undo) resetBranches() ([]Restored, error) {
	pinned := map[string]Pin{}
	for _, p := range u.pins {
		pinned[p.Branch] = p
	}
	var out []Restored
	for i, s := range u.run {
		from := u.tips[s.Branch]
		r := Restored{Branch: s.Branch, From: from, To: s.Tip, Ref: s.Ref, Aborted: u.aborted[s.Branch]}
		if p, ok := pinned[s.Branch]; ok {
			r.Kept, r.KeptTip = SafetyRef(s.Branch, u.forceEpoch), p.Safety
		}
		if wt, ok := u.byBranch[s.Branch]; ok {
			r.Path = wt.Path
			if from != s.Tip {
				if _, err := gitEnv(wt.Path, rebaseEnv, nil, "reset", "--hard", s.Tip); err != nil {
					rows, err := u.stopped(u.run[i:], err)
					return append(out, rows...), err
				}
				u.moved[s.Branch] = true
			}
		} else if from != s.Tip {
			if _, err := gitEnv(u.mainRoot, nil, nil, "update-ref", "refs/heads/"+s.Branch, s.Tip, from); err != nil {
				rows, err := u.stopped(u.run[i:], err)
				return append(out, rows...), err
			}
			u.moved[s.Branch] = true
		}
		out = append(out, r)
	}
	return out, nil
}

// disown takes back what the run fast-forwarded each branch to, once every
// branch is back: it was the remote's and is no longer anything the branch
// had. Past the resets a failure here undoes nothing, so it is reported with
// the rows, not instead.
func (u *undo) disown() error {
	var derr error
	for _, s := range u.run {
		if err := Disown(u.mainRoot, s.Branch, s.Epoch); err != nil {
			derr = errors.Join(derr, fmt.Errorf("%s is back, but the commit the run fast-forwarded it to still reads as one it had: %w", s.Branch, err))
		}
	}
	return derr
}

// stopped is what an undo that failed after its pins were written returns:
// the rows of the branches among run it aborted, and err, joined with any
// forced-undo pin it could not drop for a branch it did not move.
func (u *undo) stopped(run []Safety, err error) ([]Restored, error) {
	return abortedRows(u.mainRoot, run, u.byBranch, u.tips, u.aborted), u.dropUnmovedPins(err)
}

// dropUnmovedPins deletes the forced-undo pin of every branch this undo has
// not moved, so the run it was meant to supersede stays in reach.
func (u *undo) dropUnmovedPins(err error) error {
	for _, p := range u.pins {
		if u.moved[p.Branch] {
			continue
		}
		s := Safety{Branch: p.Branch, Epoch: u.forceEpoch, Ref: SafetyRef(p.Branch, u.forceEpoch), Tip: p.Safety}
		if derr := DeleteSafety(u.mainRoot, s); derr != nil {
			err = errors.Join(err, fmt.Errorf("%s: the forced undo's safety ref %s could not be removed: %w", p.Branch, s.Ref, derr))
		}
	}
	return err
}

// insideRefusal is the plain undo's answer to commits made inside a
// handed-over rebase: the commits, named, and both ways of keeping them.
func insideRefusal(work string, inside []Commit) error {
	first := fmt.Sprintf("%s %q", git.ShortID(inside[0].SHA, 7), inside[0].Subject)
	what, them := fmt.Sprintf("1 commit made inside the handed-over rebase (%s)", first), "it"
	if len(inside) > 1 {
		what, them = fmt.Sprintf("%d commits made inside the handed-over rebase (first: %s)", len(inside), first), "them"
	}
	return fmt.Errorf("%s has %s; undo would discard %s: %s", work, what, them, WayOut(Way{Work: work, Plan: true, Rebasing: true, Inside: len(inside)}))
}

// unprovenRefusal is the plain undo's answer to a handover written before
// the sidecar recorded HEAD: it cannot tell the run's picks from a person's
// commits, so it names both ways of keeping whatever is there. n is how
// many commits sit on top of what the run rebased onto.
func unprovenRefusal(work string, n int) error {
	on := ""
	if n > 0 {
		on = fmt.Sprintf(" (%d commit%s on top of what it rebased onto)", n, pluralPlan(n))
	}
	return fmt.Errorf("%s was handed over by an older wt that did not record where it left HEAD; undo cannot tell whether commits were made inside the rebase%s: %s", work, on, WayOut(Way{Work: work, Plan: true, Rebasing: true, Unproven: true}))
}

// abortRebase aborts the rebase in wt and checks the abort took.
func abortRebase(wt repo.Worktree) error {
	if _, err := gitEnv(wt.Path, rebaseEnv, nil, "rebase", "--abort"); err != nil {
		return fmt.Errorf("%s: rebase --abort: %w", wt.Branch, err)
	}
	if busy, err := RebaseInProgress(wt.Path); err != nil {
		return err
	} else if busy {
		return fmt.Errorf("%s is still mid-rebase after the abort", wt.Branch)
	}
	return nil
}

// abortedRows reports the branches of run that were aborted when a failure
// stopped the undo before their row was written. None of them has been
// reset. A handover the run never moved is fully put back by its abort; one
// a forced undo also had to rewind is reported from where it is now, as not
// rewound, rather than as a rewind that never happened.
func abortedRows(mainRoot string, run []Safety, byBranch map[string]repo.Worktree, tips map[string]string, aborted map[string]bool) []Restored {
	var rows []Restored
	for _, s := range run {
		if !aborted[s.Branch] {
			continue
		}
		r := Restored{Branch: s.Branch, From: tips[s.Branch], To: s.Tip, Ref: s.Ref, Path: byBranch[s.Branch].Path, Aborted: true}
		if r.From != s.Tip {
			// The abort itself moves the branch to where the rebase began,
			// which is the old tip only if nobody restarted it from elsewhere.
			cur, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", "refs/heads/"+s.Branch)
			if err != nil || cur != s.Tip {
				r.NotRewound = true
				if err == nil {
					r.From = cur
				}
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// aheadCount is how many commits tip carries beyond base, as text, so a
// refusal can say what it would have discarded. A count git cannot produce
// is reported as unknown rather than guessed at.
func aheadCount(mainRoot, base, tip string) string {
	out, err := gitEnv(mainRoot, nil, nil, "rev-list", "--count", base+".."+tip)
	if err != nil || out == "" {
		return "unknown"
	}
	return out
}

// newestRun is the safety refs of the newest run that touched branch, and
// every safety ref there is.
func newestRun(mainRoot, branch string) (run, all []Safety, err error) {
	// ListSafety comes back newest epoch first, so the branch's newest run is
	// simply the first ref naming it. Asking LatestSafety as well would list
	// every safety ref a second time to learn the same thing.
	if all, err = ListSafety(mainRoot); err != nil {
		return nil, nil, err
	}
	epoch, found := int64(0), false
	for _, s := range all {
		if s.Branch == branch {
			epoch, found = s.Epoch, true
			break
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("no run to undo for %s", branch)
	}
	for _, s := range all {
		if s.Epoch == epoch {
			run = append(run, s)
		}
	}
	return run, all, nil
}

// UndoBranches is every branch Undo would put back for branch: the branches
// of the newest run that touched it. A caller names what it is about to
// touch, and asks, before Undo takes a single lock.
func UndoBranches(mainRoot, branch string) ([]string, error) {
	run, _, err := newestRun(mainRoot, branch)
	if err != nil {
		return nil, err
	}
	branches := make([]string, len(run))
	for i, s := range run {
		branches[i] = s.Branch
	}
	return branches, nil
}
