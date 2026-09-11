package commands

import (
	"fmt"
	"io"
	"os"
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
	typ, work, branch, err := parseWork(ctx, spec)
	if err != nil {
		return "", err
	}
	if ctx.Repo.BranchExists(branch) {
		return "", fmt.Errorf("branch %s already exists", branch)
	}
	path := ctx.Scheme().Dir(typ, work)
	base := opts.Base
	if base == "" {
		base = ctx.Config.MainBranch
	}
	return addAndProvision(ctx, path, func() error {
		fmt.Fprintf(w, "Creating %s at %s (from %s)\n", branch, path, base)
		return ctx.Repo.AddWorktree(path, branch, base)
	}, opts, w)
}

// addAndProvision is the tail every worktree-creating command shares: a path
// already taken is refused, add puts the worktree there, and it is provisioned
// unless the caller asked for the checkout alone. add announces what it is
// about to do, because only the caller knows what that is.
//
// Setup is left to default SourceDir to the main checkout, which is what these
// callers each passed it by hand.
func addAndProvision(ctx *Context, path string, add func() error, opts NewOptions, w io.Writer) (string, error) {
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	if err := add(); err != nil {
		return "", err
	}
	if opts.NoSetup {
		return path, nil
	}
	return path, Setup(ctx, path, SetupOptions{SkipBuild: opts.SkipBuild}, w)
}
