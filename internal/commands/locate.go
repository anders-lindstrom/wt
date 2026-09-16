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
// type any column back, "." for the worktree the caller is standing in, and
// "/" for the main checkout, which is refused here as its path is.
// Matching is exact and never leaves this repository: a
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
		return repo.Worktree{}, errMainCheckout
	}
	if !ok {
		return repo.Worktree{}, fmt.Errorf(
			"no worktree %q in %s — run `wt list` to see them", arg, ctx.Repo.Name)
	}
	return wt, nil
}

// errMainCheckout is the answer for the main checkout named as a worktree, by
// its path, by "/" or by "." from inside it.
var errMainCheckout = errors.New("the main checkout is not a worktree; name a worktree (see wt list)")

// existing is the exact matching behind Locate: arg names a worktree by path,
// by branch, by <type>/<work> or by the bare work name, is "." for the one
// the caller is standing in, or "/" for the main checkout. ok is false when
// nothing matches; the error is the one for a work name under more than one
// type, or for a "." from outside every worktree. The main checkout is
// returned, IsMain set, only when its path is given, "/" is said, or "." is
// said from inside it, so each caller decides what that means.
func existing(ctx *Context, arg string) (wt repo.Worktree, ok bool, err error) {
	names, err := WorkNames(ctx)
	if err != nil {
		return repo.Worktree{}, false, err
	}

	// "." is where the caller stands, whatever that directory is called: it
	// is decided before the path rule, which would take "./" for the
	// directory itself and miss from anywhere below the worktree's root.
	if isDot(arg) {
		return standingWorktree(ctx, names)
	}
	// "/" is the main checkout, wherever the caller stands. Decided before the
	// path rule too, which would look for a worktree at the filesystem root.
	if isRoot(arg) {
		for _, n := range names {
			if n.IsMain {
				return n.Worktree, true, nil
			}
		}
		return repo.Worktree{}, false, nil
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

// isDot reports whether arg is ".", spelled with or without the trailing
// separator a shell's completion adds. Nothing at all is not ".", whatever
// Clean makes of it.
func isDot(arg string) bool {
	return arg != "" && (arg == "." || filepath.Clean(arg) == ".")
}

// isRoot reports whether arg is "/", the main checkout by name. Only that
// exact spelling: any other path is a path.
func isRoot(arg string) bool {
	return arg == "/"
}

// standingWorktree is the worktree the caller's directory is inside, the main
// checkout included: the deepest registered path containing it, so a worktree
// nested under another's directory is found rather than its parent. Being
// inside the repository at all does not put a directory inside a worktree
// (a bare repository's parent, say), and that is an error naming the
// directory, since "." meant something and it was not found.
func standingWorktree(ctx *Context, names []WorkName) (repo.Worktree, bool, error) {
	var found *repo.Worktree
	for i := range names {
		n := &names[i].Worktree
		if standingIn(ctx.Cwd, n.Path) && (found == nil || len(n.Path) > len(found.Path)) {
			found = n
		}
	}
	if found == nil {
		return repo.Worktree{}, false, fmt.Errorf(
			". names the worktree you are standing in, and %s is in none of %s's — run `wt list` to see them",
			ctx.Cwd, ctx.Repo.Name)
	}
	return *found, true, nil
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
