package github

import (
	"slices"
	"strings"
)

// PR is one pull request, as `gh pr list --json` reports it.
type PR struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
	// BaseRefName is the branch GitHub merged it into, or would. A stacked
	// pull request's base is its parent branch, not trunk.
	BaseRefName string `json:"baseRefName"`
	// HeadRefOid is the last commit GitHub saw on the head branch, which is
	// what says a local branch holds nothing the pull request did not carry.
	HeadRefOid string `json:"headRefOid"`
	IsDraft    bool   `json:"isDraft"`
	// State is OPEN, MERGED or CLOSED.
	State string `json:"state"`
	// ReviewDecision is APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, or
	// empty on a repository that requires no review.
	ReviewDecision string `json:"reviewDecision"`
	// IsCrossRepository says the branch lives in a fork.
	IsCrossRepository bool   `json:"isCrossRepository"`
	URL               string `json:"url"`

	Author              Account `json:"author"`
	HeadRepositoryOwner Account `json:"headRepositoryOwner"`

	// StatusCheckRollup is the checks on the head commit, empty unless they
	// were asked for.
	StatusCheckRollup []Check `json:"statusCheckRollup"`
}

// Account is a GitHub login.
type Account struct {
	Login string `json:"login"`
}

// Check is one entry of a pull request's check rollup. GitHub answers with
// two shapes through one field: a CheckRun, which carries Status and then
// Conclusion, and a StatusContext, which carries State alone.
type Check struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// AuthorLogin is who opened it, or "" when GitHub reports no author (a
// deleted account).
func (p PR) AuthorLogin() string { return p.Author.Login }

// LocalBranch is the branch `gh pr checkout` puts this pull request on: the
// head branch's own name, except that a fork branch named after trunk gets
// the fork owner as a prefix so it cannot shadow trunk. That is gh's rule,
// reproduced because wt needs the branch before the worktree exists.
func (p PR) LocalBranch(trunk string) string {
	if p.IsCrossRepository && p.HeadRefName == trunk && p.HeadRepositoryOwner.Login != "" {
		return p.HeadRepositoryOwner.Login + "/" + p.HeadRefName
	}
	return p.HeadRefName
}

// Branches is every local branch name this pull request could be checked out
// under, best first. A fork's branch is matched under both spellings, since
// gh picks the prefixed one only when the fork has no remote here.
//
// A fork branch named after trunk has the prefixed spelling only: the bare
// one is this repository's own trunk, in the main checkout.
func (p PR) Branches(trunk string) []string {
	if !p.IsCrossRepository || p.HeadRepositoryOwner.Login == "" {
		return []string{p.HeadRefName}
	}
	prefixed := p.HeadRepositoryOwner.Login + "/" + p.HeadRefName
	if p.HeadRefName == trunk {
		return []string{prefixed}
	}
	return []string{p.HeadRefName, prefixed}
}

// Open reports whether the pull request is still open.
func (p PR) Open() bool { return strings.EqualFold(p.State, "OPEN") }

// Merged reports whether GitHub has merged it.
func (p PR) Merged() bool { return strings.EqualFold(p.State, "MERGED") }

// Landed reports that this pull request took a branch still at tip onto one of
// trunks. It is what makes sweeping a squash- or rebase-merged branch safe:
// such a merge rewrites the commits, so git reads the branch as unmerged for
// ever.
//
// Three things have to hold. GitHub merged it. tip is exactly the commit it
// carried, so a commit made after the merge keeps the branch. And its base is
// trunk: a stacked pull request merged into its parent has landed nothing on
// trunk, and deleting that branch would throw the work away.
//
// No base recorded — a cache written before wt read the field — is not landed.
func (p PR) Landed(tip string, trunks ...string) bool {
	if !p.Merged() || tip == "" || tip != p.HeadRefOid || p.BaseRefName == "" {
		return false
	}
	return slices.Contains(trunks, p.BaseRefName)
}

// StateLabel is the pull request's state in one or two words: what it is, and
// for an open one how its review stands.
func (p PR) StateLabel() string {
	switch {
	case strings.EqualFold(p.State, "MERGED"):
		return "merged"
	case strings.EqualFold(p.State, "CLOSED"):
		return "closed"
	case p.IsDraft:
		return "draft"
	}
	switch p.ReviewDecision {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes requested"
	}
	return "open"
}

// ChecksLabel is what the checks on the head commit say: failing, running,
// passing, or "" when there are none or they were not asked for.
func (p PR) ChecksLabel() string {
	running := false
	for _, c := range p.StatusCheckRollup {
		// A CheckRun that has not completed has no conclusion yet; a
		// StatusContext reports the same thing as PENDING.
		if c.Status != "" && !strings.EqualFold(c.Status, "COMPLETED") {
			running = true
			continue
		}
		if strings.EqualFold(c.State, "PENDING") || strings.EqualFold(c.State, "EXPECTED") {
			running = true
			continue
		}
		if failed(c.Conclusion) || failed(c.State) {
			return "failing"
		}
	}
	switch {
	case running:
		return "running"
	case len(p.StatusCheckRollup) > 0:
		return "passing"
	}
	return ""
}

// failed reports whether a check outcome is a bad one. A skipped or neutral
// check is not a failure, and neither is an empty outcome.
func failed(outcome string) bool {
	switch strings.ToUpper(outcome) {
	case "FAILURE", "ERROR", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED":
		return true
	}
	return false
}

// ByBranch indexes pull requests by every branch they could be checked out
// under, taking the best where several contend for a name: this repository's
// own before a fork's, then an open one before a finished one, then whichever
// came first (callers list most recently updated first).
//
// The fork rule comes first because a bare branch name here is this
// repository's branch, whatever a stranger's fork calls its own.
func ByBranch(prs []PR, trunk string) map[string]PR {
	out := make(map[string]PR, len(prs))
	for _, p := range prs {
		for _, b := range p.Branches(trunk) {
			if held, taken := out[b]; taken && !better(p, held) {
				continue
			}
			out[b] = p
		}
	}
	return out
}

// better reports whether p should take a branch's row from held.
func better(p, held PR) bool {
	if p.IsCrossRepository != held.IsCrossRepository {
		return !p.IsCrossRepository
	}
	return p.Open() && !held.Open()
}
