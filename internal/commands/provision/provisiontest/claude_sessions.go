package provisiontest

import (
	"bytes"
	"io"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// OwnShape is a repository shaped like this one: a long work name, its
// branch, a pull request on it, and two sessions in the worktree, one of
// which needs input.
func OwnShape(t *testing.T) *kit.Context {
	t.Helper()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	testkit.FakeGitHub(t, ctx, testkit.OpenPR(77, "feat_wt/list-sessions", "Sessions in wt list"))
	var errs bytes.Buffer
	wt, err := provision.New(ctx, "feat/list-sessions", provision.NewOptions{}, &errs)
	if err != nil {
		t.Fatal(err)
	}
	testkit.ClaudeLists(ctx,
		testkit.Background("20260930-090330/list-sessions", "aaaaaaaa", "blocked", wt),
		testkit.Background("a second look", "bbbbbbbb", "working", wt))
	return ctx
}
