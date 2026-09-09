package wtsync

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// Restored is one ref put back.
type Restored struct {
	Branch, From, To, Ref string
	Path                  string
}

// Undo resets every ref the newest run touching branch moved: all safety
// refs sharing that run's epoch. Every checkout involved, the main one
// included, is locked and checked (clean, not mid-rebase, no agent) before
// anything is reset, and every branch is checked against where the run left
// it: a branch that has moved since is refused rather than rewound, because
// the reset would discard commits the run never made. force turns that
// refusal into a fresh safety ref at the current tip, so the forced undo is
// itself undoable; a branch with a later run is refused either way.
func Undo(mainRoot string, worktrees []repo.Worktree, agents []Agent, branch string, now time.Time, force bool) ([]Restored, error) {
	latest, ok, err := LatestSafety(mainRoot, branch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no run to undo for %s", branch)
	}
	all, err := ListSafety(mainRoot)
	if err != nil {
		return nil, err
	}
	byBranch := map[string]repo.Worktree{}
	for _, wt := range worktrees {
		if wt.Branch != "" && !wt.Detached {
			byBranch[wt.Branch] = wt
		}
	}
	var run []Safety
	for _, s := range all {
		if s.Epoch == latest.Epoch {
			run = append(run, s)
		}
	}
	var locks []*Lock
	defer func() {
		for _, l := range locks {
			_ = l.Release()
		}
	}()
	for _, s := range run {
		wt, ok := byBranch[s.Branch]
		if !ok {
			continue
		}
		gitDir, err := GitDir(wt.Path)
		if err != nil {
			return nil, err
		}
		lock, err := Acquire(gitDir, now)
		if err != nil {
			return nil, fmt.Errorf("%s: %w; nothing undone", s.Branch, err)
		}
		locks = append(locks, lock)
		agentPath := wt.Path
		if resolved, err := filepath.EvalSymlinks(wt.Path); err == nil {
			agentPath = resolved
		}
		if a := AgentAt(agents, agentPath); a != nil {
			return nil, fmt.Errorf("%s: an agent session is in it: %s; nothing undone", s.Branch, agentLabel(a))
		}
		out, err := gitEnv(wt.Path, nil, nil, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			return nil, err
		}
		if out != "" {
			return nil, fmt.Errorf("%s has tracked changes; nothing undone", s.Branch)
		}
		if busy, err := RebaseInProgress(wt.Path); err != nil {
			return nil, err
		} else if busy {
			return nil, fmt.Errorf("%s is mid-rebase; nothing undone", s.Branch)
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
	var out []Restored
	for _, s := range run {
		from := tips[s.Branch]
		r := Restored{Branch: s.Branch, From: from, To: s.Tip, Ref: s.Ref}
		if wt, ok := byBranch[s.Branch]; ok {
			r.Path = wt.Path
			if from != s.Tip {
				if _, err := gitEnv(wt.Path, rebaseEnv, nil, "reset", "--hard", s.Tip); err != nil {
					return out, err
				}
			}
		} else if from != s.Tip {
			if _, err := gitEnv(mainRoot, nil, nil, "update-ref", "refs/heads/"+s.Branch, s.Tip, from); err != nil {
				return out, err
			}
		}
		out = append(out, r)
	}
	return out, nil
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
