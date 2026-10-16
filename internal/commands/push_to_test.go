package commands

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The case this was built for: a branch pushed as own_apikey, renamed into
// the layout by wt migrate, then rebased and pushed. Its pull request is on
// origin/own_apikey, and that is where the push has to land.
func TestAMigratedBranchIsStillPushedToItsOldRemoteBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, _ := provisiontest.RunFixture(t, false)
	bare := testkit.OwnBareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	old := filepath.Join(ctx.Repo.Parent, "elsewhere", "own_apikey")
	testkit.GitOut(t, main, "worktree", "add", "-q", "-b", "own_apikey", old, "main~1")
	testkit.WriteFile(t, old, "key.txt", "key\n")
	testkit.GitOut(t, old, "add", "-A")
	testkit.GitOut(t, old, "commit", "-q", "-m", "own key")
	testkit.GitOut(t, old, "push", "-q", "-u", "origin", "own_apikey")
	pushedAs := testkit.OnBare(t, bare, "own_apikey")

	var out bytes.Buffer
	path, err := Migrate(ctx, "own_apikey", "", noSessions(), &out)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, out.String())
	}
	if want := "✓ branch renamed to feat_wt/own_apikey\n  it still pushes to origin/own_apikey, as it did before the rename\n"; !strings.Contains(out.String(), want) {
		t.Fatalf("the rename does not say where it pushes:\n%s", out.String())
	}
	if got := testkit.GitOut(t, main, "config", "--get", "branch.feat_wt/own_apikey.wtPushTo"); got != "origin own_apikey" {
		t.Fatalf("recorded %q", got)
	}

	out.Reset()
	if err := synccmd.SyncRebase(ctx, []string{"own_apikey"}, synccmdtest.PushAlways(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	tip := testkit.GitOut(t, path, "rev-parse", "HEAD")
	if tip == pushedAs {
		t.Fatal("the fixture did not rebase anything")
	}
	if got := testkit.OnBare(t, bare, "own_apikey"); got != tip {
		t.Fatalf("origin/own_apikey is at %s, the branch at %s:\n%s", got, tip, out.String())
	}
	if got := testkit.OnBare(t, bare, "feat_wt/own_apikey"); got != "" {
		t.Fatalf("the push made a second remote branch, feat_wt/own_apikey at %s", got)
	}
	if up := testkit.GitOut(t, path, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/own_apikey" {
		t.Fatalf("upstream %q", up)
	}
	s := out.String()
	said := strings.Index(s, "pushing feat_wt/own_apikey to origin/own_apikey (recorded)")
	done := strings.Index(s, "✓ pushed feat_wt/own_apikey to origin/own_apikey  "+pushedAs[:7]+" → "+tip[:7])
	if said < 0 || done < 0 || said > done {
		t.Fatalf("the destination is not named before the push and with its result:\n%s", s)
	}
}

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
