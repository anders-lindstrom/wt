package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// failGit puts a git on the PATH that fails the one command whose arguments
// contain match and runs the real git for everything else, so a test can
// break a single call in the middle of a sequence that must otherwise work.
func failGit(t *testing.T, match string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	stub := t.TempDir()
	body := "#!/bin/sh\ncase \"$*\" in\n*'" + match + "'*) echo 'fatal: boom' >&2; exit 128 ;;\nesac\nexec " + gitPath + " \"$@\"\n"
	path := filepath.Join(stub, "git")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	// The first exec of a new file can be slow (macOS vets it); warm it so
	// no deadline under test is spent on that.
	if err := exec.Command(path, "--version").Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Reading the tip of a branch that was just pushed can fail. Returning there
// took the run's whole report with it: the worktrees that did not finish were
// never printed, and the push that did go through was never named either.
func TestOfferPushKeepsTheReportWhenANewTipCannotBeRead(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	// Something for the push to carry: the fixture's origin already holds the
	// branch as it is, so without this the tip never moves and the read under
	// test is not the read of a new tip.
	testkit.WriteFile(t, bump, "p.txt", "push me\n")
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "-m", "ahead of origin")
	// Only the read of the new tip fails; the push before it is the real one.
	failGit(t, "rev-parse --verify feat_wt/bump")

	var out bytes.Buffer
	_, failed, err := offerPush(ctx, &out, PushAlways, pushOptions{}, []pushTarget{{Work: "bump", Branch: "feat_wt/bump", Path: bump}})
	if err != nil {
		t.Fatalf("a tip that could not be read ended the run: %v\n%s", err, out.String())
	}
	if len(failed) != 1 || !strings.Contains(failed[0], "bump") {
		t.Fatalf("failed %v; the run's report must still name it", failed)
	}
	if !strings.Contains(out.String(), "its new tip could not be read") {
		t.Fatalf("out %s", out.String())
	}
	// Named as something that did not come off, but the push itself stands.
	if !testkit.Pushed(t, bare, bump) {
		t.Fatal("the push did not reach origin; the test never read a new tip")
	}
}

func TestSyncRunPushesAFinishedWorktreeWithPush(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	old := testkit.GitOut(t, bump, "rev-parse", "--short=7", "HEAD")
	opts := noAgents()
	opts.Push = PushAlways
	var out bytes.Buffer
	if err := SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
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
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	testkit.GitOut(t, bump, "push", "-q", "-u", "origin", "feat_wt/bump")
	theirs := testkit.GitOut(t, bump, "rev-parse", "HEAD~1")
	testkit.GitOut(t, bare, "update-ref", "refs/heads/feat_wt/bump", theirs)
	opts := noAgents()
	opts.Push = PushAlways
	var out bytes.Buffer
	err := SyncRebase(ctx, []string{"bump"}, opts, &out)
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
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	var asked []string
	opts := noAgents()
	opts.ConfirmPush = func(works []string) (bool, error) { asked = works; return false, nil }
	var out bytes.Buffer
	if err := SyncRebase(ctx, []string{"bump", "other"}, opts, &out); err != nil {
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
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	opts := noAgents()
	opts.ConfirmPush = func([]string) (bool, error) { return true, nil }
	var out bytes.Buffer
	if err := SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !testkit.Pushed(t, bare, bump) {
		t.Fatalf("origin does not have the rebased tip:\n%s", out.String())
	}
}

func TestSyncRunNoPushNeitherAsksNorPushes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	opts := noAgents()
	opts.Push = PushNever
	opts.ConfirmPush = func([]string) (bool, error) { t.Error("asked despite --no-push"); return true, nil }
	var out bytes.Buffer
	if err := SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
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
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.ContestedFixture(t)
	bare := testkit.PushOrigin(t, ctx)
	before := testkit.GitOut(t, bare, "rev-parse", "feat_wt/bump")
	opts := noAgents()
	opts.Push = PushAlways
	var out bytes.Buffer
	if err := SyncRebase(ctx, []string{"bump"}, opts, &out); err == nil {
		t.Fatalf("the run should have handed over:\n%s", out.String())
	}
	if s := out.String(); strings.Contains(s, "pushed") || strings.Contains(s, "push:") {
		t.Fatalf("a handed-over worktree was offered for push:\n%s", s)
	}
	if testkit.GitOut(t, bare, "rev-parse", "feat_wt/bump") != before {
		t.Fatalf("origin's branch moved; %s is mid-rebase", bump)
	}
}

func TestSyncResumePushesWithPush(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump, _, _ := handedOver(t)
	bare := testkit.PushOrigin(t, ctx)
	testkit.WriteFile(t, bump, "a.txt", "merged by hand\n")
	testkit.GitOut(t, bump, "add", "--", "a.txt")
	opts := noResumeAgents()
	opts.Push = PushAlways
	var out bytes.Buffer
	if err := SyncResume(ctx, "bump", opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "✓ pushed feat_wt/bump") {
		t.Fatalf("out %s", out.String())
	}
	if !testkit.Pushed(t, bare, bump) {
		t.Fatal("origin does not have the resumed tip")
	}
}
