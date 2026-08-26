package commands

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/naming"
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

// MigrateOptions controls relocation.
type MigrateOptions struct {
	// DryRun reports what would happen and changes nothing. Worth reaching for
	// on a worktree carrying work you cannot afford to lose.
	DryRun bool
}

// Migrate moves a worktree to the canonical path without reprovisioning it.
func Migrate(ctx *Context, spec string, opts MigrateOptions, w io.Writer) (string, error) {
	path, err := Path(ctx, spec)
	if err != nil {
		return "", err
	}
	return relocate(ctx, path, opts, w)
}

func relocateWorktree(ctx *Context, path string, w io.Writer) (string, error) {
	return relocate(ctx, path, MigrateOptions{}, w)
}

func relocate(ctx *Context, path string, opts MigrateOptions, w io.Writer) (string, error) {
	branch := ctx.Repo.BranchAt(path)
	typ, work, ok := naming.ParseBranch(branch, ctx.Config.TypeSuffix)
	if !ok {
		return "", fmt.Errorf("%s is on %q, which is not a worktree branch; nothing to migrate to",
			path, branch)
	}
	want := naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, work, ctx.Config.TypeSuffix)
	if want == path {
		fmt.Fprintf(w, "- %s is already at the canonical path\n", path)
		return path, nil
	}
	if opts.DryRun {
		fmt.Fprintf(w, "would move %s\n        -> %s\n", path, want)
		fmt.Fprintln(w, "  (nothing has changed; drop --dry-run to do it)")
		return want, nil
	}

	fmt.Fprintf(w, "Moving %s -> %s\n", path, want)
	if err := ctx.Repo.MoveWorktree(path, want); err != nil {
		return "", err
	}

	// Verify against git rather than trusting the argument: `git worktree move`
	// has surprising destination semantics, and reporting a path the worktree
	// is not actually at is how one goes missing.
	actual, err := worktreePathFor(ctx, ctx.Repo.BranchAt(want))
	if err != nil || actual == "" {
		return want, fmt.Errorf("moved %s, but git no longer reports a worktree there — check `wt list`", path)
	}
	if actual != want {
		return actual, fmt.Errorf("asked git to move to %s but it landed at %s", want, actual)
	}
	fmt.Fprintln(w, "  note: tools holding the old absolute path (Superset, IDE workspaces,")
	fmt.Fprintln(w, "  running dev servers) will need to be pointed at the new one.")
	return want, nil
}

func worktreePathFor(ctx *Context, branch string) (string, error) {
	if branch == "" {
		return "", nil
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return "", err
	}
	for _, wt := range worktrees {
		if wt.Branch == branch {
			return wt.Path, nil
		}
	}
	return "", nil
}
