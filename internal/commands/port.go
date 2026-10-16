package commands

import (
	"hash/fnv"
	"strconv"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// The ports wt port hands out: above the ports dev servers default to (3000,
// 5173, 8080), and below the ephemeral range.
const (
	portBase = 10000
	portSpan = 10000
)

// PortFor is the port of the worktree with the given key in the repository
// whose main checkout is mainRoot: a hash of the two, so the same worktree
// gets the same port every time and others almost always another. The key is
// what portKey returns: the branch, or the path of a detached worktree.
// Nothing is reserved and nothing is checked; whether it is free is
// devports' question.
func PortFor(mainRoot, key string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(mainRoot))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(key))
	return portBase + int(h.Sum32()%portSpan)
}

// Port is the port for a piece of work, named as `wt path` takes it: a
// worktree that exists by any name `wt list` prints, except the main
// checkout, which only "/" or "." names, or the work `wt new` would create.
// An existing worktree's port follows the branch it is on, as provisioning
// computes it, and a detached one's its path.
func Port(ctx *Context, spec string) (int, error) {
	wt, ok, err := existingWork(ctx, spec)
	switch {
	case err != nil:
		return 0, err
	case ok:
		return PortFor(ctx.Repo.MainRoot, portKey(wt.Branch, wt.Path)), nil
	}
	_, _, branch, err := parseWork(ctx, spec)
	if err != nil {
		return 0, err
	}
	return PortFor(ctx.Repo.MainRoot, branch), nil
}

// portKey is what a worktree's port hashes besides the repository: its
// branch, or its path when it has none.
func portKey(branch, path string) string {
	if branch == "" {
		return path
	}
	return branch
}

// worktreeEnv is what provision.sh and the build command are told about the
// worktree they run in: WT_PORT as `wt port` prints it, WT_BRANCH, and
// WT_WORK as `wt list` names it. Values inherited from the caller are
// replaced, since a shell in another worktree exports that one's.
func worktreeEnv(ctx *Context, target string) []string {
	branch, path, work := ctx.Repo.BranchAt(target), target, ""
	if names, err := WorkNames(ctx); err == nil {
		// The worktree containing target, the deepest if they nest, as wt
		// setup is run from a subdirectory too. Its branch is git's, as wt
		// port reads it, and survives a rebase that has detached HEAD.
		best := -1
		for i, n := range names {
			if repo.Inside(n.Path, target, true) && (best < 0 || len(n.Path) > len(names[best].Path)) {
				best = i
			}
		}
		if best >= 0 {
			n := names[best]
			branch, path, work = n.Branch, n.Path, n.Work
		}
	}
	return []string{
		"WT_PORT=" + strconv.Itoa(PortFor(ctx.Repo.MainRoot, portKey(branch, path))),
		"WT_BRANCH=" + branch,
		"WT_WORK=" + work,
	}
}
