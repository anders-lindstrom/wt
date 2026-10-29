package synccmd_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// mine tracking origin/theirs with nobody to ask: wt does not guess. The
// branch is rebased and not pushed, and theirs is not touched.
func TestABranchTrackingAnotherNameIsNotPushedWithNobodyToAsk(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	const hint = "✗ feat_wt/bump not pushed: it tracks origin/theirs, a branch of another name, and nothing says whether it " +
		"pushes there or to origin/feat_wt/bump: say which with wt sync push-to feat_wt/bump origin/theirs (or origin/feat_wt/bump)"
	for name, mode := range map[string]syncrun.PushMode{"--push": syncrun.PushAlways, "--no-push": syncrun.PushNever, "nobody to ask": syncrun.PushAsk} {
		t.Run(name, func(t *testing.T) {
			ctx, bump := provisiontest.RunFixture(t, false)
			bare := testkit.OwnBareOrigin(t, ctx)
			theirs := synccmd.TracksTheirs(t, ctx, bare)
			before := testkit.GitOut(t, bump, "rev-parse", "HEAD")
			opts := synccmdtest.NoAgents()
			opts.Push = mode
			r, err := synccmdtest.SyncRunJSON(t, ctx, []string{"bump"}, opts)
			if mode == syncrun.PushAlways {
				if err == nil || !strings.Contains(err.Error(), "not completed: bump (not pushed: nowhere to push)") {
					t.Fatalf("a push was asked for and did not happen, yet err is %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if testkit.GitOut(t, bump, "rev-parse", "HEAD") == before {
				t.Fatal("the branch was not rebased")
			}
			if testkit.OnBare(t, bare, "theirs") != theirs || testkit.OnBare(t, bare, "feat_wt/bump") != "" {
				t.Fatalf("theirs %s (was %s), feat_wt/bump %q", testkit.OnBare(t, bare, "theirs"), theirs, testkit.OnBare(t, bare, "feat_wt/bump"))
			}
			if _, ok, err := ctx.Repo.PushTo("feat_wt/bump"); ok || err != nil {
				t.Fatalf("something was recorded: %v %v", ok, err)
			}
			p := r.Worktrees[0]
			if p.Result != syncrun.ResultRebased || p.PushCommand != nil || p.Pushed || p.NoPushReason == nil ||
				*p.NoPushReason != "it tracks origin/theirs, a branch of another name, and nothing says whether it pushes there or to origin/feat_wt/bump" ||
				strings.Join(p.FixCommand, " ") != "wt sync push-to feat_wt/bump origin/theirs" {
				t.Fatalf("participant %+v", p.UpParticipant)
			}
		})
	}
	// The words, once.
	ctx, _ := provisiontest.RunFixture(t, false)
	synccmd.TracksTheirs(t, ctx, testkit.OwnBareOrigin(t, ctx))
	var out bytes.Buffer
	_ = synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.PushAlways(), &out)
	if !strings.Contains(out.String(), hint+"\n"+syncrun.PushedByHand+"\n") {
		t.Fatalf("output lacks %q and what pushes it now:\n%s", hint, out.String())
	}
}

// With a person there, wt asks which of the two it is, once, and records it.
func TestAPersonSaysWhereABranchTrackingAnotherNamePushes(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	run := func(t *testing.T, answer func(syncrun.PushChoice) string) (ctx *kit.Context, bump, bare, theirs string, asked []syncrun.PushChoice, confirmed []string, out string) {
		ctx, bump = provisiontest.RunFixture(t, false)
		bare = testkit.OwnBareOrigin(t, ctx)
		theirs = synccmd.TracksTheirs(t, ctx, bare)
		opts := synccmdtest.NoAgents()
		opts.ChoosePush = func(c syncrun.PushChoice) (string, error) { asked = append(asked, c); return answer(c), nil }
		opts.ConfirmPush = func(works []string) bool { confirmed = works; return true }
		var buf, js bytes.Buffer
		opts.Journal = syncrun.NewSyncRunJournal(&js, "sync rebase")
		if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &buf); err != nil {
			t.Fatalf("err %v\n%s", err, buf.String())
		}
		// What --json says follows the answer, not the state before it.
		if p := synccmdtest.DecodeSyncRun(t, js.Bytes()).Worktrees[0]; p.Pushed != (p.PushCommand != nil) || p.Pushed != (p.NoPushReason == nil) {
			t.Fatalf("participant %+v", p.UpParticipant)
		}
		return ctx, bump, bare, theirs, asked, confirmed, buf.String()
	}
	want := syncrun.PushChoice{Branch: "feat_wt/bump", Upstream: "origin/theirs", Own: "origin/feat_wt/bump"}

	t.Run("its own name", func(t *testing.T) {
		ctx, bump, bare, theirs, asked, confirmed, out := run(t, func(c syncrun.PushChoice) string { return c.Own })
		if len(asked) != 1 || asked[0] != want {
			t.Fatalf("asked %+v", asked)
		}
		if len(confirmed) != 1 || confirmed[0] != "bump" {
			t.Fatalf("the push question named %v", confirmed)
		}
		if testkit.OnBare(t, bare, "theirs") != theirs || testkit.OnBare(t, bare, "feat_wt/bump") != testkit.GitOut(t, bump, "rev-parse", "HEAD") {
			t.Fatalf("theirs %s (was %s), feat_wt/bump %q\n%s", testkit.OnBare(t, bare, "theirs"), theirs, testkit.OnBare(t, bare, "feat_wt/bump"), out)
		}
		if got := testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get", "branch.feat_wt/bump.wtPushTo"); got != "origin feat_wt/bump" {
			t.Fatalf("recorded %q", got)
		}
		if up := testkit.GitOut(t, bump, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/theirs" {
			t.Fatalf("the push rewrote the upstream to %q", up)
		}
	})
	t.Run("the branch it tracks", func(t *testing.T) {
		ctx, bump, bare, _, asked, confirmed, out := run(t, func(c syncrun.PushChoice) string { return c.Upstream })
		if len(asked) != 1 || len(confirmed) != 1 || confirmed[0] != "bump to origin/theirs" {
			t.Fatalf("asked %+v, the push question named %v", asked, confirmed)
		}
		if testkit.OnBare(t, bare, "theirs") != testkit.GitOut(t, bump, "rev-parse", "HEAD") || testkit.OnBare(t, bare, "feat_wt/bump") != "" {
			t.Fatalf("theirs %s, feat_wt/bump %q\n%s", testkit.OnBare(t, bare, "theirs"), testkit.OnBare(t, bare, "feat_wt/bump"), out)
		}
		if got := testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get", "branch.feat_wt/bump.wtPushTo"); got != "origin theirs" {
			t.Fatalf("recorded %q", got)
		}
		// Asked once: the next run knows.
		testkit.GitOut(t, ctx.Repo.MainRoot, "commit", "-q", "--allow-empty", "-m", "trunk moves")
		testkit.GitOut(t, ctx.Repo.MainRoot, "push", "-q", "origin", "main")
		opts := synccmdtest.NoAgents()
		opts.Now = func() time.Time { return time.Unix(0, 199) }
		opts.ChoosePush = func(syncrun.PushChoice) (string, error) { t.Fatal("asked a second time"); return "", nil }
		opts.ConfirmPush = func([]string) bool { return true }
		var buf bytes.Buffer
		if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &buf); err != nil {
			t.Fatalf("err %v\n%s", err, buf.String())
		}
		if testkit.OnBare(t, bare, "theirs") != testkit.GitOut(t, bump, "rev-parse", "HEAD") {
			t.Fatalf("the second run did not push to theirs:\n%s", buf.String())
		}
	})
	t.Run("neither", func(t *testing.T) {
		ctx, _, bare, theirs, asked, confirmed, out := run(t, func(syncrun.PushChoice) string { return "" })
		if len(asked) != 1 || confirmed != nil {
			t.Fatalf("asked %+v, and then asked to push %v", asked, confirmed)
		}
		if testkit.OnBare(t, bare, "theirs") != theirs || testkit.OnBare(t, bare, "feat_wt/bump") != "" {
			t.Fatal("something was pushed")
		}
		if _, ok, _ := ctx.Repo.PushTo("feat_wt/bump"); ok {
			t.Fatal("something was recorded")
		}
		if !strings.Contains(out, "✗ feat_wt/bump not pushed: it tracks origin/theirs") {
			t.Fatalf("out:\n%s", out)
		}
	})
}

// push.default upstream makes the upstream where git pushes. It does not
// make it where wt's forced push goes: a branch cut from a release branch
// tracks it, and is not it.
func TestPushDefaultUpstreamDoesNotMakeTheUpstreamADestination(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.OwnBareOrigin(t, ctx)
	release := synccmd.TracksTheirs(t, ctx, bare)
	testkit.GitOut(t, ctx.Repo.MainRoot, "config", "push.default", "upstream")
	if got := testkit.GitOut(t, bump, "rev-parse", "--abbrev-ref", "@{push}"); got != "origin/theirs" {
		t.Fatalf("the fixture's git push goes to %s", got)
	}
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, synccmdtest.PushAlways(), &out)
	if err == nil || !strings.Contains(err.Error(), "bump (not pushed: nowhere to push)") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if testkit.OnBare(t, bare, "theirs") != release || testkit.OnBare(t, bare, "feat_wt/bump") != "" {
		t.Fatalf("theirs %s (was %s), feat_wt/bump %q\n%s", testkit.OnBare(t, bare, "theirs"), release, testkit.OnBare(t, bare, "feat_wt/bump"), out.String())
	}
}

// A run that fetches, under push.default upstream, with the branch tracking
// trunk: what the remote holds afterwards is the branch under its own name,
// and trunk where it was. git would send a refspec with no colon to trunk.
func TestARunUnderPushDefaultUpstreamPushesTheBranchsOwnNameAndNotTrunk(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.OwnBareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	testkit.GitOut(t, main, "config", "push.default", "upstream")
	testkit.GitOut(t, main, "branch", "-q", "--set-upstream-to=origin/main", "feat_wt/bump")
	if out, _ := gittest.Try(t, bump, "push", "--dry-run", "origin", "feat_wt/bump"); !strings.Contains(out, "feat_wt/bump -> main") {
		t.Fatalf("the fixture is meant to have git map the branch to trunk:\n%s", out)
	}
	trunk := testkit.OnBare(t, bare, "main")
	opts := synccmdtest.PushAlways()
	opts.NoFetch = false
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "(fetched)") {
		t.Fatalf("the run did not fetch:\n%s", out.String())
	}
	if testkit.OnBare(t, bare, "main") != trunk {
		t.Fatalf("the push moved trunk:\n%s", out.String())
	}
	if testkit.OnBare(t, bare, "feat_wt/bump") != testkit.GitOut(t, bump, "rev-parse", "HEAD") {
		t.Fatalf("origin has feat_wt/bump at %q:\n%s", testkit.OnBare(t, bare, "feat_wt/bump"), out.String())
	}
	// The upstream it had is the upstream it has.
	if up := testkit.GitOut(t, bump, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/main" {
		t.Fatalf("upstream %q", up)
	}
}

// wt new cuts a branch from its base and does not make the base its
// upstream: a new branch is not the branch it was cut from.
func TestNewLeavesTheBranchWithNoUpstream(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, _ := provisiontest.RunFixture(t, false)
	bare := testkit.OwnBareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	testkit.GitOut(t, main, "push", "-q", "origin", "main~1:refs/heads/release-1")
	release := testkit.OnBare(t, bare, "release-1")
	for work, base := range map[string]string{"on-release": "origin/release-1", "on-trunk": "origin/main", "on-local": "main"} {
		var out bytes.Buffer
		_, err := provision.New(ctx, "feat/"+work, provision.NewOptions{NoSetup: true, Base: base}, &out)
		if err != nil {
			t.Fatalf("%s: %v\n%s", base, err, out.String())
		}
		if up := ctx.Repo.Upstream("feat_wt/" + work); up != "" {
			t.Errorf("cut from %s, it tracks %s", base, up)
		}
		if _, err := gittest.Try(t, main, "config", "--get-regexp", `^branch\.feat_wt/`+work+`\.`); err == nil {
			t.Errorf("cut from %s, it has configuration of its own", base)
		}
	}
	// So a run pushes it under its own name, and the release branch stays.
	path := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "on-release")
	testkit.WriteFile(t, path, "hotfix.txt", "hotfix\n")
	testkit.GitOut(t, path, "add", "-A")
	testkit.GitOut(t, path, "commit", "-q", "-m", "hotfix")
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"on-release"}, synccmdtest.PushAlways(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if testkit.OnBare(t, bare, "release-1") != release || testkit.OnBare(t, bare, "feat_wt/on-release") != testkit.GitOut(t, path, "rev-parse", "HEAD") {
		t.Fatalf("release-1 %s (was %s), feat_wt/on-release %q", testkit.OnBare(t, bare, "release-1"), release, testkit.OnBare(t, bare, "feat_wt/on-release"))
	}
	if up := testkit.GitOut(t, path, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/feat_wt/on-release" {
		t.Fatalf("after its first push the upstream is %q", up)
	}
}

// Somebody pushed to the recorded branch and nothing here fetched it: git
// refuses on the commit wt pinned, and the run says the push failed.
func TestAPushUnderAnotherNameThatTheLeaseRefusesIsAFailedPush(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, _, bare := provisiontest.RecordedBump(t)
	theirs := testkit.MoveOnRemote(t, bare, "old-name")
	r, err := synccmdtest.SyncRunJSON(t, ctx, []string{"bump"}, synccmdtest.PushAlways())
	if err == nil || !strings.Contains(err.Error(), "bump (push failed)") {
		t.Fatalf("err %v", err)
	}
	if testkit.OnBare(t, bare, "old-name") != theirs || testkit.OnBare(t, bare, "feat_wt/bump") != "" {
		t.Fatalf("old-name %s (theirs %s), feat_wt/bump %q", testkit.OnBare(t, bare, "old-name"), theirs, testkit.OnBare(t, bare, "feat_wt/bump"))
	}
	if p := r.Worktrees[0]; p.Pushed || p.PushCommand == nil || p.NoPushReason != nil {
		t.Fatalf("participant %+v", p.UpParticipant)
	}
}

// A run that fetches, on a branch recorded as pushing to another name: the
// fetch brings a teammate's commit, the check finds it, and the branch takes
// it in before it is rebased and pushed. Theirs is still there afterwards.
func TestARunFetchesChecksAndPushesTheRecordedBranch(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump, bare := provisiontest.RecordedBump(t)
	theirs := testkit.MoveOnRemote(t, bare, "old-name")
	opts := synccmdtest.PushAlways()
	opts.NoFetch = false
	var out bytes.Buffer
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	for _, want := range []string{"fast-forwarded to origin/old-name", "pushing feat_wt/bump to origin/old-name (recorded)", "✓ pushed feat_wt/bump to origin/old-name"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out.String())
		}
	}
	tip := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	if testkit.OnBare(t, bare, "old-name") != tip || testkit.OnBare(t, bare, "feat_wt/bump") != "" {
		t.Fatalf("old-name %s, the branch %s, feat_wt/bump %q", testkit.OnBare(t, bare, "old-name"), tip, testkit.OnBare(t, bare, "feat_wt/bump"))
	}
	if got := testkit.GitOut(t, bump, "log", "--format=%s", "-1", "--grep=^theirs$"); got != "theirs" {
		t.Fatalf("their commit %s is not in what was pushed:\n%s", theirs[:7], testkit.GitOut(t, bump, "log", "--oneline", "-5"))
	}

	// With a commit of its own beside theirs it has diverged: the same run
	// refuses before it rebases, and the remote keeps theirs.
	ctx, bump, bare = provisiontest.RecordedBump(t)
	theirs = testkit.MoveOnRemote(t, bare, "old-name")
	testkit.WriteFile(t, bump, "mine.txt", "mine\n")
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "-m", "mine")
	before := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	out.Reset()
	err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(out.String(), "origin/old-name has 1 commit this branch never had") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if testkit.OnBare(t, bare, "old-name") != theirs || testkit.GitOut(t, bump, "rev-parse", "HEAD") != before {
		t.Fatal("the run rebased or pushed a diverged branch")
	}
}
