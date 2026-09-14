package wtsync

// Standing is where a worktree stands under a verb's lock: what a run reads
// again once it holds the lock, so that what triage saw still holds, and
// what undo reads once it has taken a handover's lock over, so that nothing
// can move between a check and the reset that trusts it. Every caller keeps
// its own order of refusals, so every answer is here rather than a verdict.
//
// The reads are in one order: the rebase first; with a rebase in progress
// the handover marker, and without one the dirt, since a rebase's staged
// resolutions are not dirt and neither caller consults the other. Err is
// the first read that failed, and an answer that could not be read is a
// failed check for both callers, never a passed one. A marker that cannot
// be read counts as absent: the rebase is refused either way, and the
// caller only says which way out.
type Standing struct {
	Sessions Sessions // the live sessions in the worktree, from the listing given
	Rebasing bool     // a rebase in progress, under either backend
	Plan     bool     // with Rebasing: a handover marks it as a run's
	Dirty    bool     // without Rebasing: tracked changes; untracked files never block a rebase
	Err      error
}

// Recheck reads where the worktree at wtPath stands, with gitDir its own git
// dir and agents the sessions to look for in it. The caller holds the lock.
func Recheck(wtPath, gitDir string, agents []Agent) Standing {
	s := RecheckGit(wtPath, gitDir)
	s.Sessions = SessionsAt(agents, wtPath)
	return s
}

// RecheckGit is the part of Recheck git answers, for a caller that has
// already refused on the sessions: the listing it holds says who is in the
// worktree without a single git command, and a worktree refused for a busy
// session is never read. Sessions is left empty.
func RecheckGit(wtPath, gitDir string) Standing {
	var s Standing
	busy, err := RebaseInProgress(wtPath)
	if err != nil {
		s.Err = err
		return s
	}
	s.Rebasing = busy
	if busy {
		has, err := HasPlan(gitDir)
		s.Plan = err == nil && has
		return s
	}
	// --no-optional-locks: a plain status may refresh and rewrite the index,
	// and a check must not touch the worktree it is checking.
	out, err := gitEnv(wtPath, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		s.Err = err
		return s
	}
	s.Dirty = out != ""
	return s
}
