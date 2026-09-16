package commands

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/anders-lindstrom/wt/internal/find"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// defaultRoots is where repositories live on this machine when WT_ROOTS says
// nothing. Colon-separated, so a path containing spaces survives.
const defaultRoots = "programmering/telcred:programmering/private:Git"

// Roots returns the directories to scan for other repositories.
func Roots() []string {
	raw := os.Getenv("WT_ROOTS")
	if raw == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		var out []string
		for _, rel := range strings.Split(defaultRoots, ":") {
			out = append(out, filepath.Join(home, rel))
		}
		return out
	}
	var out []string
	for _, p := range strings.Split(raw, ":") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Find resolves a fuzzy pattern to the best-matching worktrees.
//
// The current repository is searched first and wins ties, but only a strong hit
// — exact work name, exact branch, or a work-name prefix — ends the search
// there. A vaguer local hit also scans the roots and competes on merit, because
// otherwise an incidental substring beats a real match next door: standing in
// infrastructure, "arch" matches opensearch_ism and would quietly shadow the
// feat_wt/arch worktrees that actually exist elsewhere.
//
// ctx may be nil when the caller is not inside a repository.
func Find(ctx *Context, pattern string) ([]find.Scored, error) {
	var scored []find.Scored

	// "/" is the repository you are in — its main checkout — and so is nothing
	// at all, so `wt cd` with no argument has somewhere to go. "." is the
	// worktree you are standing in, the main checkout included, resolved the
	// way every other command resolves it. Neither searches anything.
	if isRoot(pattern) || pattern == "" || isDot(pattern) {
		if ctx == nil {
			return nil, nil
		}
		wt := repo.Worktree{Path: ctx.Repo.MainRoot, Branch: ctx.Repo.BranchAt(ctx.Repo.MainRoot), IsMain: true}
		if isDot(pattern) {
			names, err := WorkNames(ctx)
			if err != nil {
				return nil, err
			}
			if wt, _, err = standingWorktree(ctx, names); err != nil {
				return nil, err
			}
		}
		return []find.Scored{{
			Candidate: candidate(ctx.Repo, wt, ctx.Config.TypeSuffix, true),
			Tier:      find.TierExactWork,
		}}, nil
	}

	if ctx != nil {
		local, err := candidates(ctx.Repo, ctx.Config.TypeSuffix, true)
		if err != nil {
			return nil, err
		}
		scored = scoreAll(local, pattern)
		if t := find.StrongestTier(scored); t > 0 && t <= find.TierWorkPrefix {
			return find.Best(scored), nil
		}
	}

	seen := map[string]bool{}
	for _, s := range scored {
		seen[s.Path] = true
	}
	for _, r := range scanRoots(Roots()) {
		if ctx != nil && r.MainRoot == ctx.Repo.MainRoot {
			continue
		}
		cands, err := candidates(r, suffixFor(r), false)
		if err != nil {
			continue
		}
		for _, s := range scoreAll(cands, pattern) {
			if !seen[s.Path] {
				seen[s.Path] = true
				scored = append(scored, s)
			}
		}
	}
	return find.Best(scored), nil
}

// suffixFor reads another repository's type suffix, falling back to the default
// when it has no readable configuration — a repo wt does not manage still has
// worktrees worth finding.
func suffixFor(r *repo.Repo) string {
	c, _, err := loadFor(r)
	if err != nil {
		return "_wt"
	}
	return c.TypeSuffix
}

func candidates(r *repo.Repo, suffix string, local bool) ([]find.Candidate, error) {
	worktrees, err := r.Worktrees()
	if err != nil {
		return nil, err
	}
	out := make([]find.Candidate, 0, len(worktrees))
	for _, wt := range worktrees {
		out = append(out, candidate(r, wt, suffix, local))
	}
	return out, nil
}

// candidate is a worktree as the matcher sees it: the main checkout goes by
// the repository's name, a linked worktree by its work name, or by its
// directory when the branch does not carry one.
func candidate(r *repo.Repo, wt repo.Worktree, suffix string, local bool) find.Candidate {
	work := r.Name
	if !wt.IsMain {
		work = naming.StripPrefix(wt.Branch, suffix)
		if work == "" {
			work = filepath.Base(wt.Path)
		}
	}
	return find.Candidate{
		Work:   work,
		Branch: wt.Branch,
		Repo:   r.Name,
		Path:   wt.Path,
		Local:  local,
	}
}

// scoreAll scores every candidate. A pattern containing "/" is tried whole
// first, so a full branch name works; only if nothing matches is it split into
// repo/work, so "personal/arch" disambiguates across repositories.
func scoreAll(cands []find.Candidate, pattern string) []find.Scored {
	var out []find.Scored
	for _, c := range cands {
		if s, ok := find.Score(c, pattern, ""); ok {
			out = append(out, s)
		}
	}
	if len(out) > 0 || !strings.Contains(pattern, "/") {
		return out
	}
	cut := strings.LastIndex(pattern, "/")
	repoPat, workPat := pattern[:cut], pattern[cut+1:]
	for _, c := range cands {
		if s, ok := find.Score(c, workPat, repoPat); ok {
			out = append(out, s)
		}
	}
	return out
}

// scanRoots finds repositories one level under each root. A linked worktree
// that happens to sit at that depth reports its whole repository, so results
// are deduplicated by main-worktree path.
func scanRoots(roots []string) []*repo.Repo {
	seen := map[string]bool{}
	var out []*repo.Repo
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
				continue
			}
			r, err := repo.Discover(dir)
			if err != nil || seen[r.MainRoot] {
				continue
			}
			seen[r.MainRoot] = true
			out = append(out, r)
		}
	}
	return out
}
