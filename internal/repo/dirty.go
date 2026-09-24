package repo

import (
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
)

// Dirty reports whether the checkout at path has uncommitted changes.
// trackedOnly leaves untracked files out of the answer, which is what anything
// about to rebase asks: an untracked file never blocks a rebase, and a script
// may have left one behind.
//
// --no-optional-locks, because reading a status is not a reason to rewrite the
// index of a worktree somebody else may be working in.
func Dirty(path string, trackedOnly bool) (bool, error) {
	args := []string{"--no-optional-locks", "status", "--porcelain"}
	if trackedOnly {
		args = append(args, "--untracked-files=no")
	}
	out, err := git.Run(path, args...)
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// Vanishing reports that the checkout at path is being deleted: git can no
// longer read it as a worktree, or every change it has is a tracked file gone
// from disk. It is how a removal tells a checkout somebody else is deleting
// from one somebody is editing.
func Vanishing(path string) bool {
	out, err := git.Run(path, "--no-optional-locks", "status", "--porcelain")
	if err != nil {
		return true
	}
	if out == "" {
		return false
	}
	for _, l := range strings.Split(out, "\n") {
		if len(l) < 2 || l[1] != 'D' {
			return false
		}
	}
	return true
}
