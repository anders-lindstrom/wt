package provisiontest

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// OneWorktreeWithAPR is a repository with a worktree on a pull request's head
// branch, and the fake gh's argv log.
func OneWorktreeWithAPR(t *testing.T) (*kit.Context, string) {
	t.Helper()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := testkit.FakeGitHub(t, ctx, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	return ctx, log
}
