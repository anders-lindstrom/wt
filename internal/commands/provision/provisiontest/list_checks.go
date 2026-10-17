package provisiontest

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
)

// WithChecks is pr with its last commit's checks summed up as state, the way
// the branch query reads them; "" is a commit with no checks.
func WithChecks(pr github.PR, state string) github.PR {
	pr.CheckState = state
	return pr
}

// FourWorktreesWithChecks is a repository with a worktree on each of four
// pull requests: checks passing, failing, running, and none at all.
func FourWorktreesWithChecks(t *testing.T) (*kit.Context, string) {
	t.Helper()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t, ctx,
		WithChecks(testkit.OpenPR(12, "fix_wt/passing", "Passing"), "SUCCESS"),
		WithChecks(testkit.OpenPR(13, "fix_wt/failing", "Failing"), "FAILURE"),
		WithChecks(testkit.OpenPR(14, "fix_wt/running", "Running"), "PENDING"),
		WithChecks(testkit.OpenPR(15, "fix_wt/unchecked", "Unchecked"), ""),
	)
	for _, work := range []string{"fix/passing", "fix/failing", "fix/running", "fix/unchecked"} {
		var errs bytes.Buffer
		if _, err := provision.New(ctx, work, provision.NewOptions{}, &errs); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, log
}
