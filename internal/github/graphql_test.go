package github

import (
	"fmt"
	"strings"
	"testing"
)

var demo = Remote{Name: "origin", Host: "github.com", Slug: "t/demo"}

// The whole point of the branch query: the question names the branch, so an
// answer cannot depend on how recently the pull request was touched.
func TestPRsOnBranchesAsksByBranch(t *testing.T) {
	c, log := fake(t, `echo '{"data":{"repository":{"b0":{"nodes":[{"number":7,"headRefName":"old-work","state":"OPEN"}]},"b1":{"nodes":[]}}}}'`)
	prs, err := c.PRsOnBranches(t.TempDir(), demo, []string{"old-work", "nothing-here"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 7 {
		t.Fatalf("prs = %+v", prs)
	}
	got := argv(t, log)
	if len(got) != 1 {
		t.Fatalf("argv = %q, want one call for both branches", got)
	}
	for _, want := range []string{"api graphql", "owner=t", "repo=demo",
		"b0=old-work", "b1=nothing-here", "headRefName:$b0", "headRefName:$b1",
		"UPDATED_AT", "baseRefName"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("argv does not carry %q: %q", want, got[0])
		}
	}
	if strings.Contains(got[0], "--limit") {
		t.Errorf("the branch query still lists by recency: %q", got[0])
	}
}

// A branch name travels as a GraphQL variable, never spliced into the query,
// so nothing a branch is called can change what is asked.
func TestPRsOnBranchesSendsBranchesAsVariables(t *testing.T) {
	c, log := fake(t, `echo '{"data":{"repository":{"b0":{"nodes":[]}}}}'`)
	nasty := `x"){nodes{number}} evil:pullRequests(headRefName:"y`
	if _, err := c.PRsOnBranches(t.TempDir(), demo, []string{nasty}, 0); err != nil {
		t.Fatal(err)
	}
	got := argv(t, log)[0]
	query := got[strings.Index(got, "query=query("):]
	if strings.Contains(query, "evil:") {
		t.Errorf("a branch name reached the query text: %q", query)
	}
}

// More branches than one call carries is several calls, and every branch is
// still asked about.
func TestPRsOnBranchesBatches(t *testing.T) {
	c, log := fake(t, `echo '{"data":{"repository":{}}}'`)
	branches := make([]string, BranchBatch+3)
	for i := range branches {
		branches[i] = fmt.Sprintf("work-%d", i)
	}
	if _, err := c.PRsOnBranches(t.TempDir(), demo, branches, 0); err != nil {
		t.Fatal(err)
	}
	got := argv(t, log)
	if len(got) != 2 {
		t.Fatalf("argv = %d calls, want 2", len(got))
	}
	for _, b := range branches {
		if !strings.Contains(got[0]+got[1], "="+b+" ") && !strings.HasSuffix(got[0], "="+b) {
			if !strings.Contains(got[0]+" "+got[1], "="+b) {
				t.Errorf("branch %q was never asked about", b)
			}
		}
	}
}

// The answers come back in the order the branches were asked in, so which
// pull request wins a branch with several is the same on every run. GraphQL
// gives an object, and ranging a Go map would not be.
func TestPRsOnBranchesKeepsTheOrderAsked(t *testing.T) {
	c, _ := fake(t, `echo '{"data":{"repository":{`+
		`"b0":{"nodes":[{"number":1},{"number":2}]},`+
		`"b1":{"nodes":[{"number":3}]},`+
		`"b2":{"nodes":[{"number":4}]}}}}'`)
	prs, err := c.PRsOnBranches(t.TempDir(), demo, []string{"a", "b", "c"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var numbers []int
	for _, p := range prs {
		numbers = append(numbers, p.Number)
	}
	if fmt.Sprint(numbers) != "[1 2 3 4]" {
		t.Errorf("numbers = %v", numbers)
	}
}

func TestPRsOnBranchesAsksNothingForNoBranches(t *testing.T) {
	c, log := fake(t, `echo 'unexpected'`)
	prs, err := c.PRsOnBranches(t.TempDir(), demo, nil, 0)
	if err != nil || prs != nil {
		t.Fatalf("prs = %v, err = %v", prs, err)
	}
	if got := argv(t, log); len(got) != 0 {
		t.Errorf("gh ran for no branches: %q", got)
	}
}

func TestPRsOnBranchesRejectsRubbish(t *testing.T) {
	c, _ := fake(t, `echo 'not json'`)
	if _, err := c.PRsOnBranches(t.TempDir(), demo, []string{"a"}, 0); err == nil {
		t.Error("rubbish parsed as an empty answer")
	}
}

// One call answers both halves of the picker's question: who gh is logged in
// as, and who has been asked to review what.
func TestOpenPRsAndViewer(t *testing.T) {
	c, log := fake(t, `echo '{"data":{"viewer":{"login":"anders"},"repository":{"pullRequests":{"nodes":[`+
		`{"number":9,"headRefName":"fix-login","state":"OPEN","reviewRequests":{"nodes":[{"requestedReviewer":{"login":"Anders"}}]}},`+
		`{"number":8,"headRefName":"other","state":"OPEN","reviewRequests":{"nodes":[]}}]}}}}'`)
	viewer, prs, err := c.OpenPRsAndViewer(t.TempDir(), demo, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if viewer != "anders" {
		t.Errorf("viewer = %q", viewer)
	}
	if len(prs) != 2 {
		t.Fatalf("prs = %+v", prs)
	}
	if !prs[0].WantsReviewFrom("anders") {
		t.Error("#9 asks anders to review it and did not say so")
	}
	if prs[1].WantsReviewFrom("anders") {
		t.Error("#8 asks nobody and said it did")
	}
	if got := argv(t, log); len(got) != 1 || !strings.Contains(got[0], "viewer{login}") {
		t.Errorf("argv = %q", got)
	}
}

// A login nobody is is nobody: an empty viewer must not match an empty
// requested reviewer.
func TestWantsReviewFromNobody(t *testing.T) {
	var p PR
	p.ReviewRequests.Nodes = append(p.ReviewRequests.Nodes, struct {
		RequestedReviewer Account `json:"requestedReviewer"`
	}{})
	if p.WantsReviewFrom("") {
		t.Error("an empty login matched an empty reviewer")
	}
}

// gh checks a fork's branch out under its own name unless that name is
// trunk's, where it prefixes the owner. A local someone/main is therefore a
// pull request GitHub calls main, and both have to be asked about.
func TestHeadRefNames(t *testing.T) {
	got := HeadRefNames([]string{"fix_wt/login", "someone/main", "main", "fix_wt/login"}, "main")
	want := "[fix_wt/login someone/main main]"
	if fmt.Sprint(got) != want {
		t.Errorf("HeadRefNames = %v, want %s", got, want)
	}
}

func TestPRsOnBranchesNeedsAnOwnerAndRepo(t *testing.T) {
	c, _ := fake(t, `echo '{}'`)
	if _, err := c.PRsOnBranches(t.TempDir(), Remote{Slug: "nonsense"}, []string{"a"}, 0); err == nil {
		t.Error("a slug that is not owner/repo was accepted")
	}
}
