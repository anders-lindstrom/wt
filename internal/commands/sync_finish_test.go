package commands

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The finish's input is built under the tracker, already set to rebased:
// an interrupt while the paths to check are collected names the rebased
// worktree, as one during the deferred steps does. The tracker is clear
// again once the finish has returned.
func TestCompleteRunBuildsItsInputUnderTheRebasedTracker(t *testing.T) {
	ctx, bump := runFixture(t, false)
	trunk := gitOut(t, ctx.Repo.MainRoot, "rev-parse", "origin/main")
	branch := gitOut(t, bump, "rev-parse", "--abbrev-ref", "HEAD")
	cfg, err := wtsync.LoadFromRef(ctx.Repo.MainRoot, trunk)
	if err != nil {
		t.Fatal(err)
	}
	res, err := wtsync.Rebase(ctx.Repo.MainRoot, cfg, wtsync.Request{Path: bump, Branch: branch, Trunk: trunk, Onto: trunk, Epoch: 7, Work: "bump"}, nil)
	if err != nil || res.Left != nil || res.Restored {
		t.Fatalf("rebase %+v err %v", res, err)
	}
	tracker := &rebaseTracker{}
	var atBuild *rebaseInFlight
	var out bytes.Buffer
	head, owed, err := completeRun(ctx, &out, cfg, tracker, "bump", bump, func() completeInput {
		atBuild = tracker.get()
		return completeInput{Branch: branch, Epoch: 7, Res: res, TrunkName: "main", Landed: 1}
	})
	if err != nil || len(owed) != 0 {
		t.Fatalf("owed %v err %v\n%s", owed, err, out.String())
	}
	if atBuild == nil || !atBuild.rebased || atBuild.work != "bump" || atBuild.path != bump {
		t.Fatalf("the input was built with the tracker at %+v", atBuild)
	}
	if at := tracker.get(); at != nil {
		t.Fatalf("the tracker still reads %+v after the finish", at)
	}
	if head != gitOut(t, bump, "rev-parse", "HEAD") {
		t.Fatalf("head %s", head)
	}
}
