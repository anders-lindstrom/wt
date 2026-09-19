package github

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// BranchBatch is how many branches go into one call, each as its own aliased
// connection; prsPerBranch is how many pull requests each is asked for, most
// recently updated first. A branch reused after a merge has two or three, so
// five leaves room for a few closed ones without the open one falling off.
const (
	BranchBatch  = 50
	prsPerBranch = 5
)

// prFields is every pull request field wt reads, in GraphQL's spelling. The
// names match the JSON `gh pr list --json` produces, so one PR struct decodes
// both.
const prFields = `number title headRefName baseRefName headRefOid isDraft state reviewDecision ` +
	`isCrossRepository url author{login} headRepositoryOwner{login}`

// PRsOnBranches is every pull request whose head branch is one of heads,
// whatever state it is in, most recently updated first within each branch.
// The question names the branch, so no pull request is too old to find.
//
// heads are head branch names as GitHub knows them; HeadRefNames turns local
// branch names into those. More than BranchBatch of them is several calls.
func (c CLI) PRsOnBranches(dir string, r Remote, heads []string, deadline time.Duration) ([]PR, error) {
	owner, name, ok := strings.Cut(r.Slug, "/")
	if !ok {
		return nil, fmt.Errorf("%q is not an owner/repo", r.Slug)
	}
	var all []PR
	for start := 0; start < len(heads); start += BranchBatch {
		batch := heads[start:min(start+BranchBatch, len(heads))]
		prs, err := c.branchBatch(dir, owner, name, batch, deadline)
		if err != nil {
			return nil, err
		}
		all = append(all, prs...)
	}
	return all, nil
}

// branchBatch asks one query for up to BranchBatch branches, each as an alias.
// Branch names travel as GraphQL variables, never spliced into the query text.
func (c CLI) branchBatch(dir, owner, name string, heads []string, deadline time.Duration) ([]PR, error) {
	var decls, conns strings.Builder
	args := []string{"api", "graphql", "-f", "owner=" + owner, "-f", "repo=" + name,
		"-F", fmt.Sprintf("n=%d", prsPerBranch)}
	for i, head := range heads {
		alias := fmt.Sprintf("b%d", i)
		fmt.Fprintf(&decls, ",$%s:String!", alias)
		fmt.Fprintf(&conns, "%s:pullRequests(headRefName:$%s,first:$n,"+
			"orderBy:{field:UPDATED_AT,direction:DESC}){nodes{%s}} ", alias, alias, prFields)
		args = append(args, "-f", alias+"="+head)
	}
	query := "query($owner:String!,$repo:String!,$n:Int!" + decls.String() + "){" +
		"repository(owner:$owner,name:$repo){" + conns.String() + "}}"
	out, err := c.run(dir, deadline, append(args, "-f", "query="+query)...)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Data struct {
			Repository map[string]struct {
				Nodes []PR `json:"nodes"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		return nil, fmt.Errorf("gh api graphql: %w", err)
	}
	// By alias, not by ranging the map: the order decides which pull request
	// wins a branch that has several, and it has to be the same every run.
	var prs []PR
	for i := range heads {
		prs = append(prs, answer.Data.Repository[fmt.Sprintf("b%d", i)].Nodes...)
	}
	return prs, nil
}

// OpenPRsAndViewer is the repository's open pull requests, most recently
// updated first, with the login gh is authenticated as. One call answers
// both, so the picker can lead with this person's review queue without a
// second round trip.
func (c CLI) OpenPRsAndViewer(dir string, r Remote, limit int, deadline time.Duration) (viewer string, prs []PR, err error) {
	owner, name, ok := strings.Cut(r.Slug, "/")
	if !ok {
		return "", nil, fmt.Errorf("%q is not an owner/repo", r.Slug)
	}
	query := "query($owner:String!,$repo:String!,$n:Int!){viewer{login}" +
		"repository(owner:$owner,name:$repo){pullRequests(states:OPEN,first:$n," +
		"orderBy:{field:UPDATED_AT,direction:DESC}){nodes{" + prFields +
		" reviewRequests(first:20){nodes{requestedReviewer{__typename ... on User{login}}}}}}}}"
	out, err := c.run(dir, deadline, "api", "graphql", "-f", "owner="+owner, "-f", "repo="+name,
		"-F", fmt.Sprintf("n=%d", limit), "-f", "query="+query)
	if err != nil {
		return "", nil, err
	}
	var answer struct {
		Data struct {
			Viewer     Account `json:"viewer"`
			Repository struct {
				PullRequests struct {
					Nodes []PR `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		return "", nil, fmt.Errorf("gh api graphql: %w", err)
	}
	return answer.Data.Viewer.Login, answer.Data.Repository.PullRequests.Nodes, nil
}

// HeadRefNames is the head branch names to ask GitHub about for a set of local
// branches: the same names, plus one.
//
// gh checks a fork's branch out under its own name except when that name is
// trunk's, where it prefixes the fork owner. A local `someone/main` is
// therefore a pull request GitHub calls `main`, so both spellings are asked
// about. Duplicates dropped, order kept.
func HeadRefNames(branches []string, trunk string) []string {
	seen := make(map[string]bool, len(branches))
	out := make([]string, 0, len(branches))
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, b := range branches {
		add(b)
		if owner, rest, ok := strings.Cut(b, "/"); ok && owner != "" && rest == trunk {
			add(rest)
		}
	}
	return out
}
