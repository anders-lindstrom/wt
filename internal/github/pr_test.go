package github

import (
	"reflect"
	"testing"
)

func fork(owner, head string) PR {
	return PR{HeadRefName: head, IsCrossRepository: true,
		HeadRepositoryOwner: Account{Login: owner}}
}

// The branch gh puts a pull request on. A fork's branch named after trunk is
// the one gh renames, because the bare name is trunk itself.
func TestLocalBranch(t *testing.T) {
	same := PR{HeadRefName: "fix_wt/tidy"}
	if got := same.LocalBranch("main"); got != "fix_wt/tidy" {
		t.Errorf("LocalBranch() = %q", got)
	}
	if got := fork("rusker86", "argument-sanitize").LocalBranch("master"); got != "argument-sanitize" {
		t.Errorf("LocalBranch() = %q", got)
	}
	if got := fork("rusker86", "master").LocalBranch("master"); got != "rusker86/master" {
		t.Errorf("LocalBranch() = %q", got)
	}
}

// Matching a worktree to a pull request must never claim the main checkout:
// a fork's "master" is not this repository's master.
func TestBranchesNeverClaimTrunk(t *testing.T) {
	for name, tc := range map[string]struct {
		pr    PR
		trunk string
		want  []string
	}{
		"same repo":      {PR{HeadRefName: "fix_wt/tidy"}, "main", []string{"fix_wt/tidy"}},
		"fork":           {fork("them", "their-fix"), "main", []string{"their-fix", "them/their-fix"}},
		"fork off trunk": {fork("them", "main"), "main", []string{"them/main"}},
		"fork, no owner": {PR{HeadRefName: "x", IsCrossRepository: true}, "main", []string{"x"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.pr.Branches(tc.trunk); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Branches() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStateLabel(t *testing.T) {
	for name, tc := range map[string]struct {
		pr   PR
		want string
	}{
		"open":               {PR{State: "OPEN"}, "open"},
		"draft":              {PR{State: "OPEN", IsDraft: true}, "draft"},
		"approved":           {PR{State: "OPEN", ReviewDecision: "APPROVED"}, "approved"},
		"changes":            {PR{State: "OPEN", ReviewDecision: "CHANGES_REQUESTED"}, "changes requested"},
		"review required":    {PR{State: "OPEN", ReviewDecision: "REVIEW_REQUIRED"}, "open"},
		"merged":             {PR{State: "MERGED"}, "merged"},
		"closed":             {PR{State: "CLOSED"}, "closed"},
		"merged beats draft": {PR{State: "MERGED", IsDraft: true}, "merged"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.pr.StateLabel(); got != tc.want {
				t.Errorf("StateLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A rollup mixes CheckRuns, which carry a status and a conclusion, with
// StatusContexts, which carry a state. A skipped or neutral check is not a
// failure.
func TestChecksLabel(t *testing.T) {
	check := func(status, conclusion, state string) Check {
		return Check{Status: status, Conclusion: conclusion, State: state}
	}
	for name, tc := range map[string]struct {
		rollup []Check
		want   string
	}{
		"none":                  {nil, ""},
		"passing":               {[]Check{check("COMPLETED", "SUCCESS", ""), check("COMPLETED", "SKIPPED", "")}, "passing"},
		"failing":               {[]Check{check("COMPLETED", "SUCCESS", ""), check("COMPLETED", "FAILURE", "")}, "failing"},
		"running":               {[]Check{check("IN_PROGRESS", "", ""), check("COMPLETED", "SUCCESS", "")}, "running"},
		"context ok":            {[]Check{check("", "", "SUCCESS")}, "passing"},
		"context pending":       {[]Check{check("", "", "PENDING")}, "running"},
		"context failed":        {[]Check{check("", "", "ERROR")}, "failing"},
		"failing beats running": {[]Check{check("IN_PROGRESS", "", ""), check("COMPLETED", "TIMED_OUT", "")}, "failing"},
	} {
		t.Run(name, func(t *testing.T) {
			pr := PR{StatusCheckRollup: tc.rollup}
			if got := pr.ChecksLabel(); got != tc.want {
				t.Errorf("ChecksLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A branch reused for a second pull request keeps the open one, whichever
// order they came back in.
func TestByBranchKeepsTheOpenOne(t *testing.T) {
	closed := PR{Number: 1, State: "CLOSED", HeadRefName: "fix_wt/tidy"}
	open := PR{Number: 2, State: "OPEN", HeadRefName: "fix_wt/tidy"}
	for name, prs := range map[string][]PR{
		"closed first": {closed, open},
		"open first":   {open, closed},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ByBranch(prs, "main")["fix_wt/tidy"]; got.Number != 2 {
				t.Errorf("ByBranch kept #%d, want #2", got.Number)
			}
		})
	}
}

func TestByBranchIndexesBothForkSpellings(t *testing.T) {
	index := ByBranch([]PR{fork("them", "their-fix")}, "main")
	for _, branch := range []string{"their-fix", "them/their-fix"} {
		if _, ok := index[branch]; !ok {
			t.Errorf("ByBranch has no entry for %q", branch)
		}
	}
}

// Landed is the one fact that lets a squash- or rebase-merged branch be
// swept: GitHub merged it, and the branch is still at exactly the commit it
// merged.
func TestLanded(t *testing.T) {
	const tip = "a1b2c3"
	for name, tc := range map[string]struct {
		pr   PR
		tip  string
		want bool
	}{
		"merged at this tip":        {PR{State: "MERGED", HeadRefOid: tip}, tip, true},
		"merged, lower case":        {PR{State: "merged", HeadRefOid: tip}, tip, true},
		"a commit past the merge":   {PR{State: "MERGED", HeadRefOid: tip}, "d4e5f6", false},
		"still open":                {PR{State: "OPEN", HeadRefOid: tip}, tip, false},
		"closed without merging":    {PR{State: "CLOSED", HeadRefOid: tip}, tip, false},
		"GitHub named no commit":    {PR{State: "MERGED"}, "", false},
		"a branch with no tip read": {PR{State: "MERGED", HeadRefOid: tip}, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.pr.Landed(tc.tip); got != tc.want {
				t.Errorf("Landed(%q) = %v, want %v", tc.tip, got, tc.want)
			}
		})
	}
}
