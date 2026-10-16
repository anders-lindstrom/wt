package synccmdtest

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// HandedOver drives the contested fixture to the point a run hands it over:
// v.txt resolved by the declared owned-line strategy and staged, a.txt left
// unmerged for a person, the rebase still in progress, the sidecar written.
func HandedOver(t *testing.T) (ctx *kit.Context, bump, gitDir string, st wtsync.State) {
	t.Helper()
	ctx, bump = provisiontest.ContestedFixture(t)
	gitDir, st = HandOverNow(t, ctx, bump)
	return ctx, bump, gitDir, st
}

// HandOverNow runs sync rebase on bump and returns what it handed over.
func HandOverNow(t *testing.T, ctx *kit.Context, bump string) (gitDir string, st wtsync.State) {
	t.Helper()
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, NoAgents(), &out); err == nil {
		t.Fatalf("the run should have handed over:\n%s", out.String())
	}
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	st, ok, err := wtsync.ReadState(gitDir)
	if err != nil || !ok {
		t.Fatalf("ReadState = %v, %v\n%s", ok, err, out.String())
	}
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the fixture is not mid-rebase", busy, berr)
	}
	return gitDir, st
}

// ResolvedByHand is handedOver with the person's part done: a.txt merged and
// staged, ready for resume.
func ResolvedByHand(t *testing.T) (ctx *kit.Context, bump, gitDir string) {
	t.Helper()
	ctx, bump, gitDir, _ = HandedOver(t)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	return ctx, bump, gitDir
}
