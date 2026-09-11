package wtsync

import (
	"fmt"
	"time"

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
func Undo(mainRoot string, worktrees []repo.Worktree, agents []Agent, branch string, now time.Time, force bool) ([]Restored, error) {
	run, all, err := newestRun(mainRoot, branch)
	if err != nil {
		return nil, err
	}
	byBranch := map[string]repo.Worktree{}
	for _, wt := range worktrees {
		if wt.Branch != "" && !wt.Detached {
			byBranch[wt.Branch] = wt
		}
	}
	var locks []*Lock
	defer func() {
		for _, l := range locks {
			_ = l.Release()
		}
	}()
	// The branches whose handed-over rebase is to be aborted, collected
	// here and aborted only once every branch has passed every check.
	aborting := map[string]bool{}
	for _, s := range run {
		wt, ok := byBranch[s.Branch]
		if !ok {
			continue
		}
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return nil, err
		}
		// A handover left its lock behind; undo is one of the two commands
		// entitled to take that exact lock over. A sidecar that cannot be
		// read is a refusal: the lock it records cannot be recognised, and
		// the handover it marks cannot be trusted enough to abort.
		st, stateOK, err := ReadState(gitDir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w; nothing undone", s.Branch, err)
		}
		var prev LeftLock
		if stateOK {
			prev = st.Lock
		}
		lock, err := TakeOver(gitDir, now, prev)
		if err != nil {
			return nil, fmt.Errorf("%s: %w; nothing undone", s.Branch, err)
		}
		locks = append(locks, lock)
		if sessions := SessionsAt(agents, wt.Path); len(sessions.Busy()) > 0 {
			return nil, fmt.Errorf("%s: an agent session is busy in it: %s; nothing undone", s.Branch, sessions.Label(agentLabel))
		}
		busy, err := RebaseInProgress(wt.Path)
		if err != nil {
			return nil, err
		}
		if busy {
			has, herr := HasPlan(gitDir)
			if herr != nil {
				return nil, herr
			}
			if !has {
				return nil, fmt.Errorf("%s is mid-rebase; nothing undone", s.Branch)
			}
			// A rebase this tool left: aborting it is what undo is for, and
			// its staged conflicts are not dirt. The abort happens later,
			// once every branch has passed every check.
			aborting[s.Branch] = true
			continue
		}
		out, err := gitEnv(wt.Path, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			return nil, err
		}
		if out != "" {
			return nil, fmt.Errorf("%s has tracked changes; nothing undone", s.Branch)
		}
	}
	// Where each branch is now, read once under the locks and reused by the
	// apply loop below: nothing may move between the check and the reset.
	tips := map[string]string{}
	// What a forced undo will pin. Collected here and written only once
	// every branch has passed: a refusal raised by a later branch of the
	// same run must not leave a stray safety ref behind, which would make
	// every later plain undo report "already at" and strand the run.
	var pins []Pin
	forceEpoch := now.UnixNano()
	for _, s := range run {
		for _, other := range all {
			if other.Branch == s.Branch && other.Epoch > s.Epoch {
				return nil, fmt.Errorf("%s has a later run (%d); undo that first", s.Branch, other.Epoch)
			}
		}
		tip, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", "refs/heads/"+s.Branch)
		if err != nil {
			return nil, err
		}
		tips[s.Branch] = tip
		if tip == s.Tip {
			continue // already back at the old tip; nothing to discard
		}
		// The run's own result is where the branch should still be. Without
		// one the run never finished for this branch, so any movement at all
		// is somebody else's.
		want, ok, err := ResultTip(mainRoot, s.Branch, s.Epoch)
		if err != nil {
			return nil, err
		}
		if !ok {
			want = s.Tip
		}
		if tip == want {
			continue
		}
		if !force {
			return nil, fmt.Errorf("%s has moved since that run (%s commits ahead of it); undo would discard them", s.Branch, aheadCount(mainRoot, want, tip))
		}
		// The forced undo is itself a run: it moves the branch from tip to
		// s.Tip, so those are its safety and its result. Pinning the result
		// too is what lets a plain undo of the forced undo see the branch
		// where it was left and put the discarded commits back.
		pins = append(pins, Pin{Branch: s.Branch, Safety: tip, Result: s.Tip})
	}
	if err := WriteRun(mainRoot, forceEpoch, pins); err != nil {
		return nil, err
	}
	// Only now, with every branch of the run checked and the forced-undo
	// pins written, does anything change: aborting is a mutation, and doing
	// it earlier would let a refusal raised by a later branch leave an
	// already-discarded resolution behind.
	//
	// The aborts are a pass of their own, before any branch is reset. A
	// leaf handed over onto a parent the run rebased must never be left
	// mid-rebase onto a parent that has already been put back, so if an
	// abort fails the undo stops there, with nothing reset, and reports
	// the aborts that did happen.
	aborted := map[string]bool{}
	for _, s := range run {
		if !aborting[s.Branch] {
			continue
		}
		wt := byBranch[s.Branch]
		if err := abortRebase(wt); err != nil {
			return abortedRows(mainRoot, run, byBranch, tips, aborted), err
		}
		aborted[s.Branch] = true
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return abortedRows(mainRoot, run, byBranch, tips, aborted), err
		}
		if err := RemovePlan(gitDir); err != nil {
			return abortedRows(mainRoot, run, byBranch, tips, aborted), fmt.Errorf("%s: the rebase is aborted but its handover was not removed: %w", s.Branch, err)
		}
	}
	// A branch that was handed over may still have a stale handover even
	// when no rebase is in progress: somebody finished or aborted it by
	// hand. Undoing the run is the end of that handover either way.
	for _, s := range run {
		wt, ok := byBranch[s.Branch]
		if !ok || aborted[s.Branch] {
			continue
		}
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return abortedRows(mainRoot, run, byBranch, tips, aborted), err
		}
		if err := RemovePlan(gitDir); err != nil {
			return abortedRows(mainRoot, run, byBranch, tips, aborted), err
		}
	}
	// A failure here returns the rows already put back, and every abort
	// among the branches not reached yet, so a partial restore is visible.
	var out []Restored
	for i, s := range run {
		from := tips[s.Branch]
		r := Restored{Branch: s.Branch, From: from, To: s.Tip, Ref: s.Ref, Aborted: aborted[s.Branch]}
		if wt, ok := byBranch[s.Branch]; ok {
			r.Path = wt.Path
			if from != s.Tip {
				if _, err := gitEnv(wt.Path, rebaseEnv, nil, "reset", "--hard", s.Tip); err != nil {
					return append(out, abortedRows(mainRoot, run[i:], byBranch, tips, aborted)...), err
				}
			}
		} else if from != s.Tip {
			if _, err := gitEnv(mainRoot, nil, nil, "update-ref", "refs/heads/"+s.Branch, s.Tip, from); err != nil {
				return append(out, abortedRows(mainRoot, run[i:], byBranch, tips, aborted)...), err
			}
		}
		out = append(out, r)
	}
	return out, nil
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
	latest, ok, err := LatestSafety(mainRoot, branch)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("no run to undo for %s", branch)
	}
	if all, err = ListSafety(mainRoot); err != nil {
		return nil, nil, err
	}
	for _, s := range all {
		if s.Epoch == latest.Epoch {
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
