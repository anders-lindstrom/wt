package commands

import (
	"fmt"
	"io"
	"path/filepath"
)

// Adopt provisions a worktree somebody else created — plain `git worktree add`,
// a detached agent checkout, or anything made before this repo was migrated.
// With relocate set it is also moved to the canonical path.
func Adopt(ctx *Context, path string, relocate bool, opts SetupOptions, w io.Writer) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return "", err
	}
	known := false
	for _, wt := range worktrees {
		if wt.Path == abs {
			known = true
			break
		}
	}
	if !known {
		return "", fmt.Errorf("%s is not a worktree of %s", abs, ctx.Repo.Name)
	}

	if relocate {
		moved, err := relocateWorktree(ctx, abs, w)
		if err != nil {
			return "", err
		}
		abs = moved
	}
	if opts.Source == "" {
		opts.Source = ctx.Repo.MainRoot
	}
	if err := Setup(ctx, abs, opts, w); err != nil {
		return abs, err
	}
	return abs, nil
}
