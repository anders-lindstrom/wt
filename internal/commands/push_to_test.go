package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The same commit, fetched, and the run told to rebase the diverged branch
// as it stands: wt's own check is what refuses now, with no push command to
// run by hand either.
func TestAPushUnderAnotherNameOverACommitTheBranchNeverHadIsNotMade(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump, bare := provisiontest.RecordedBump(t)
	theirs := testkit.MoveOnRemote(t, bare, "old-name")
	testkit.GitOut(t, ctx.Repo.MainRoot, "fetch", "-q", "origin")
	// Something of its own since the push, or the run would just take theirs
	// in by fast-forward.
	testkit.WriteFile(t, bump, "mine.txt", "mine\n")
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "-m", "mine")
	// Said before any run, in status and its JSON: a reader of ownRemote can
	// tell this branch from one a run would push.
	const why = "origin/old-name has commits this branch never had"
	if o := synccmdtest.PlanOfWork(t, ctx, "bump").Worktree.OwnRemote; o.State != "diverged" || o.NoPushReason == nil || *o.NoPushReason != why || o.FixCommand != nil {
		t.Fatalf("ownRemote %+v", o)
	}
	var status bytes.Buffer
	if err := StatusWorktree(ctx, "bump", StatusOptions{NoPR: true, Agents: []wtsync.Agent{}}, &status); err != nil {
		t.Fatal(err)
	}
	if want := "to origin/old-name, a branch of another name (recorded); a run would not push it as it stands: " + why; !strings.Contains(status.String(), want) {
		t.Fatalf("status lacks %q:\n%s", want, status.String())
	}
	opts := synccmdtest.PushAlways()
	opts.AllowDiverged = true
	var out, human bytes.Buffer
	opts.Journal = synccmd.NewSyncRunJournal(&out, "sync rebase")
	err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &human)
	if err == nil || !strings.Contains(err.Error(), "bump (not pushed: nowhere to push)") {
		t.Fatalf("err %v\n%s", err, human.String())
	}
	if !strings.Contains(human.String(), "✗ feat_wt/bump not pushed: origin/old-name has commits this branch never had") {
		t.Fatalf("out:\n%s", human.String())
	}
	if testkit.OnBare(t, bare, "old-name") != theirs {
		t.Fatal("origin lost their commit")
	}
	p := synccmdtest.DecodeSyncRun(t, out.Bytes()).Worktrees[0]
	if p.Result != synccmd.ResultRebased || p.PushCommand != nil || p.FixCommand != nil || p.NoPushReason == nil ||
		*p.NoPushReason != "origin/old-name has commits this branch never had" {
		t.Fatalf("participant %+v", p.UpParticipant)
	}
}

// Both remote branches exist after pushes of a renamed branch went to its
// new name: wt status says so, and says which one it pushes to.
func TestStatusNamesTheDestinationAndTheStrayBranchOfItsOwnName(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump, _ := provisiontest.RecordedBump(t)
	testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--unset", "branch.feat_wt/bump.wtPushTo")
	testkit.GitOut(t, bump, "push", "-q", "origin", "feat_wt/bump")
	status := func() string {
		t.Helper()
		var out bytes.Buffer
		if err := StatusWorktree(ctx, "bump", StatusOptions{NoPR: true, Agents: []wtsync.Agent{}}, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	s := status()
	for _, want := range []string{
		"remote  nowhere to push yet: it tracks origin/old-name, a branch of another name",
		"say which with wt sync push-to feat_wt/bump origin/old-name (or origin/feat_wt/bump)",
		"also    origin/feat_wt/bump exists as well\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("undecided, status lacks %q:\n%s", want, s)
		}
	}
	p := synccmdtest.PlanOfWork(t, ctx, "bump")
	if o := p.Worktree.OwnRemote; o.State != "unknown" || o.Ref != nil || o.NoPushReason == nil || !strings.HasPrefix(*o.NoPushReason, "it tracks origin/old-name") ||
		strings.Join(o.FixCommand, " ") != "wt sync push-to feat_wt/bump origin/old-name" {
		t.Fatalf("ownRemote %+v", o)
	}
	testkit.GitOut(t, ctx.Repo.MainRoot, "config", "branch.feat_wt/bump.wtPushTo", "origin old-name")
	s = status()
	for _, want := range []string{
		"remote  in sync with origin/old-name (as last fetched)\n",
		"push    to origin/old-name, a branch of another name (recorded)\n",
		"also    origin/feat_wt/bump exists as well: nothing pushes to it, and wt leaves it there\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("recorded, status lacks %q:\n%s", want, s)
		}
	}
	if o := synccmdtest.PlanOfWork(t, ctx, "bump").Worktree.OwnRemote; o.Ref == nil || *o.Ref != "origin/old-name" || o.NoPushReason != nil || o.FixCommand != nil {
		t.Fatalf("ownRemote %+v", o)
	}
}
