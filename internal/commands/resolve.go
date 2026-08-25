package commands

import (
	"fmt"
	"strings"

	"github.com/anders-lindstrom/wt/internal/naming"
)

// Branch returns the branch name for a work spec, validating the type against
// the repository's WORKTREE_TYPES.
func Branch(ctx *Context, spec string) (string, error) {
	typ, work, err := naming.ParseSpec(spec, ctx.Config.DefaultType)
	if err != nil {
		return "", err
	}
	if !typeAllowed(ctx, typ) {
		return "", fmt.Errorf("unknown worktree type %q; expected one of: %s",
			typ, strings.Join(ctx.Config.Types, " "))
	}
	return naming.BranchName(typ, work, ctx.Config.TypeSuffix), nil
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
	for _, w := range worktrees {
		if w.Branch == branch {
			return w.Path, nil
		}
	}
	typ, work, _ := naming.ParseSpec(spec, ctx.Config.DefaultType)
	return naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, work, ctx.Config.TypeSuffix), nil
}

func typeAllowed(ctx *Context, typ string) bool {
	for _, t := range ctx.Config.Types {
		if t == typ {
			return true
		}
	}
	return false
}
