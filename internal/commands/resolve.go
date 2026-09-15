package commands

import (
	"fmt"
	"slices"
	"strings"

	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
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

// Branch returns the branch for a piece of work. A worktree that already exists
// wins by any name `wt list` prints for it (see existingWork); otherwise the
// spec is parsed and the type validated against the repository's
// WORKTREE_TYPES. A detached worktree, which only its path can name, has no
// branch to print and is refused as locateBranch refuses it.
func Branch(ctx *Context, spec string) (string, error) {
	if wt, ok, err := existingWork(ctx, spec); err != nil || ok {
		if ok && wt.Branch == "" {
			return "", fmt.Errorf("%s has no branch", spec)
		}
		return wt.Branch, err
	}
	_, _, branch, err := parseWork(ctx, spec)
	return branch, err
}

// Path returns where a piece of work lives. A worktree that already exists wins
// by any name `wt list` prints for it (see existingWork), whatever layout it is
// in, which is what lets worktrees created by other tools in other shapes —
// Superset's included — resolve without migration. Otherwise the canonical path
// is returned, so `new` and `switch` agree on one answer.
func Path(ctx *Context, spec string) (string, error) {
	if wt, ok, err := existingWork(ctx, spec); err != nil || ok {
		return wt.Path, err
	}
	typ, work, _, err := parseWork(ctx, spec)
	if err != nil {
		return "", err
	}
	return ctx.Scheme().Dir(typ, work), nil
}

// existingWork is the exact lookup Path and Branch try before reading a type
// out of the spec: the work name, the branch, the path or <type>/<work> of a
// worktree that exists. It runs first so that a bare name is never resolved to
// the default type while a worktree of another type carries it, and so that a
// worktree on a branch outside the declared types still answers, since `wt
// list` printed it. The same lookup keeps a worktree Superset made findable by
// the name it was given: "fix_dev-123" sits on feat_wt/fix_dev-123, and is
// matched by that work name before a type is read out of it. A name under two
// types is refused as ambiguous. The main
// checkout, which only its path can name, is not a piece of work and is left
// to the parse.
func existingWork(ctx *Context, spec string) (repo.Worktree, bool, error) {
	wt, ok, err := existing(ctx, strings.TrimSpace(spec))
	if err != nil || !ok || wt.IsMain {
		return repo.Worktree{}, false, err
	}
	return wt, true, nil
}
