package commands

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/anders-lindstrom/wt/internal/naming"
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
// It also does not fall back to the repository's default type. Path() has to,
// because `wt new` names a worktree that does not exist yet; here the worktrees
// are right there to look at, and inventing a type only produces a confident
// answer about the wrong one.
func Locate(ctx *Context, arg string) (repo.Worktree, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return repo.Worktree{}, errors.New("no worktree given")
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return repo.Worktree{}, err
	}

	// A path is tried first and on its own: it identifies a worktree outright,
	// so a name that happens to look like one cannot pull the answer elsewhere.
	if abs, ok := absPath(arg); ok {
		for _, wt := range worktrees {
			if !samePath(wt.Path, abs) {
				continue
			}
			if wt.IsMain {
				return repo.Worktree{}, errors.New("refusing to remove the main checkout")
			}
			return wt, nil
		}
	}

	var matches []repo.Worktree
	for _, wt := range worktrees {
		if !wt.IsMain && matchesName(ctx, wt, arg) {
			matches = append(matches, wt)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		if arg == ctx.Repo.Name {
			return repo.Worktree{}, errors.New("refusing to remove the main checkout")
		}
		return repo.Worktree{}, fmt.Errorf(
			"no worktree %q in %s — run `wt list` to see them", arg, ctx.Repo.Name)
	default:
		return repo.Worktree{}, ambiguous(arg, matches)
	}
}

// matchesName reports whether arg names this worktree by branch, by
// <type>/<work>, or by the bare work name.
func matchesName(ctx *Context, wt repo.Worktree, arg string) bool {
	if wt.Branch == "" {
		return false
	}
	if wt.Branch == arg {
		return true
	}
	typ, work, ok := naming.ParseBranch(wt.Branch, ctx.Config.TypeSuffix)
	return ok && (work == arg || typ+"/"+work == arg)
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

// samePath compares two paths, resolving symlinks only if the plain comparison
// fails — macOS puts temporary directories behind /var -> /private/var, and git
// and the shell do not always agree on which side of it a worktree lives.
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}

// ambiguous builds the error for a work name used under more than one type,
// which is the only ambiguity exact matching can produce.
func ambiguous(arg string, matches []repo.Worktree) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d worktrees; name one exactly:\n", arg, len(matches))
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, m := range matches {
		fmt.Fprintf(tw, "  %s\t%s\n", m.Branch, m.Path)
	}
	_ = tw.Flush()
	return errors.New(strings.TrimRight(b.String(), "\n"))
}
