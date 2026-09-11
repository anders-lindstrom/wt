package commands

import (
	"fmt"
	"slices"
	"strings"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// parseWork reads a work spec into the type, the work name and the branch the
// two make, with the type checked against the repository's WORKTREE_TYPES.
// Everything that takes a spec reads it here, so nothing has to parse the same
// argument twice to learn a second half of the answer.
func parseWork(ctx *Context, spec string) (typ, work, branch string, err error) {
	typ, work, err = naming.ParseSpec(spec, ctx.Config.DefaultType, ctx.Config.Types)
	if err != nil {
		return "", "", "", err
	}
	if err := checkType(ctx, typ); err != nil {
		return "", "", "", err
	}
	return typ, work, ctx.Scheme().Branch(typ, work), nil
}

// checkType refuses a type this repository does not declare.
func checkType(ctx *Context, typ string) error {
	if slices.Contains(ctx.Config.Types, typ) {
		return nil
	}
	return fmt.Errorf("unknown worktree type %q; expected one of: %s",
		typ, strings.Join(ctx.Config.Types, " "))
}

// Branch returns the branch name for a work spec, validating the type against
// the repository's WORKTREE_TYPES.
func Branch(ctx *Context, spec string) (string, error) {
	_, _, branch, err := parseWork(ctx, spec)
	return branch, err
}

// Path returns where a piece of work lives. An existing worktree on that branch
// wins regardless of its layout, which is what lets worktrees created by other
// tools in other shapes — Superset's included — resolve without migration.
// Otherwise the canonical path is returned, so `new` and `switch` agree on one
// answer.
func Path(ctx *Context, spec string) (string, error) {
	typ, work, branch, err := parseWork(ctx, spec)
	if err != nil {
		return "", err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return "", err
	}
	if w, ok := worktrees.ByBranch(branch); ok {
		return w.Path, nil
	}
	// Nothing on the branch the spec implies. A worktree Superset made carries
	// that tool's one fixed prefix, so reading a type out of the name looks
	// past it: "fix_dev-123" is on feat_wt/fix_dev-123. Try the name as typed
	// before falling back to a path that does not exist yet.
	sch := ctx.Scheme()
	if literal := sch.Branch(ctx.Config.DefaultType, spec); literal != branch {
		if w, ok := worktrees.ByBranch(literal); ok {
			return w.Path, nil
		}
	}
	return sch.Dir(typ, work), nil
}
