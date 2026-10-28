package synccmdtest

import (
	"bytes"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// WithWork adds worktree x (feat_wt/x) at trunk as it is now, one commit
// of its own ahead, and returns run options with nobody in any worktree.
func (f *TrunkFixture) WithWork(t *testing.T) synccmd.RunOptions {
	t.Helper()
	var buf bytes.Buffer
	x, err := provision.New(f.Ctx, "feat/x", provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	testkit.WriteIn(t, x, "x.txt", "x\n")
	testkit.GitIn(t, x, "add", "x.txt")
	testkit.GitIn(t, x, "commit", "-q", "-m", "x")
	return synccmd.RunOptions{PushOptions: synccmd.PushOptions{Push: synccmd.PushNever}, VerbOptions: synccmd.VerbOptions{
		Agents: []agents.Agent{},
		Now:    func() time.Time { return time.Unix(0, 99) },
	}}
}

// Declare has origin's trunk declare a .wt-sync.yaml, which wt sync rebase
// needs; the main checkout does not fetch it.
func (f *TrunkFixture) Declare(t *testing.T) {
	t.Helper()
	testkit.GitIn(t, f.Other, "pull", "-q", "--ff-only")
	testkit.WriteIn(t, f.Other, ".wt-sync.yaml", "conflicts: []\n")
	testkit.GitIn(t, f.Other, "add", "-A")
	testkit.GitIn(t, f.Other, "commit", "-q", "-m", "declare")
	testkit.GitIn(t, f.Other, "push", "-q", "origin", "main")
}
