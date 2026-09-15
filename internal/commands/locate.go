package commands

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// Locate resolves an argument to exactly one worktree that already exists.
//
// It accepts every form `wt list` prints — the work name, the branch, the path —
// plus the <type>/<work> spec, because a user reading a list should be able to
// type any column back. Matching is exact and never leaves this repository: a
// wrong guess by `wt cd` costs a directory change, a wrong guess here costs a
// checkout, so this deliberately does not reuse the fuzzy resolver behind
// `wt find`.
//
// It also does not fall back to the repository's default type. Path() and
// Branch() do, but only after the same exact lookup finds nothing, because `wt
// new` names a worktree that does not exist yet; here the worktrees are right
// there to look at, and inventing a type only produces a confident answer about
// the wrong one.
func Locate(ctx *Context, arg string) (repo.Worktree, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return repo.Worktree{}, errors.New("no worktree given")
	}
	wt, ok, err := existing(ctx, arg)
	if err != nil {
		return repo.Worktree{}, err
	}
	if wt.IsMain || (!ok && arg == ctx.Repo.Name) {
		return repo.Worktree{}, errors.New("the main checkout is not a worktree; name a worktree (see wt list)")
	}
	if !ok {
		return repo.Worktree{}, fmt.Errorf(
			"no worktree %q in %s — run `wt list` to see them", arg, ctx.Repo.Name)
	}
	return wt, nil
}

// existing is the exact matching behind Locate: arg names a worktree by path,
// by branch, by <type>/<work> or by the bare work name. ok is false when
// nothing matches; the error is the one for a work name under more than one
// type. The main checkout is returned, IsMain set, only when its path is given,
// so each caller decides what that means.
func existing(ctx *Context, arg string) (wt repo.Worktree, ok bool, err error) {
	names, err := WorkNames(ctx)
	if err != nil {
		return repo.Worktree{}, false, err
	}

	// A path is tried first and on its own: it identifies a worktree outright,
	// so a name that happens to look like one cannot pull the answer elsewhere.
	if abs, ok := absPath(arg); ok {
		for _, n := range names {
			if repo.SamePath(n.Path, abs) {
				return n.Worktree, true, nil
			}
		}
	}

	var matches []repo.Worktree
	for _, n := range names {
		if !n.IsMain && matchesName(n, arg) {
			matches = append(matches, n.Worktree)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], true, nil
	case 0:
		return repo.Worktree{}, false, nil
	default:
		return repo.Worktree{}, false, ambiguous(arg, matches)
	}
}

// locateBranch is Locate for the verbs that act on a branch: one with none
// is named and refused rather than worked on.
func locateBranch(ctx *Context, arg string) (repo.Worktree, error) {
	wt, err := Locate(ctx, arg)
	if err != nil {
		return repo.Worktree{}, err
	}
	if wt.Branch == "" {
		return repo.Worktree{}, fmt.Errorf("%s has no branch", arg)
	}
	return wt, nil
}

// matchesName reports whether arg names this worktree by branch, by
// <type>/<work>, or by the bare work name.
func matchesName(n WorkName, arg string) bool {
	if n.Branch == "" {
		return false
	}
	if n.Branch == arg {
		return true
	}
	return n.Work != "" && (n.Work == arg || n.Type+"/"+n.Work == arg)
}

// WorkName is a worktree with its branch read against the naming convention.
// Type and Work are empty when the branch does not follow it, or there is no
// branch.
type WorkName struct {
	repo.Worktree
	Type, Work string
}

// WorkNames returns every worktree of the repository, the main checkout
// included, in the order git lists them, each with its type and work name.
func WorkNames(ctx *Context) ([]WorkName, error) {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil, err
	}
	sch := ctx.Scheme()
	names := make([]WorkName, 0, len(worktrees))
	for _, wt := range worktrees {
		n := WorkName{Worktree: wt}
		if typ, work, ok := sch.Parse(wt.Branch); ok {
			n.Type, n.Work = typ, work
		}
		names = append(names, n)
	}
	return names, nil
}

// absPath reports the absolute form of an argument that could be a path. An
// argument with no separator is never treated as one, so a work name is not
// silently resolved against the working directory.
func absPath(arg string) (string, bool) {
	if !strings.ContainsRune(arg, filepath.Separator) {
		return "", false
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", false
	}
	return abs, true
}

// ambiguous builds the error for a work name used under more than one type,
// which is the only ambiguity exact matching can produce.
func ambiguous(arg string, matches []repo.Worktree) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d worktrees; name one exactly:\n", arg, len(matches))
	rows := make([][]string, 0, len(matches))
	for _, m := range matches {
		rows = append(rows, []string{"  " + m.Branch, m.Path})
	}
	_ = printTable(&b, rows)
	return errors.New(strings.TrimRight(b.String(), "\n"))
}
