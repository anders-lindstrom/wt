package admin

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
)

// Adopt provisions a worktree somebody else created — plain `git worktree add`,
// a detached agent checkout, or anything made before this repo was migrated.
// With relocate set it is also moved to the canonical path.
func Adopt(ctx *kit.Context, path string, relocate bool, opts provision.SetupOptions, w io.Writer) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return "", err
	}
	// Once matched, git's own spelling of the path is the one that travels on,
	// so setup and the printed path name the worktree the way every other
	// command does.
	wt, known := worktrees.ByPath(abs)
	if !known {
		return "", fmt.Errorf("%s is not a worktree of %s", abs, ctx.Repo.Name)
	}
	abs = wt.Path

	if relocate {
		moved, err := relocateWorktree(ctx, abs, w)
		if err != nil {
			return "", err
		}
		abs = moved
	}
	return abs, provision.Setup(ctx, abs, opts, w)
}
