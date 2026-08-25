package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// NewOptions controls worktree creation.
type NewOptions struct {
	Base      string
	SkipBuild bool
	NoSetup   bool
}

// New creates a branch and its worktree at the canonical path, then provisions
// it. It returns the worktree path so a caller can cd there.
func New(ctx *Context, spec string, opts NewOptions, w io.Writer) (string, error) {
	branch, err := Branch(ctx, spec)
	if err != nil {
		return "", err
	}
	typ, work, err := naming.ParseSpec(spec, ctx.Config.DefaultType)
	if err != nil {
		return "", err
	}

	if ctx.Repo.BranchExists(branch) {
		return "", fmt.Errorf("branch %s already exists", branch)
	}
	path := naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, work, ctx.Config.TypeSuffix)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}

	base := opts.Base
	if base == "" {
		base = ctx.Config.MainBranch
	}
	fmt.Fprintf(w, "Creating %s at %s (from %s)\n", branch, path, base)
	if err := ctx.Repo.AddWorktree(path, branch, base); err != nil {
		return "", err
	}

	if opts.NoSetup {
		return path, nil
	}
	if err := Setup(ctx, path, SetupOptions{
		Source:    ctx.Repo.MainRoot,
		SkipBuild: opts.SkipBuild,
	}, w); err != nil {
		return path, err
	}
	return path, nil
}
