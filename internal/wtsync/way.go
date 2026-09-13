package wtsync

import "fmt"

// Way is where a sync verb left a worktree, as far as what gets a person out
// of it is concerned. WayOut turns it into the one sentence every verb
// prints for that state, so the wording lives in one table and no caller
// composes a remedy of its own: a caller says what it found, then prints
// WayOut for what to do about it.
type Way struct {
	Work string // what a person calls it; "" prints the verbs bare
	Path string // for the git -C form; "" leaves the abort unqualified
	// Plan is a handover (sidecar) in the git dir; Rebasing a rebase in
	// progress. Both: the run's stop is still waiting. Plan alone: the
	// rebase was finished or aborted by hand, which Finished and Aborted
	// tell apart. Rebasing alone: a rebase with no handover, which only git
	// puts back.
	Plan     bool
	Rebasing bool
	OwesAdd  bool   // paths a person has to git add before resume will continue
	Moved    bool   // the branch carries commits the run did not make: undo needs --force
	Finished bool   // no rebase in progress and VerifyFinished passed: resume runs what is left
	Aborted  bool   // no rebase in progress and the branch is back at the old tip
	Result   bool   // the run pinned its result: a plain undo puts it back
	Safety   string // the old tip, as a ref or a short id, when the sentence should name it
	Inside   int    // commits made inside the handed-over rebase
	Unproven bool   // an old sidecar: whether commits were made inside cannot be told
	Foreign  bool   // the rebase in progress is not the one the handover describes
	Restart  bool   // the stop cannot be continued as it stands: undo, then run again
}

// HandoverWay reads where a worktree's handover stands, touching nothing:
// its rebase still in progress and waiting at the stop, or gone, in which
// case the branch back at the tip the run started from means it was
// aborted by hand, a branch VerifyFinished certifies means it was finished
// by hand and resume runs what is left, and anything else (a commit made
// inside, HEAD reset elsewhere, a reflog that shows another move) is a
// branch that moved, which only the forced undo puts back. Work and Path
// are the caller's to fill.
func HandoverWay(wtPath string, st State) (Way, error) {
	busy, err := RebaseInProgress(wtPath)
	if err != nil {
		return Way{}, err
	}
	w := Way{Plan: true, Rebasing: busy}
	if busy {
		return w, nil
	}
	head, err := gitEnv(wtPath, nil, nil, "rev-parse", "HEAD")
	if err != nil {
		return Way{}, err
	}
	switch {
	case head == st.OldTip:
		w.Aborted = true
	case VerifyFinished(wtPath, st.Branch, st.Work, st.Onto, st.OldTip, st.Total) == nil:
		w.Finished = true
	default:
		w.Moved = true
	}
	return w, nil
}

// WayOut is the sentence for w: one clause, no full stop, so a caller can
// put it after its own finding. The situations are listed once, adjacent,
// so a change of wording is a one-place edit.
func WayOut(w Way) string {
	resume, undo, force, run := "wt sync resume", "wt sync undo", "wt sync undo --force", "wt sync run"
	if w.Work != "" {
		resume += " " + w.Work
		undo += " " + w.Work
		force += " " + w.Work
		run += " " + w.Work
	}
	abort := "git rebase --abort"
	if w.Path != "" {
		abort = "git -C " + w.Path + " rebase --abort"
	}
	switch {
	case w.Plan && w.Rebasing && w.Foreign:
		// J: the rebase is the person's to end, not undo's: a foreign
		// sequencer, or the run's own carrying commits undo cannot pin.
		// A branch that also moved still needs the forced undo afterwards.
		then := undo
		if w.Moved {
			then = force
		}
		if w.Inside > 0 {
			return fmt.Sprintf("finish that rebase yourself, or abort it (%s) and lose those commits, then %s", abort, then)
		}
		return fmt.Sprintf("finish or abort that rebase yourself (%s), then %s", abort, then)
	case w.Plan && w.Rebasing && w.Unproven:
		// I, for a sidecar written before HEAD was recorded: nothing tells
		// the run's picks from a person's commits, so both ways keep what
		// is there.
		return fmt.Sprintf("%s carries on; %s aborts and keeps what is at HEAD under a safety ref", resume, force)
	case w.Plan && w.Rebasing && w.Inside > 0:
		// I: commits a person made inside the run's rebase.
		them := "it"
		if w.Inside > 1 {
			them = "them"
		}
		return fmt.Sprintf("%s keeps %s and carries on; %s aborts and keeps %s under a safety ref", resume, them, force, them)
	case w.Plan && w.Rebasing && w.Restart:
		// K: the stop is not one a person may finish (a strategy's file
		// hand-merged or unmerged again), so the run starts over.
		return fmt.Sprintf("%s puts everything back, then %s starts again", undo, run)
	case w.Plan && w.Rebasing && w.OwesAdd:
		// B: the stop is waiting on the person's own resolution.
		return fmt.Sprintf("resolve what is yours, git add it, then %s; or %s puts everything back", resume, undo)
	case w.Plan && w.Rebasing:
		// A: the stop is waiting, and either verb takes it from here.
		return fmt.Sprintf("%s, or %s", resume, undo)
	case w.Rebasing:
		// C: a rebase with no handover. Undo refuses it (nothing to abort
		// on its behalf, nothing pinned), so git is the whole of the way
		// back, and the tip is named when the caller knows it.
		s := fmt.Sprintf("finish it by hand, or put it back with %s", abort)
		if w.Safety != "" {
			s += "; the old tip is " + w.Safety
		}
		return s
	case w.Plan && w.Finished:
		// D: git rebase --continue finished the handed-over rebase. Resume
		// runs the deferred steps and pins the result; a plain undo would
		// call the rebased branch "moved since the run".
		return fmt.Sprintf("the handed-over rebase was finished by hand: %s runs what is left; %s puts the old tip back and keeps what is there now under a safety ref", resume, force)
	case w.Plan && w.Aborted:
		// E: the branch is back where it started with the handover still
		// there. A run refuses while the plan is there, so undo clears it
		// first.
		return fmt.Sprintf("the handed-over rebase was aborted, not finished: %s clears its plan, then %s starts again", undo, run)
	case w.Moved && !w.Plan && !w.Result:
		// G: interrupted after the rebase and before the result was pinned.
		return fmt.Sprintf("the rebase stands; %s puts it back", force)
	case w.Moved:
		// F: the branch carries commits the run did not make.
		return fmt.Sprintf("%s puts the run's tip back and keeps what is there now under a safety ref", force)
	case w.Result:
		// H: a completed run.
		if w.Safety != "" {
			return fmt.Sprintf("%s puts it back (was %s)", undo, w.Safety)
		}
		return fmt.Sprintf("%s puts it back", undo)
	}
	return fmt.Sprintf("%s puts it back", undo)
}
