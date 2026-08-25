// Package find matches a worktree by a fuzzy pattern.
//
// Matching is tiered rather than one blended score, so the result is
// predictable: a user can reason about why a pattern chose what it chose.
// Comparison is case-insensitive and uses substring containment, never a
// regular expression, so a pattern full of dots and brackets is still just
// text.
package find

import "strings"

// Candidate is one worktree that a pattern may match.
type Candidate struct {
	Work   string
	Branch string
	Repo   string
	Path   string
	Local  bool // in the repository the user is standing in
}

// Scored is a Candidate that matched, with the tier it matched at.
type Scored struct {
	Candidate
	Tier int
}

// Tiers, best first.
const (
	TierExactWork = iota + 1
	TierExactBranch
	TierWorkPrefix
	TierWorkSubstring
	TierBranchOrPath
	TierSubsequence
)

// minSubsequence is the shortest pattern allowed to reach the subsequence tier.
// Below it almost every pattern matches almost every candidate.
const minSubsequence = 3

// Score rates one candidate against a pattern, optionally restricted to repos
// whose name contains repoPattern.
func Score(c Candidate, pattern, repoPattern string) (Scored, bool) {
	p := strings.ToLower(pattern)
	if p == "" {
		return Scored{}, false
	}
	if repoPattern != "" && !strings.Contains(strings.ToLower(c.Repo), strings.ToLower(repoPattern)) {
		return Scored{}, false
	}

	work := strings.ToLower(c.Work)
	branch := strings.ToLower(c.Branch)
	path := strings.ToLower(c.Path)

	var tier int
	switch {
	case work == p:
		tier = TierExactWork
	case branch == p:
		tier = TierExactBranch
	case strings.HasPrefix(work, p):
		tier = TierWorkPrefix
	case strings.Contains(work, p):
		tier = TierWorkSubstring
	case strings.Contains(branch, p), strings.Contains(path, p):
		tier = TierBranchOrPath
	case len(p) >= minSubsequence && isSubsequence(work, p):
		tier = TierSubsequence
	default:
		return Scored{}, false
	}
	return Scored{Candidate: c, Tier: tier}, true
}

// Best returns every candidate sharing the winning (tier, locality) pair. The
// current repository wins ties, which is the whole of "repo-first": it decides
// nothing when another repo matched better.
func Best(scored []Scored) []Scored {
	if len(scored) == 0 {
		return nil
	}
	bestTier, bestLocal := 0, false
	for _, s := range scored {
		better := bestTier == 0 ||
			s.Tier < bestTier ||
			(s.Tier == bestTier && s.Local && !bestLocal)
		if better {
			bestTier, bestLocal = s.Tier, s.Local
		}
	}
	var out []Scored
	for _, s := range scored {
		if s.Tier == bestTier && s.Local == bestLocal {
			out = append(out, s)
		}
	}
	return out
}

// StrongestTier reports the best tier present, or 0 for an empty set.
func StrongestTier(scored []Scored) int {
	best := 0
	for _, s := range scored {
		if best == 0 || s.Tier < best {
			best = s.Tier
		}
	}
	return best
}

func isSubsequence(s, pattern string) bool {
	j := 0
	for i := 0; i < len(s) && j < len(pattern); i++ {
		if s[i] == pattern[j] {
			j++
		}
	}
	return j == len(pattern)
}
