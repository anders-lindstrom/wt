package github

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fake writes a shell script as `gh` in its own directory and returns the CLI
// pointing at it. Every call to the real thing goes through argv, so a fake
// that records argv is what proves wt asks GitHub the right question.
func fake(t *testing.T, body string) (CLI, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	exe := filepath.Join(dir, "gh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n" + body + "\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return CLI{Exe: exe}, log
}

func argv(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestFind(t *testing.T) {
	onPath, _ := fake(t, "")
	t.Setenv("PATH", filepath.Dir(onPath.Exe))
	c, ok := Find()
	if !ok || c.Exe != onPath.Exe {
		t.Errorf("Find() = %q, %v; want the fake", c.Exe, ok)
	}

	t.Setenv("PATH", t.TempDir())
	if c, ok := Find(); ok {
		t.Errorf("Find() = %q on a PATH with no gh", c.Exe)
	}
}

// The version line carries the release date and a URL under it; the number
// alone is what a report wants.
func TestVersion(t *testing.T) {
	c, _ := fake(t, `printf 'gh version 2.100.0 (2026-09-03)\nhttps://github.com/cli/cli\n'`)
	if got := c.Version(); got != "2.100.0" {
		t.Errorf("Version() = %q", got)
	}
	broken, _ := fake(t, `exit 1`)
	if got := broken.Version(); got != "" {
		t.Errorf("Version() = %q, want empty", got)
	}
}

func TestAuthenticated(t *testing.T) {
	c, log := fake(t, `exit 0`)
	if err := c.Authenticated("github.com"); err != nil {
		t.Fatal(err)
	}
	if got := argv(t, log); len(got) != 1 || got[0] != "auth status --hostname github.com" {
		t.Errorf("argv = %q", got)
	}
	out, _ := fake(t, `echo "You are not logged into any GitHub hosts." >&2; exit 1`)
	if err := out.Authenticated("github.com"); err == nil {
		t.Error("a logged-out gh reported success")
	} else if !strings.Contains(err.Error(), "not logged into any GitHub hosts") {
		t.Errorf("err = %v, want gh's own complaint", err)
	}
}

// The cheap call `wt list` makes: no statusCheckRollup, which is the
// expensive half, and a state and a limit it chose on purpose.
func TestListArgvIsTheCheapCall(t *testing.T) {
	c, log := fake(t, `echo '[]'`)
	if _, err := c.List(t.TempDir(), ListOptions{State: "all", Limit: 50}); err != nil {
		t.Fatal(err)
	}
	got := argv(t, log)
	if len(got) != 1 {
		t.Fatalf("argv = %q, want one call", got)
	}
	if !strings.HasPrefix(got[0], "pr list --state all --limit 50 --json ") {
		t.Errorf("argv = %q", got[0])
	}
	if strings.Contains(got[0], "statusCheckRollup") {
		t.Errorf("wt list asked for the checks: %q", got[0])
	}
	for _, field := range []string{"number", "headRefName", "isDraft", "state", "reviewDecision", "isCrossRepository"} {
		if !strings.Contains(got[0], field) {
			t.Errorf("argv does not ask for %s: %q", field, got[0])
		}
	}
}

func TestListWithChecksAndHead(t *testing.T) {
	c, log := fake(t, `echo '[]'`)
	if _, err := c.List(t.TempDir(), ListOptions{State: "open", Limit: 100, Checks: true, Head: "fix_wt/x"}); err != nil {
		t.Fatal(err)
	}
	got := argv(t, log)[0]
	if !strings.Contains(got, "statusCheckRollup") {
		t.Errorf("checks were not asked for: %q", got)
	}
	if !strings.HasSuffix(got, "--head fix_wt/x") {
		t.Errorf("argv = %q", got)
	}
}

// Asking for the checks must not leave them in the fields the next call sends:
// the field list is shared, and appending to it in place would poison it.
func TestListDoesNotKeepTheChecksField(t *testing.T) {
	c, log := fake(t, `echo '[]'`)
	if _, err := c.List(t.TempDir(), ListOptions{State: "open", Limit: 10, Checks: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(t.TempDir(), ListOptions{State: "all", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if got := argv(t, log); len(got) != 2 || strings.Contains(got[1], "statusCheckRollup") {
		t.Errorf("second call asked for the checks: %q", got)
	}
}

func TestListParsesPullRequests(t *testing.T) {
	c, _ := fake(t, `echo '[{"number":12,"title":"Tidy","headRefName":"fix_wt/tidy","isDraft":false,`+
		`"state":"OPEN","reviewDecision":"APPROVED","isCrossRepository":false,`+
		`"author":{"login":"anders"},"headRepositoryOwner":{"login":"Telcred"},"url":"u"}]'`)
	prs, err := c.List(t.TempDir(), ListOptions{State: "open", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d pull requests", len(prs))
	}
	p := prs[0]
	if p.Number != 12 || p.HeadRefName != "fix_wt/tidy" || p.AuthorLogin() != "anders" {
		t.Errorf("PR = %+v", p)
	}
	if p.StateLabel() != "approved" {
		t.Errorf("StateLabel() = %q", p.StateLabel())
	}
}

func TestViewArgv(t *testing.T) {
	c, log := fake(t, `echo '{"number":7,"headRefName":"b","state":"MERGED"}'`)
	pr, err := c.View(t.TempDir(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 || pr.StateLabel() != "merged" {
		t.Errorf("PR = %+v", pr)
	}
	if got := argv(t, log); len(got) != 1 || !strings.HasPrefix(got[0], "pr view 7 --json ") {
		t.Errorf("argv = %q", got)
	}
}

// gh runs with the worktree as its working directory, because that is what
// makes it set the branch up there and nowhere else.
func TestCheckoutIntoRunsInTheWorktree(t *testing.T) {
	dir := t.TempDir()
	c, log := fake(t, `pwd >> `+filepath.Join(dir, "cwd"))
	if err := c.CheckoutInto(dir, 12); err != nil {
		t.Fatal(err)
	}
	if got := argv(t, log); len(got) != 1 || got[0] != "pr checkout 12" {
		t.Errorf("argv = %q", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	// macOS spells a temporary directory two ways; the tail is the answer.
	if got := strings.TrimSpace(string(b)); !strings.HasSuffix(got, filepath.Base(dir)) {
		t.Errorf("gh ran in %q, want %q", got, dir)
	}
}

// gh's own complaint is what a person needs to see, not "exit status 1".
func TestRunQuotesGitHubsComplaint(t *testing.T) {
	c, _ := fake(t, `echo "could not determine base repository" >&2; exit 1`)
	_, err := c.List(t.TempDir(), ListOptions{State: "open", Limit: 5})
	if err == nil || !strings.Contains(err.Error(), "could not determine base repository") {
		t.Errorf("err = %v", err)
	}
}

// A gh that never answers must not hold wt open. This is what makes the
// column in `wt list` safe: the deadline is the whole of the guarantee.
func TestRunDeadline(t *testing.T) {
	c, _ := fake(t, `sleep 30`)
	start := time.Now()
	_, err := c.List(t.TempDir(), ListOptions{State: "open", Limit: 5, Deadline: 200 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "did not answer within") {
		t.Fatalf("err = %v, want a deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %s, want the deadline to have cut it short", elapsed)
	}
}

// Broken JSON is an error, not an empty list that would read as "no pull
// requests".
func TestListRejectsRubbish(t *testing.T) {
	c, _ := fake(t, `echo 'not json'`)
	if _, err := c.List(t.TempDir(), ListOptions{State: "open", Limit: 5}); err == nil {
		t.Error("rubbish parsed as an empty list")
	}
}

// The line between "no GitHub is set up here", which is silence, and "GitHub
// was supposed to answer and did not", which is a warning.
func TestNotLoggedIn(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"gh's own advice": {
			errors.New("gh pr list failed: To get started with GitHub CLI, please run:  gh auth login"), true},
		"auth status's wording": {
			errors.New("gh auth status failed: You are not logged into any GitHub hosts"), true},
		// "authentication token" is not a phrase this matches on: gh writes it
		// about a token that is there and short of a scope as well, and
		// silencing that hides a 401 on a machine that is logged in. A gh
		// with no token at all says so in the same breath as `gh auth login`.
		"a token short of a scope": {
			errors.New("gh api graphql failed: HTTP 401: Bad credentials; " +
				"the authentication token is missing required scopes [read:org]"), false},
		"no token, with gh's advice": {
			errors.New("gh api graphql failed: authentication token not found for host " +
				"github.com. Try authenticating with: gh auth login"), true},
		"offline":  {errors.New("gh pr list failed: dial tcp: no route to host"), false},
		"a stall":  {errors.New("gh pr list did not answer within 2s"), false},
		"no error": {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := NotLoggedIn(tc.err); got != tc.want {
				t.Errorf("NotLoggedIn(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
