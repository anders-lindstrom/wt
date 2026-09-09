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
// anything is reset.
func Undo(mainRoot string, worktrees []repo.Worktree, agents []Agent, branch string, now time.Time) ([]Restored, error) {
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
	var out []Restored
	for _, s := range run {
		from, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", "refs/heads/"+s.Branch)
		if err != nil {
			return out, err
		}
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
