package repo

import "github.com/anders-lindstrom/wt/internal/git"

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
