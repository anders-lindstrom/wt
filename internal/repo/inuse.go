package repo

import (
	"errors"
	"fmt"
)

// BranchUse is the worktree using a branch, and how.
type BranchUse struct {
	Path string
	// By is the operation in Path holding the branch, "bisect" or "rebase";
	// "" when Path simply has it checked out.
	By string
}

// Users maps each branch a worktree has in use to that worktree: the branch it
// has checked out, and the branches its git operations still hold. It reads the
// worktrees rather than for-each-ref's worktreepath, which is empty for a
// branch mid-rebase, for the branch a bisect started from, and for the branches
// a stopped rebase --update-refs will move: only the operation's own files name
// those, and git refuses to delete them all the same. A checkout mid-rebase
// counts as held by that rebase. A branch's own checkout wins over a hold
// elsewhere.
func (w Worktrees) Users() map[string]BranchUse {
	m := map[string]BranchUse{}
	for _, wt := range w {
		if wt.Branch != "" {
			use := BranchUse{Path: wt.Path}
			if wt.Rebasing {
				use.By = "rebase"
			}
			m[wt.Branch] = use
		}
	}
	for _, wt := range w {
		for _, h := range wt.Holds {
			if _, ok := m[h.Branch]; !ok {
				m[h.Branch] = BranchUse{Path: wt.Path, By: h.By}
			}
		}
	}
	return m
}

// ErrWorktreesUnknown is a worktree list that could not be read. It is not the
// same answer as "no worktree is using it", so a delete refuses rather than
// guess, and every caller can tell the two apart.
var ErrWorktreesUnknown = errors.New("could not list worktrees, so cannot tell which branches are in use")

// BranchUsers reads the worktrees and maps the branches they are using.
func (r *Repo) BranchUsers() (map[string]BranchUse, error) {
	worktrees, err := r.Worktrees()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWorktreesUnknown, err)
	}
	return worktrees.Users(), nil
}

// BranchInUseError is why a branch was not deleted: a worktree is using it.
type BranchInUseError struct {
	// Path is the worktree using the branch, and By the operation there
	// holding it — "bisect" or "rebase", "" when it is simply checked out.
	Path string
	By   string
}

func (e *BranchInUseError) Error() string {
	if e.By != "" {
		return "held by the " + e.By + " in " + e.Path
	}
	return "checked out in " + e.Path
}
