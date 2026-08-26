// Package repo discovers a repository and its worktrees. Every fact here comes
// from git, never from the shape of a path, which is why worktrees created by
// other tools in other layouts resolve just as well as wt's own.
package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
)

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	Branch   string
	Detached bool
	Bare     bool
	IsMain   bool
}

// Repo is the repository containing some directory.
type Repo struct {
	Name string
	// Root is the worktree the caller is standing in. Configuration is read
	// from here, matching the bash implementation's
	// BASE_DIR=$(git rev-parse --show-toplevel): a branch that changes
	// worktree.conf is honoured where it is checked out.
	Root string
	// MainRoot is the repository's primary checkout — the source of the repo
	// name, and where branch and worktree operations run.
	MainRoot string
	Parent   string
}

// Discover locates the repository containing cwd. It gives the same answer from
// the main checkout or any linked worktree.
func Discover(cwd string) (*Repo, error) {
	out, err := git.Run(cwd, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	main := ""
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			main = rest
			break
		}
	}
	if main == "" {
		return nil, git.ErrNotRepo
	}
	root, err := git.Run(cwd, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		root = main
	}
	return &Repo{
		Name:     filepath.Base(main),
		Root:     root,
		MainRoot: main,
		Parent:   filepath.Dir(main),
	}, nil
}

// Worktrees lists every worktree of the repository, main first.
func (r *Repo) Worktrees() ([]Worktree, error) {
	out, err := git.Run(r.MainRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var list []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			cur.IsMain = len(list) == 0
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
			// header noise before the first entry
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(
				strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.Detached = true
		case line == "bare":
			cur.Bare = true
		}
	}
	flush()
	return list, nil
}

// DetectMainBranch reads origin/HEAD, falling back to the checked-out branch.
// This replaces the old hardcoded "development" default, which was correct for
// exactly one of the seven repositories.
func (r *Repo) DetectMainBranch() string {
	if out, err := git.Run(r.MainRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if _, branch, ok := strings.Cut(out, "/"); ok && branch != "" {
			return branch
		}
	}
	// symbolic-ref, not rev-parse --abbrev-ref: the latter fails outright on a
	// repository whose HEAD is unborn, which is exactly the state a freshly
	// initialised repo is in.
	if out, err := git.Run(r.MainRoot, "symbolic-ref", "--short", "HEAD"); err == nil && out != "" {
		return out
	}
	return "main"
}

// BranchAt reports the branch checked out at path, or "" when the worktree is
// detached or unreadable. remove reads the branch from the worktree rather than
// rebuilding it from the work name: once the type varies, the name no longer
// determines the branch, and a reconstructed name may belong to an unrelated
// branch that would then be deleted.
func (r *Repo) BranchAt(path string) string {
	out, err := git.Run(path, "symbolic-ref", "--short", "HEAD")
	if err != nil || out == "HEAD" {
		return ""
	}
	return out
}

// BranchExists reports whether a local branch of that name exists.
func (r *Repo) BranchExists(name string) bool {
	_, err := git.Run(r.MainRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// IsMerged reports whether branch is fully contained in base.
func (r *Repo) IsMerged(branch, base string) bool {
	out, err := git.Run(r.MainRoot, "branch", "--merged", base, "--format=%(refname:short)")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == branch {
			return true
		}
	}
	return false
}

// AddWorktree creates a worktree at path on a new branch cut from base.
func (r *Repo) AddWorktree(path, branch, base string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := git.Run(r.MainRoot, "worktree", "add", "-b", branch, path, base)
	return err
}

// RemoveWorktree deletes a worktree checkout. A worktree containing submodules
// cannot be removed by git worktree remove, so it is deleted directly and the
// registration pruned — the same manual path the bash implementation took.
func (r *Repo) RemoveWorktree(path string) error {
	if r.HasSubmodules(path) {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		return r.Prune()
	}
	if _, err := git.Run(r.MainRoot, "worktree", "remove", path); err != nil {
		return fmt.Errorf("%w (uncommitted changes? try removing it by hand)", err)
	}
	return nil
}

// Prune clears stale worktree registrations.
func (r *Repo) Prune() error {
	_, err := git.Run(r.MainRoot, "worktree", "prune")
	return err
}

// MoveWorktree relocates a worktree checkout.
//
// `git worktree move` behaves like mv: given a destination that already exists
// it moves the worktree *inside* it, so the checkout silently lands one level
// deeper than asked and the caller reports a path that is not where it went.
// Refuse that outright — a non-empty destination means something is already
// there, and quietly nesting a worktree carrying real work is worse than
// failing.
func (r *Repo) MoveWorktree(from, to string) error {
	if entries, err := os.ReadDir(to); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty; refusing to move %s into it", to, from)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if _, err := git.Run(r.MainRoot, "worktree", "move", from, to); err != nil {
		return err
	}
	return nil
}

// DeleteBranch deletes a branch, refusing when it is not merged.
func (r *Repo) DeleteBranch(name string) error {
	_, err := git.Run(r.MainRoot, "branch", "-d", name)
	return err
}

// RenameBranch renames a branch.
func (r *Repo) RenameBranch(from, to string) error {
	_, err := git.Run(r.MainRoot, "branch", "-m", from, to)
	return err
}

// HasSubmodules reports whether the checkout at path declares submodules.
func (r *Repo) HasSubmodules(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".gitmodules"))
	return err == nil
}

// AddExistingWorktree puts a worktree at path on a branch that already exists.
// Unlike AddWorktree it never creates a branch.
func (r *Repo) AddExistingWorktree(path, branch string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := git.Run(r.MainRoot, "worktree", "add", path, branch)
	return err
}
