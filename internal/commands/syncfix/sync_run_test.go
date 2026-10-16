package syncfix

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A handover that cannot be written leaves the rebase in the worktree with
// nothing to explain it. The run cannot put that right — a restore here
// would be the one thing the handover exists to avoid — but it must say
// where the worktree is and what puts it back.
func TestSyncRunSaysWhereAFailedHandoverLeftTheWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: a sync run over real repositories and worktrees")
	}
	ctx, bump := provisiontest.ContestedFixture(t)
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	// A directory where writeAtomic wants its temp file: the brief cannot be
	// written, and nothing else in the run touches this name.
	if err := os.Mkdir(wtsync.PlanPath(gitDir)+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	old := testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", "feat_wt/bump")
	var out bytes.Buffer
	err = synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.NoAgents(), &out)
	s := out.String()
	if err == nil || !strings.Contains(err.Error(), "bump (failed)") {
		t.Fatalf("err %v\n%s", err, s)
	}
	if !strings.Contains(s, "bump is left mid-rebase with no plan") || !strings.Contains(s, "rebase --abort") {
		t.Fatalf("the run did not say where it left the worktree:\n%s", s)
	}
	// Not wt sync undo: it refuses a mid-rebase worktree, and there is no
	// handover here for it to abort, so naming it would send the person to a
	// command that answers "nothing undone".
	if strings.Contains(s, "wt sync undo") {
		t.Fatalf("the run named a command that will refuse:\n%s", s)
	}
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || !busy {
		t.Fatalf("RebaseInProgress = %v, %v; the run put the rebase back", busy, berr)
	}
	if has, herr := wtsync.HasPlan(gitDir); herr != nil || has {
		t.Fatalf("HasPlan = %v, %v; a handover that failed leaves no marker", has, herr)
	}
	if _, err := os.Stat(wtsync.PlanPath(gitDir)); !os.IsNotExist(err) {
		t.Fatalf("a half-written brief survived: %v", err)
	}
	// The command the line names has to be the one that works. undo does not:
	// it refuses a mid-rebase worktree outright.
	if uerr := SyncUndo(ctx, "bump", UndoOptions{VerbOptions: synccmd.VerbOptions{Agents: []wtsync.Agent{}}}, io.Discard); uerr == nil {
		t.Fatal("undo accepted a mid-rebase worktree; the old wording would have been true")
	}
	testkit.GitOut(t, bump, "rebase", "--abort")
	if busy, berr := wtsync.RebaseInProgress(bump); berr != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v after the abort the line names", busy, berr)
	}
	if got := testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", "feat_wt/bump"); got != old {
		t.Fatalf("the abort left %s at %s, not %s", "feat_wt/bump", got, old)
	}
}
