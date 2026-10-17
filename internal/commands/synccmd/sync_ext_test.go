package synccmd_test

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A handed-over worktree's staged resolutions are what a person is
// finishing, not dirt: its row says it is waiting, and nothing else.
func TestSyncShowsAHandedOverWorktreeWithoutCallingItDirty(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.ContestedFixture(t)
	synccmdtest.HandOverNow(t, ctx, bump)
	var buf bytes.Buffer
	if err := synccmd.Sync(ctx, synccmd.SyncOptions{NoFetch: true}, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	block := synccmd.SyncBlock(t, buf.String(), "bump")
	// Both commands, as Preflight and doctor name them: resume refuses a
	// handover the person aborted by hand, and undo ends that one.
	if !strings.Contains(block, "contested") || !strings.Contains(block, wtsync.WayOut(wtsync.Way{Plan: true, Rebasing: true})) {
		t.Fatalf("bump block %q:\n%s", block, buf.String())
	}
	if strings.Contains(block, "dirty") {
		t.Fatalf("a handover is called dirty: %q", block)
	}
	if strings.Index(buf.String(), "needs you") > strings.Index(buf.String(), "  bump ") {
		t.Errorf("a handover is filed under needs you:\n%s", buf.String())
	}
}

// A handed-over row keeps what a person returning cold needs: the stop and
// its total, the subject of the commit being replayed, the file that is
// theirs, then the way out. Before this the row said only that the worktree
// was left mid-rebase, and everything the same row had shown before the run
// was gone.
func TestSyncKeepsTheStopAndFileOnAHandedOverRow(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.ContestedFixture(t)
	_, st := synccmdtest.HandOverNow(t, ctx, bump)
	var buf bytes.Buffer
	if err := synccmd.Sync(ctx, synccmd.SyncOptions{NoFetch: true}, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	block := synccmd.SyncBlock(t, buf.String(), "bump")
	want := fmt.Sprintf("at %d/%d %q  a.txt✗ v.txt✓  waits on you: %s", st.Stop, st.Total, "bump", wtsync.WayOut(wtsync.Way{Plan: true, Rebasing: true}))
	if !strings.Contains(block, want) {
		t.Fatalf("want %q in the bump block:\n%s", want, block)
	}
}

// A handover whose rebase somebody finished with git alone is not left
// mid-rebase any more: the row says it was finished by hand and that
// resume runs what is left, and the detail view's run line says the same.
// One aborted by hand is called that, and sent to undo.
func TestSyncShowsAFinishedByHandHandoverAsSuch(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.ContestedFixture(t)
	synccmdtest.HandOverNow(t, ctx, bump)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	testkit.GitTry(t, bump, "rebase", "--continue")
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; the hand continue did not finish", busy, err)
	}
	var buf bytes.Buffer
	if err := synccmd.Sync(ctx, synccmd.SyncOptions{NoFetch: true}, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	block := synccmd.SyncBlock(t, buf.String(), "bump")
	want := wtsync.WayOut(wtsync.Way{Plan: true, Finished: true})
	if !strings.Contains(block, want) || !strings.Contains(want, "finished by hand") {
		t.Fatalf("want %q in the bump block:\n%s", want, block)
	}
	for _, not := range []string{"left mid-rebase", "waits on you"} {
		if strings.Contains(block, not) {
			t.Fatalf("a finished rebase is still %q:\n%s", not, block)
		}
	}
	buf.Reset()
	if err := synccmd.SyncWorktree(ctx, "bump", synccmd.SyncOptions{NoFetch: true}, &buf); err != nil {
		t.Fatalf("SyncWorktree: %v", err)
	}
	if !strings.Contains(buf.String(), "\n  run     refused: "+want+"\n") {
		t.Fatalf("the run line does not say finished by hand:\n%s", buf.String())
	}
	// The stop is not live any more: the plan line stays, the stop
	// listing with its "yours" files does not.
	if !strings.Contains(buf.String(), "\n  plan    ") || strings.Contains(buf.String(), "handed over at") {
		t.Fatalf("the detail view lists a finished stop as live, or lost the plan line:\n%s", buf.String())
	}

	ctx, bump = provisiontest.ContestedFixture(t)
	synccmdtest.HandOverNow(t, ctx, bump)
	testkit.GitOut(t, bump, "rebase", "--abort")
	buf.Reset()
	if err := synccmd.Sync(ctx, synccmd.SyncOptions{NoFetch: true}, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	block = synccmd.SyncBlock(t, buf.String(), "bump")
	want = wtsync.WayOut(wtsync.Way{Plan: true, Aborted: true})
	if !strings.Contains(block, want) || !strings.Contains(want, "aborted") || !strings.Contains(want, "wt sync undo") {
		t.Fatalf("want %q in the bump block:\n%s", want, block)
	}
}

// A handover finished by hand with a commit made inside it is one resume
// will refuse, so the row and doctor must not send a person to resume:
// the branch moved, and the forced undo is the way out.
func TestSyncShowsAHandoverFinishedWithACommitInsideAsMoved(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.ContestedFixture(t)
	synccmdtest.HandOverNow(t, ctx, bump)
	testkit.GitOut(t, ctx.Repo.MainRoot, "config", "core.hooksPath", filepath.Join(ctx.Repo.MainRoot, ".git", "hooks"))
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	testkit.GitOut(t, bump, "commit", "-q", "-m", "resolved by hand")
	testkit.GitOut(t, bump, "commit", "-q", "--allow-empty", "-m", "inside the rebase")
	testkit.GitTry(t, bump, "rebase", "--continue")
	if busy, err := wtsync.RebaseInProgress(bump); err != nil || busy {
		t.Fatalf("RebaseInProgress = %v, %v; the hand continue did not finish", busy, err)
	}
	var buf bytes.Buffer
	if err := synccmd.Sync(ctx, synccmd.SyncOptions{NoFetch: true}, &buf); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	block := synccmd.SyncBlock(t, buf.String(), "bump")
	if want := wtsync.WayOut(wtsync.Way{Plan: true, Moved: true}); !strings.Contains(block, want) || strings.Contains(block, "finished by hand") {
		t.Fatalf("want %q and not finished by hand in the bump block:\n%s", want, block)
	}
	buf.Reset()
	if err := synccmd.SyncDoctor(ctx, synccmd.DoctorOptions{}, &buf); err != nil {
		t.Fatalf("doctor: %v\n%s", err, buf.String())
	}
	if want := "bump: " + wtsync.WayOut(wtsync.Way{Work: "bump", Path: bump, Plan: true, Moved: true}); !strings.Contains(buf.String(), want) {
		t.Fatalf("want %q in doctor's plan row:\n%s", want, buf.String())
	}
}

// wt sync <work> is what the overview points at for the detail, so
// mid-handover it must at least name the plan file and the files that are
// the person's, not only the run's refusal.
func TestSyncDetailNamesThePlanAndTheYoursFilesMidHandover(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.ContestedFixture(t)
	gitDir, _ := synccmdtest.HandOverNow(t, ctx, bump)
	var buf bytes.Buffer
	if err := synccmd.SyncWorktree(ctx, "bump", synccmd.SyncOptions{NoFetch: true}, &buf); err != nil {
		t.Fatalf("SyncWorktree: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"\n  plan    " + wtsync.PlanPath(gitDir) + "\n",
		"\nhanded over at 1/1  bump\n",
		"\n    ✗ a.txt  yours\n",
		"\n    ✓ v.txt  resolved by owned-line\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}
