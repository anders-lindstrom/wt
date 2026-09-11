package commands

import (
	"fmt"
	"strings"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// Branch returns the branch name for a work spec, validating the type against
// the repository's WORKTREE_TYPES.
func Branch(ctx *Context, spec string) (string, error) {
	typ, work, err := naming.ParseSpec(spec, ctx.Config.DefaultType, ctx.Config.Types)
	if err != nil {
		return "", err
	}
	if !typeAllowed(ctx, typ) {
		return "", fmt.Errorf("unknown worktree type %q; expected one of: %s",
			typ, strings.Join(ctx.Config.Types, " "))
	}
	return ctx.Scheme().Branch(typ, work), nil
}

// Path returns where a piece of work lives. An existing worktree on that branch
// wins regardless of its layout, which is what lets worktrees created by other
// tools in other shapes — Superset's included — resolve without migration.
// Otherwise the canonical path is returned, so `new` and `switch` agree on one
// answer.
func Path(ctx *Context, spec string) (string, error) {
	branch, err := Branch(ctx, spec)
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
	typ, work, _ := naming.ParseSpec(spec, ctx.Config.DefaultType, ctx.Config.Types)
	if literal := sch.Branch(ctx.Config.DefaultType, spec); literal != branch {
		if w, ok := worktrees.ByBranch(literal); ok {
			return w.Path, nil
		}
	}
	return sch.Dir(typ, work), nil
}

func typeAllowed(ctx *Context, typ string) bool {
	for _, t := range ctx.Config.Types {
		if t == typ {
			return true
		}
	}
	return false
}
