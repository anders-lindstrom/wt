package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

func noAgentsUndo() UndoOptions {
	return UndoOptions{Agents: []wtsync.Agent{}, Now: func() time.Time { return time.Unix(0, 100) }}
}

func TestSyncUndoPutsBackWhatSyncRunMoved(t *testing.T) {
	ctx, bump := runFixture(t, true)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "log", "-1", "--format=%s") != "chore: regen" {
		t.Fatal("fixture did not produce the regeneration commit; the test is vacuous")
	}

	var undoOut bytes.Buffer
	if err := SyncUndo(ctx, "bump", noAgentsUndo(), &undoOut); err != nil {
		t.Fatalf("err %v\n%s", err, undoOut.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("HEAD not restored to the pre-run tip")
	}
	if gitOut(t, bump, "log", "-1", "--format=%s") == "chore: regen" {
		t.Fatal("regeneration commit still present after undo")
	}
	if !strings.Contains(undoOut.String(), "bump") || !strings.Contains(undoOut.String(), "→") {
		t.Fatalf("out %s", undoOut.String())
	}

	undoOut.Reset()
	if err := SyncUndo(ctx, "bump", noAgentsUndo(), &undoOut); err != nil {
		t.Fatalf("second undo err %v\n%s", err, undoOut.String())
	}
	if !strings.Contains(undoOut.String(), "already at") {
		t.Fatalf("second undo out %s", undoOut.String())
	}
}

func TestSyncUndoRefusesAWorktreeCommittedToSinceTheRun(t *testing.T) {
	ctx, bump := runFixture(t, true)
	var out bytes.Buffer
	if err := SyncRun(ctx, []string{"bump"}, noAgents(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	gitIn(t, bump, "commit", "-q", "--allow-empty", "-m", "after the run")
	after := gitOut(t, bump, "rev-parse", "HEAD")

	var undoOut bytes.Buffer
	err := SyncUndo(ctx, "bump", noAgentsUndo(), &undoOut)
	if err == nil || !strings.Contains(err.Error(), "moved since that run") {
		t.Fatalf("err %v\n%s", err, undoOut.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != after {
		t.Fatal("HEAD moved despite the refusal")
	}

	forced := noAgentsUndo()
	forced.Force = true
	undoOut.Reset()
	if err := SyncUndo(ctx, "bump", forced, &undoOut); err != nil {
		t.Fatalf("forced undo err %v\n%s", err, undoOut.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") == after {
		t.Fatal("forced undo did not rewind")
	}
	if gitOut(t, ctx.Repo.MainRoot, "rev-parse", wtsync.SafetyPrefix+"feat_wt/bump/100") != after {
		t.Fatal("forced undo did not pin the tip it discarded")
	}
}
