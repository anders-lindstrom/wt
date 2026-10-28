package synccmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

func TestSyncRunPushesAFinishedWorktreeWithPush(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	old := testkit.GitOut(t, bump, "rev-parse", "--short=7", "HEAD")
	opts := synccmdtest.NoAgents()
	opts.Push = syncrun.PushAlways
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if want := "✓ pushed feat_wt/bump  " + old + " → " + testkit.GitOut(t, bump, "rev-parse", "--short=7", "HEAD"); !strings.Contains(out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, out.String())
	}
	if !testkit.Pushed(t, bare, bump) {
		t.Fatal("origin does not have the rebased tip")
	}
	if up := testkit.GitOut(t, bump, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/feat_wt/bump" {
		t.Fatalf("upstream %q", up)
	}
}

// Somebody else moved the branch on origin and nothing here fetched it: the
// lease refuses, the run says why and does not complete, origin keeps theirs.
func TestSyncRunDoesNotPushOverCommitsItHasNotSeen(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	testkit.GitOut(t, bump, "push", "-q", "-u", "origin", "feat_wt/bump")
	theirs := testkit.GitOut(t, bump, "rev-parse", "HEAD~1")
	testkit.GitOut(t, bare, "update-ref", "refs/heads/feat_wt/bump", theirs)
	opts := synccmdtest.NoAgents()
	opts.Push = syncrun.PushAlways
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "bump (push failed)") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "✗ push of feat_wt/bump failed: ! [rejected]") {
		t.Fatalf("out %s", out.String())
	}
	if testkit.GitOut(t, bare, "rev-parse", "feat_wt/bump") != theirs {
		t.Fatal("origin's branch was overwritten")
	}
}

func TestSyncRunAsksToPushOnlyWhatFinishedAndPrintsTheCommandOnNo(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	var asked []string
	opts := synccmdtest.NoAgents()
	opts.ConfirmPush = func(works []string) (bool, error) { asked = works; return false, nil }
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump", "other"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	// other is already on trunk: skipped, so there is nothing of it to push.
	if len(asked) != 1 || asked[0] != "bump" {
		t.Fatalf("asked %v", asked)
	}
	s := out.String()
	for _, want := range []string{"1 rebased · 1 skipped", "push --force-with-lease --force-if-includes -u origin refs/heads/feat_wt/bump:refs/heads/feat_wt/bump"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if testkit.Pushed(t, bare, bump) {
		t.Fatal("pushed after no")
	}
}

func TestSyncRunPushesWhenTheAnswerIsYes(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	opts := synccmdtest.NoAgents()
	opts.ConfirmPush = func([]string) (bool, error) { return true, nil }
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !testkit.Pushed(t, bare, bump) {
		t.Fatalf("origin does not have the rebased tip:\n%s", out.String())
	}
}

func TestSyncRunNoPushNeitherAsksNorPushes(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	opts := synccmdtest.NoAgents()
	opts.Push = syncrun.PushNever
	opts.ConfirmPush = func([]string) (bool, error) { t.Error("asked despite --no-push"); return true, nil }
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "push: git -C") {
		t.Fatalf("no push command:\n%s", out.String())
	}
	if testkit.Pushed(t, bare, bump) {
		t.Fatal("pushed despite --no-push")
	}
}

func TestSyncRunPushesNothingThatDidNotFinish(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.ContestedFixture(t)
	bare := testkit.PushOrigin(t, ctx)
	before := testkit.GitOut(t, bare, "rev-parse", "feat_wt/bump")
	opts := synccmdtest.NoAgents()
	opts.Push = syncrun.PushAlways
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err == nil {
		t.Fatalf("the run should have handed over:\n%s", out.String())
	}
	if s := out.String(); strings.Contains(s, "pushed") || strings.Contains(s, "push:") {
		t.Fatalf("a handed-over worktree was offered for push:\n%s", s)
	}
	if testkit.GitOut(t, bare, "rev-parse", "feat_wt/bump") != before {
		t.Fatalf("origin's branch moved; %s is mid-rebase", bump)
	}
}
