// Package repo discovers a repository and its worktrees. Every fact here comes
// from git, never from the shape of a path, which is why worktrees created by
// other tools in other layouts resolve just as well as wt's own.
package repo

import (
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
	Name     string
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
	return &Repo{
		Name:     filepath.Base(main),
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
