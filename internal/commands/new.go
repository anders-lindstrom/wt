package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// NewOptions controls worktree creation.
type NewOptions struct {
	Base      string
	SkipBuild bool
	NoSetup   bool
	// NoSuperset leaves the worktree out of the Superset desktop app for this
	// one invocation, whatever SUPERSET_REGISTER says.
	NoSuperset bool
}

// New creates a branch and its worktree at the canonical path, then provisions
// it. It returns the worktree path so a caller can cd there.
func New(ctx *Context, spec string, opts NewOptions, w io.Writer) (string, error) {
	if err := dotOrRoot(spec); err != nil {
		return "", err
	}
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
	return addAndProvision(ctx, path, newAdd(ctx, path, branch, base, w), opts, w, nil)
}

// dotOrRoot refuses "." and "/" as a work name. They name a place that
// exists, as everywhere in wt; read as a work name they would make a branch
// called feat_wt/. or a worktree at <repo>_wt/<type>, and the error for
// either is about the wrong thing.
func dotOrRoot(spec string) error {
	switch {
	case isDot(spec):
		return errors.New(". names the worktree you are in; wt new takes a new <type>/<work>")
	case isRoot(spec):
		return errors.New("/ names the main checkout; wt new takes a new <type>/<work>")
	}
	return nil
}

// newAdd is wt new's add: the branch cut from base, and its worktree.
func newAdd(ctx *Context, path, branch, base string, w io.Writer) func() error {
	return func() error {
		fmt.Fprintf(w, "Creating %s at %s (from %s)\n", branch, path, base)
		return ctx.Repo.AddWorktree(path, branch, base)
	}
}

// addAndProvision is the tail every worktree-creating command shares: a path
// already taken is refused, add puts the worktree there, and it is provisioned
// unless the caller asked for the checkout alone. add announces what it is
// about to do, because only the caller knows what that is.
//
// Setup is left to default SourceDir to the main checkout, which is what these
// callers each passed it by hand.
//
// Superset registration runs after Setup, and runs even when Setup reported a
// problem: the worktree is on disk and on its branch either way, which is all
// Superset is told. It cannot change what this returns.
//
// j records each step for --json; it is nil otherwise.
func addAndProvision(ctx *Context, path string, add func() error, opts NewOptions, w io.Writer, j *CreateJournal) (string, error) {
	// The one line a repository with no configuration gets: which trunk wt
	// took, since that decides what the worktree is cut from.
	if ctx.Config.Detected {
		ctx.Warnf(WarnDetected, "no config file here; on detected defaults (trunk %s) — `wt init` writes one",
			ctx.Config.MainBranch)
	}
	j.start(StepWorktree)
	if _, err := os.Stat(path); err == nil {
		err := fmt.Errorf("%s already exists", path)
		j.finish(StepWorktree, StepFailed, err.Error(), "")
		return "", err
	}
	if err := add(); err != nil {
		j.addFailed(ctx, path, err)
		return "", err
	}
	j.created(ctx, path)
	// --no-setup is "the checkout, nothing else", and Superset is one of the
	// else: it answers a new workspace by running the project's setup script.
	if opts.NoSetup {
		j.skipSetup("--no-setup")
		return path, nil
	}
	err := setup(ctx, path, SetupOptions{SkipBuild: opts.SkipBuild}, w, j)
	if opts.NoSuperset {
		j.finish(StepSuperset, StepSkipped, "--no-superset", "")
		return path, err
	}
	j.start(StepSuperset)
	result, reason := registerSuperset(ctx, path, w)
	j.finish(StepSuperset, result, reason, "")
	return path, err
}
