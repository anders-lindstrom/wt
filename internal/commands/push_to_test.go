package commands

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// ownBareOrigin makes origin a bare repository of its own that holds trunk and
// nothing else: the fixture's branches are unpushed, and every push, fetch
// and lease goes to a real remote.
func ownBareOrigin(t *testing.T, ctx *Context) string {
	t.Helper()
	main := ctx.Repo.MainRoot
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitOut(t, main, "init", "-q", "--bare", bare)
	gitOut(t, main, "push", "-q", bare, "refs/remotes/origin/main:refs/heads/main")
	gitOut(t, main, "remote", "set-url", "origin", bare)
	for _, ref := range strings.Fields(gitOut(t, main, "for-each-ref", "--format=%(refname)", "refs/remotes/origin")) {
		if ref != "refs/remotes/origin/main" {
			gitOut(t, main, "update-ref", "--no-deref", "-d", ref)
		}
	}
	return bare
}

// onBare is the commit the bare remote has branch at, "" when it has none.
func onBare(t *testing.T, bare, branch string) string {
	t.Helper()
	out, err := gittest.Try(t, bare, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// tracksTheirs puts a branch theirs on the remote, at the commit bump was
// cut from, and makes the fixture's bump track it: git checkout -b
// feat_wt/bump origin/theirs, as far as the configuration goes.
func tracksTheirs(t *testing.T, ctx *Context, bare string) string {
	t.Helper()
	main := ctx.Repo.MainRoot
	gitOut(t, main, "push", "-q", "origin", "main~1:refs/heads/theirs")
	gitOut(t, main, "branch", "-q", "--set-upstream-to=origin/theirs", "feat_wt/bump")
	return onBare(t, bare, "theirs")
}

func pushAlways() RunOptions {
	opts := noAgents()
	opts.Push = PushAlways
	return opts
}

// The case this was built for: a branch pushed as own_apikey, renamed into
// the layout by wt migrate, then rebased and pushed. Its pull request is on
// origin/own_apikey, and that is where the push has to land.
func TestAMigratedBranchIsStillPushedToItsOldRemoteBranch(t *testing.T) {
	ctx, _ := runFixture(t, false)
	bare := ownBareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	old := filepath.Join(ctx.Repo.Parent, "elsewhere", "own_apikey")
	gitOut(t, main, "worktree", "add", "-q", "-b", "own_apikey", old, "main~1")
	writeFile(t, old, "key.txt", "key\n")
	gitOut(t, old, "add", "-A")
	gitOut(t, old, "commit", "-q", "-m", "own key")
	gitOut(t, old, "push", "-q", "-u", "origin", "own_apikey")
	pushedAs := onBare(t, bare, "own_apikey")

	var out bytes.Buffer
	path, err := Migrate(ctx, "own_apikey", "", noSessions(), &out)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, out.String())
	}
	if want := "✓ branch renamed to feat_wt/own_apikey\n  it still pushes to origin/own_apikey, as it did before the rename\n"; !strings.Contains(out.String(), want) {
		t.Fatalf("the rename does not say where it pushes:\n%s", out.String())
	}
	if got := gitOut(t, main, "config", "--get", "branch.feat_wt/own_apikey.wtPushTo"); got != "origin own_apikey" {
		t.Fatalf("recorded %q", got)
	}

	out.Reset()
	if err := SyncRebase(ctx, []string{"own_apikey"}, pushAlways(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	tip := gitOut(t, path, "rev-parse", "HEAD")
	if tip == pushedAs {
		t.Fatal("the fixture did not rebase anything")
	}
	if got := onBare(t, bare, "own_apikey"); got != tip {
		t.Fatalf("origin/own_apikey is at %s, the branch at %s:\n%s", got, tip, out.String())
	}
	if got := onBare(t, bare, "feat_wt/own_apikey"); got != "" {
		t.Fatalf("the push made a second remote branch, feat_wt/own_apikey at %s", got)
	}
	if up := gitOut(t, path, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/own_apikey" {
		t.Fatalf("upstream %q", up)
	}
	s := out.String()
	said := strings.Index(s, "pushing feat_wt/own_apikey to origin/own_apikey (recorded)")
	done := strings.Index(s, "✓ pushed feat_wt/own_apikey to origin/own_apikey  "+pushedAs[:7]+" → "+tip[:7])
	if said < 0 || done < 0 || said > done {
		t.Fatalf("the destination is not named before the push and with its result:\n%s", s)
	}
}

// mine tracking origin/theirs with nobody to ask: wt does not guess. The
// branch is rebased and not pushed, and theirs is not touched.
func TestABranchTrackingAnotherNameIsNotPushedWithNobodyToAsk(t *testing.T) {
	const hint = "✗ feat_wt/bump not pushed: it tracks origin/theirs, a branch of another name, and nothing says whether it " +
		"pushes there or to origin/feat_wt/bump: say which with wt sync push-to feat_wt/bump origin/theirs (or origin/feat_wt/bump)"
	for name, mode := range map[string]PushMode{"--push": PushAlways, "--no-push": PushNever, "nobody to ask": PushAsk} {
		t.Run(name, func(t *testing.T) {
			ctx, bump := runFixture(t, false)
			bare := ownBareOrigin(t, ctx)
			theirs := tracksTheirs(t, ctx, bare)
			before := gitOut(t, bump, "rev-parse", "HEAD")
			opts := noAgents()
			opts.Push = mode
			r, err := syncRunJSON(t, ctx, []string{"bump"}, opts)
			if mode == PushAlways {
				if err == nil || !strings.Contains(err.Error(), "not completed: bump (not pushed: nowhere to push)") {
					t.Fatalf("a push was asked for and did not happen, yet err is %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if gitOut(t, bump, "rev-parse", "HEAD") == before {
				t.Fatal("the branch was not rebased")
			}
			if onBare(t, bare, "theirs") != theirs || onBare(t, bare, "feat_wt/bump") != "" {
				t.Fatalf("theirs %s (was %s), feat_wt/bump %q", onBare(t, bare, "theirs"), theirs, onBare(t, bare, "feat_wt/bump"))
			}
			if _, ok, err := ctx.Repo.PushTo("feat_wt/bump"); ok || err != nil {
				t.Fatalf("something was recorded: %v %v", ok, err)
			}
			p := r.Worktrees[0]
			if p.Result != ResultRebased || p.PushCommand != nil || p.Pushed || p.NoPushReason == nil ||
				*p.NoPushReason != "it tracks origin/theirs, a branch of another name, and nothing says whether it pushes there or to origin/feat_wt/bump" ||
				strings.Join(p.FixCommand, " ") != "wt sync push-to feat_wt/bump origin/theirs" {
				t.Fatalf("participant %+v", p.UpParticipant)
			}
		})
	}
	// The words, once.
	ctx, _ := runFixture(t, false)
	tracksTheirs(t, ctx, ownBareOrigin(t, ctx))
	var out bytes.Buffer
	_ = SyncRebase(ctx, []string{"bump"}, pushAlways(), &out)
	if !strings.Contains(out.String(), hint+"\n"+pushedByHand+"\n") {
		t.Fatalf("output lacks %q and what pushes it now:\n%s", hint, out.String())
	}
}

// With a person there, wt asks which of the two it is, once, and records it.
func TestAPersonSaysWhereABranchTrackingAnotherNamePushes(t *testing.T) {
	run := func(t *testing.T, answer func(PushChoice) string) (ctx *Context, bump, bare, theirs string, asked []PushChoice, confirmed []string, out string) {
		ctx, bump = runFixture(t, false)
		bare = ownBareOrigin(t, ctx)
		theirs = tracksTheirs(t, ctx, bare)
		opts := noAgents()
		opts.ChoosePush = func(c PushChoice) (string, error) { asked = append(asked, c); return answer(c), nil }
		opts.ConfirmPush = func(works []string) (bool, error) { confirmed = works; return true, nil }
		var buf, js bytes.Buffer
		opts.Journal = NewSyncRunJournal(&js, "sync rebase")
		if err := SyncRebase(ctx, []string{"bump"}, opts, &buf); err != nil {
			t.Fatalf("err %v\n%s", err, buf.String())
		}
		// What --json says follows the answer, not the state before it.
		if p := decodeSyncRun(t, js.Bytes()).Worktrees[0]; p.Pushed != (p.PushCommand != nil) || p.Pushed != (p.NoPushReason == nil) {
			t.Fatalf("participant %+v", p.UpParticipant)
		}
		return ctx, bump, bare, theirs, asked, confirmed, buf.String()
	}
	want := PushChoice{Branch: "feat_wt/bump", Upstream: "origin/theirs", Own: "origin/feat_wt/bump"}

	t.Run("its own name", func(t *testing.T) {
		ctx, bump, bare, theirs, asked, confirmed, out := run(t, func(c PushChoice) string { return c.Own })
		if len(asked) != 1 || asked[0] != want {
			t.Fatalf("asked %+v", asked)
		}
		if len(confirmed) != 1 || confirmed[0] != "bump" {
			t.Fatalf("the push question named %v", confirmed)
		}
		if onBare(t, bare, "theirs") != theirs || onBare(t, bare, "feat_wt/bump") != gitOut(t, bump, "rev-parse", "HEAD") {
			t.Fatalf("theirs %s (was %s), feat_wt/bump %q\n%s", onBare(t, bare, "theirs"), theirs, onBare(t, bare, "feat_wt/bump"), out)
		}
		if got := gitOut(t, ctx.Repo.MainRoot, "config", "--get", "branch.feat_wt/bump.wtPushTo"); got != "origin feat_wt/bump" {
			t.Fatalf("recorded %q", got)
		}
		if up := gitOut(t, bump, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/theirs" {
			t.Fatalf("the push rewrote the upstream to %q", up)
		}
	})
	t.Run("the branch it tracks", func(t *testing.T) {
		ctx, bump, bare, _, asked, confirmed, out := run(t, func(c PushChoice) string { return c.Upstream })
		if len(asked) != 1 || len(confirmed) != 1 || confirmed[0] != "bump to origin/theirs" {
			t.Fatalf("asked %+v, the push question named %v", asked, confirmed)
		}
		if onBare(t, bare, "theirs") != gitOut(t, bump, "rev-parse", "HEAD") || onBare(t, bare, "feat_wt/bump") != "" {
			t.Fatalf("theirs %s, feat_wt/bump %q\n%s", onBare(t, bare, "theirs"), onBare(t, bare, "feat_wt/bump"), out)
		}
		if got := gitOut(t, ctx.Repo.MainRoot, "config", "--get", "branch.feat_wt/bump.wtPushTo"); got != "origin theirs" {
			t.Fatalf("recorded %q", got)
		}
		// Asked once: the next run knows.
		gitOut(t, ctx.Repo.MainRoot, "commit", "-q", "--allow-empty", "-m", "trunk moves")
		gitOut(t, ctx.Repo.MainRoot, "push", "-q", "origin", "main")
		opts := noAgents()
		opts.Now = func() time.Time { return time.Unix(0, 199) }
		opts.ChoosePush = func(PushChoice) (string, error) { t.Fatal("asked a second time"); return "", nil }
		opts.ConfirmPush = func([]string) (bool, error) { return true, nil }
		var buf bytes.Buffer
		if err := SyncRebase(ctx, []string{"bump"}, opts, &buf); err != nil {
			t.Fatalf("err %v\n%s", err, buf.String())
		}
		if onBare(t, bare, "theirs") != gitOut(t, bump, "rev-parse", "HEAD") {
			t.Fatalf("the second run did not push to theirs:\n%s", buf.String())
		}
	})
	t.Run("neither", func(t *testing.T) {
		ctx, _, bare, theirs, asked, confirmed, out := run(t, func(PushChoice) string { return "" })
		if len(asked) != 1 || confirmed != nil {
			t.Fatalf("asked %+v, and then asked to push %v", asked, confirmed)
		}
		if onBare(t, bare, "theirs") != theirs || onBare(t, bare, "feat_wt/bump") != "" {
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
	ctx, bump := runFixture(t, false)
	bare := ownBareOrigin(t, ctx)
	release := tracksTheirs(t, ctx, bare)
	gitOut(t, ctx.Repo.MainRoot, "config", "push.default", "upstream")
	if got := gitOut(t, bump, "rev-parse", "--abbrev-ref", "@{push}"); got != "origin/theirs" {
		t.Fatalf("the fixture's git push goes to %s", got)
	}
	var out bytes.Buffer
	err := SyncRebase(ctx, []string{"bump"}, pushAlways(), &out)
	if err == nil || !strings.Contains(err.Error(), "bump (not pushed: nowhere to push)") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if onBare(t, bare, "theirs") != release || onBare(t, bare, "feat_wt/bump") != "" {
		t.Fatalf("theirs %s (was %s), feat_wt/bump %q\n%s", onBare(t, bare, "theirs"), release, onBare(t, bare, "feat_wt/bump"), out.String())
	}
}

// A run that fetches, under push.default upstream, with the branch tracking
// trunk: what the remote holds afterwards is the branch under its own name,
// and trunk where it was. git would send a refspec with no colon to trunk.
func TestARunUnderPushDefaultUpstreamPushesTheBranchsOwnNameAndNotTrunk(t *testing.T) {
	ctx, bump := runFixture(t, false)
	bare := ownBareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	gitOut(t, main, "config", "push.default", "upstream")
	gitOut(t, main, "branch", "-q", "--set-upstream-to=origin/main", "feat_wt/bump")
	if out, _ := gittest.Try(t, bump, "push", "--dry-run", "origin", "feat_wt/bump"); !strings.Contains(out, "feat_wt/bump -> main") {
		t.Fatalf("the fixture is meant to have git map the branch to trunk:\n%s", out)
	}
	trunk := onBare(t, bare, "main")
	opts := pushAlways()
	opts.NoFetch = false
	var out bytes.Buffer
	if err := SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "(fetched)") {
		t.Fatalf("the run did not fetch:\n%s", out.String())
	}
	if onBare(t, bare, "main") != trunk {
		t.Fatalf("the push moved trunk:\n%s", out.String())
	}
	if onBare(t, bare, "feat_wt/bump") != gitOut(t, bump, "rev-parse", "HEAD") {
		t.Fatalf("origin has feat_wt/bump at %q:\n%s", onBare(t, bare, "feat_wt/bump"), out.String())
	}
	// The upstream it had is the upstream it has.
	if up := gitOut(t, bump, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/main" {
		t.Fatalf("upstream %q", up)
	}
}

// wt new cuts a branch from its base and does not make the base its
// upstream: a new branch is not the branch it was cut from.
func TestNewLeavesTheBranchWithNoUpstream(t *testing.T) {
	ctx, _ := runFixture(t, false)
	bare := ownBareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	gitOut(t, main, "push", "-q", "origin", "main~1:refs/heads/release-1")
	release := onBare(t, bare, "release-1")
	for work, base := range map[string]string{"on-release": "origin/release-1", "on-trunk": "origin/main", "on-local": "main"} {
		var out bytes.Buffer
		_, err := New(ctx, "feat/"+work, NewOptions{NoSetup: true, Base: base}, &out)
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
	writeFile(t, path, "hotfix.txt", "hotfix\n")
	gitOut(t, path, "add", "-A")
	gitOut(t, path, "commit", "-q", "-m", "hotfix")
	var out bytes.Buffer
	if err := SyncRebase(ctx, []string{"on-release"}, pushAlways(), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if onBare(t, bare, "release-1") != release || onBare(t, bare, "feat_wt/on-release") != gitOut(t, path, "rev-parse", "HEAD") {
		t.Fatalf("release-1 %s (was %s), feat_wt/on-release %q", onBare(t, bare, "release-1"), release, onBare(t, bare, "feat_wt/on-release"))
	}
	if up := gitOut(t, path, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/feat_wt/on-release" {
		t.Fatalf("after its first push the upstream is %q", up)
	}
}

// recordedBump is the fixture's bump pushed as origin/old-name and recorded
// as pushing there: a renamed branch.
func recordedBump(t *testing.T) (ctx *Context, bump, bare string) {
	t.Helper()
	ctx, bump = runFixture(t, false)
	bare = ownBareOrigin(t, ctx)
	gitOut(t, bump, "push", "-q", "-u", "origin", "feat_wt/bump:old-name")
	gitOut(t, ctx.Repo.MainRoot, "config", "branch.feat_wt/bump.wtPushTo", "origin old-name")
	return ctx, bump, bare
}

// moveOnRemote puts a commit on the remote's branch from another clone:
// somebody else's push.
func moveOnRemote(t *testing.T, bare, branch string) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	gitOut(t, bare, "clone", "-q", "--branch", branch, bare, other)
	gitOut(t, other, "config", "user.email", "o@example.com")
	gitOut(t, other, "config", "user.name", "O")
	writeFile(t, other, "theirs.txt", "theirs\n")
	gitOut(t, other, "add", "-A")
	gitOut(t, other, "commit", "-q", "-m", "theirs")
	gitOut(t, other, "push", "-q", "origin", branch)
	return gitOut(t, other, "rev-parse", "HEAD")
}

// Somebody pushed to the recorded branch and nothing here fetched it: git
// refuses on the commit wt pinned, and the run says the push failed.
func TestAPushUnderAnotherNameThatTheLeaseRefusesIsAFailedPush(t *testing.T) {
	ctx, _, bare := recordedBump(t)
	theirs := moveOnRemote(t, bare, "old-name")
	r, err := syncRunJSON(t, ctx, []string{"bump"}, pushAlways())
	if err == nil || !strings.Contains(err.Error(), "bump (push failed)") {
		t.Fatalf("err %v", err)
	}
	if onBare(t, bare, "old-name") != theirs || onBare(t, bare, "feat_wt/bump") != "" {
		t.Fatalf("old-name %s (theirs %s), feat_wt/bump %q", onBare(t, bare, "old-name"), theirs, onBare(t, bare, "feat_wt/bump"))
	}
	if p := r.Worktrees[0]; p.Pushed || p.PushCommand == nil || p.NoPushReason != nil {
		t.Fatalf("participant %+v", p.UpParticipant)
	}
}

// A run that fetches, on a branch recorded as pushing to another name: the
// fetch brings a teammate's commit, the check finds it, and the branch takes
// it in before it is rebased and pushed. Theirs is still there afterwards.
func TestARunFetchesChecksAndPushesTheRecordedBranch(t *testing.T) {
	ctx, bump, bare := recordedBump(t)
	theirs := moveOnRemote(t, bare, "old-name")
	opts := pushAlways()
	opts.NoFetch = false
	var out bytes.Buffer
	if err := SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	for _, want := range []string{"fast-forwarded to origin/old-name", "pushing feat_wt/bump to origin/old-name (recorded)", "✓ pushed feat_wt/bump to origin/old-name"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out.String())
		}
	}
	tip := gitOut(t, bump, "rev-parse", "HEAD")
	if onBare(t, bare, "old-name") != tip || onBare(t, bare, "feat_wt/bump") != "" {
		t.Fatalf("old-name %s, the branch %s, feat_wt/bump %q", onBare(t, bare, "old-name"), tip, onBare(t, bare, "feat_wt/bump"))
	}
	if got := gitOut(t, bump, "log", "--format=%s", "-1", "--grep=^theirs$"); got != "theirs" {
		t.Fatalf("their commit %s is not in what was pushed:\n%s", theirs[:7], gitOut(t, bump, "log", "--oneline", "-5"))
	}

	// With a commit of its own beside theirs it has diverged: the same run
	// refuses before it rebases, and the remote keeps theirs.
	ctx, bump, bare = recordedBump(t)
	theirs = moveOnRemote(t, bare, "old-name")
	writeFile(t, bump, "mine.txt", "mine\n")
	gitOut(t, bump, "add", "-A")
	gitOut(t, bump, "commit", "-q", "-m", "mine")
	before := gitOut(t, bump, "rev-parse", "HEAD")
	out.Reset()
	err := SyncRebase(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(out.String(), "origin/old-name has 1 commit this branch never had") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if onBare(t, bare, "old-name") != theirs || gitOut(t, bump, "rev-parse", "HEAD") != before {
		t.Fatal("the run rebased or pushed a diverged branch")
	}
}

// The same commit, fetched, and the run told to rebase the diverged branch
// as it stands: wt's own check is what refuses now, with no push command to
// run by hand either.
func TestAPushUnderAnotherNameOverACommitTheBranchNeverHadIsNotMade(t *testing.T) {
	ctx, bump, bare := recordedBump(t)
	theirs := moveOnRemote(t, bare, "old-name")
	gitOut(t, ctx.Repo.MainRoot, "fetch", "-q", "origin")
	// Something of its own since the push, or the run would just take theirs
	// in by fast-forward.
	writeFile(t, bump, "mine.txt", "mine\n")
	gitOut(t, bump, "add", "-A")
	gitOut(t, bump, "commit", "-q", "-m", "mine")
	// Said before any run, in status and its JSON: a reader of ownRemote can
	// tell this branch from one a run would push.
	const why = "origin/old-name has commits this branch never had"
	if o := planOfWork(t, ctx, "bump").Worktree.OwnRemote; o.State != "diverged" || o.NoPushReason == nil || *o.NoPushReason != why || o.FixCommand != nil {
		t.Fatalf("ownRemote %+v", o)
	}
	var status bytes.Buffer
	if err := StatusWorktree(ctx, "bump", StatusOptions{NoPR: true, Agents: []wtsync.Agent{}}, &status); err != nil {
		t.Fatal(err)
	}
	if want := "to origin/old-name, a branch of another name (recorded); a run would not push it as it stands: " + why; !strings.Contains(status.String(), want) {
		t.Fatalf("status lacks %q:\n%s", want, status.String())
	}
	opts := pushAlways()
	opts.AllowDiverged = true
	var out, human bytes.Buffer
	opts.Journal = NewSyncRunJournal(&out, "sync rebase")
	err := SyncRebase(ctx, []string{"bump"}, opts, &human)
	if err == nil || !strings.Contains(err.Error(), "bump (not pushed: nowhere to push)") {
		t.Fatalf("err %v\n%s", err, human.String())
	}
	if !strings.Contains(human.String(), "✗ feat_wt/bump not pushed: origin/old-name has commits this branch never had") {
		t.Fatalf("out:\n%s", human.String())
	}
	if onBare(t, bare, "old-name") != theirs {
		t.Fatal("origin lost their commit")
	}
	p := decodeSyncRun(t, out.Bytes()).Worktrees[0]
	if p.Result != ResultRebased || p.PushCommand != nil || p.FixCommand != nil || p.NoPushReason == nil ||
		*p.NoPushReason != "origin/old-name has commits this branch never had" {
		t.Fatalf("participant %+v", p.UpParticipant)
	}
}

func TestSyncPushToRecordsSaysAndForgets(t *testing.T) {
	ctx, bump := runFixture(t, false)
	bare := ownBareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	tracksTheirs(t, ctx, bare)
	say := func(dest string, unset bool) string {
		t.Helper()
		var out bytes.Buffer
		if err := SyncPushTo(ctx, "bump", dest, unset, &out); err != nil {
			t.Fatalf("push-to %q: %v", dest, err)
		}
		return out.String()
	}
	if got := say("", false); !strings.HasPrefix(got, "feat_wt/bump has nowhere to push: it tracks origin/theirs") || !strings.Contains(got, "wt sync push-to feat_wt/bump origin/theirs") {
		t.Fatalf("undecided: %q", got)
	}
	// The branch is ahead of theirs, so there is a push to make by hand,
	// with the commit it was compared with pinned.
	want := "feat_wt/bump pushes to origin/theirs (recorded)\n  wt pushes it after its next rebase. To push it now:\n" +
		"  push: git -C " + bump + " push --force-with-lease=refs/heads/theirs:" +
		onBare(t, bare, "theirs") + " origin refs/heads/feat_wt/bump:refs/heads/theirs\n"
	if got := say("origin/theirs", false); got != want {
		t.Fatalf("recorded: %q", got)
	}
	if got := gitOut(t, main, "config", "--get", "branch.feat_wt/bump.wtPushTo"); got != "origin theirs" {
		t.Fatalf("written %q", got)
	}
	want = "feat_wt/bump pushes to origin/feat_wt/bump (recorded)\n  origin/feat_wt/bump is not there as last fetched: the first push makes it\n" +
		"  wt pushes it after its next rebase. To push it now:\n" +
		"  push: git -C " + bump + " push --force-with-lease --force-if-includes origin refs/heads/feat_wt/bump:refs/heads/feat_wt/bump\n"
	if got := say("origin/feat_wt/bump", false); got != want {
		t.Fatalf("its own name: %q", got)
	}
	if got := say("", true); !strings.HasPrefix(got, "feat_wt/bump has nowhere to push") {
		t.Fatalf("after --unset: %q", got)
	}
	if _, ok, _ := ctx.Repo.PushTo("feat_wt/bump"); ok {
		t.Fatal("--unset left the record")
	}

	// The longest remote name the destination starts with is the remote.
	// git remote add refuses such a name; a configuration written by hand
	// or by an older git can still hold one.
	gitOut(t, main, "config", "remote.origin/team.url", bare)
	if got := say("origin/team/x", false); !strings.HasPrefix(got, "feat_wt/bump pushes to origin/team/x (recorded)") {
		t.Fatalf("%q", got)
	}
	if got := gitOut(t, main, "config", "--get", "branch.feat_wt/bump.wtPushTo"); got != "origin/team x" {
		t.Fatalf("written %q, which splits at the first slash", got)
	}
	gitOut(t, main, "config", "--remove-section", "remote.origin/team")
	for dest, want := range map[string]string{
		"origin/main":      "origin/main is trunk: wt pushes nothing there",
		"nowhere/x":        "nowhere/x names no remote branch: write it as <remote>/<branch>, with one of the remotes here: origin",
		"theirs":           "theirs names no remote branch",
		"origin/":          "origin/ names no remote branch",
		"origin/bad..name": "bad..name is not a branch name",
		"origin/HEAD":      "HEAD is not a branch name",
		"origin/@":         "@ is not a branch name",
		"origin/+theirs":   "+theirs is not a branch name",
	} {
		say("", true)
		var out bytes.Buffer
		if err := SyncPushTo(ctx, "bump", dest, false, &out); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v", dest, err)
		}
		if _, ok, _ := ctx.Repo.PushTo("feat_wt/bump"); ok {
			t.Errorf("%q was recorded", dest)
		}
	}
	var out bytes.Buffer
	if err := SyncPushTo(ctx, main, "origin/x", false, &out); err == nil || !strings.Contains(err.Error(), "main checkout") {
		t.Fatalf("the main checkout: %v", err)
	}
}

// Both remote branches exist after pushes of a renamed branch went to its
// new name: wt status says so, and says which one it pushes to.
func TestStatusNamesTheDestinationAndTheStrayBranchOfItsOwnName(t *testing.T) {
	ctx, bump, _ := recordedBump(t)
	gitOut(t, ctx.Repo.MainRoot, "config", "--unset", "branch.feat_wt/bump.wtPushTo")
	gitOut(t, bump, "push", "-q", "origin", "feat_wt/bump")
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
	p := planOfWork(t, ctx, "bump")
	if o := p.Worktree.OwnRemote; o.State != "unknown" || o.Ref != nil || o.NoPushReason == nil || !strings.HasPrefix(*o.NoPushReason, "it tracks origin/old-name") ||
		strings.Join(o.FixCommand, " ") != "wt sync push-to feat_wt/bump origin/old-name" {
		t.Fatalf("ownRemote %+v", o)
	}
	gitOut(t, ctx.Repo.MainRoot, "config", "branch.feat_wt/bump.wtPushTo", "origin old-name")
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
	if o := planOfWork(t, ctx, "bump").Worktree.OwnRemote; o.Ref == nil || *o.Ref != "origin/old-name" || o.NoPushReason != nil || o.FixCommand != nil {
		t.Fatalf("ownRemote %+v", o)
	}
}

// wt remove keeps an unmerged branch under another name, and wt restore gives
// it its name back: its pushes stay where they went through both.
func TestRemoveAndRestoreKeepWhereAKeptBranchPushes(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/kept-push")
	main := ctx.Repo.MainRoot
	gitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitOut(t, main, "init", "-q", "--bare", bare)
	gitOut(t, main, "remote", "add", "origin", bare)
	gitOut(t, path, "push", "-q", "-u", "origin", "fix_wt/kept-push")
	dir := trashFor(t, ctx)
	var out bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &out); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "; wt still pushes it to origin/fix_wt/kept-push") {
		t.Fatalf("the removal does not say where the kept branch pushes:\n%s", out.String())
	}
	if got := gitOut(t, main, "config", "--get", "branch.kept-push.wtPushTo"); got != "origin fix_wt/kept-push" {
		t.Fatalf("recorded %q", got)
	}
	restore(t, ctx, dir)
	if _, ok, err := ctx.Repo.PushTo("fix_wt/kept-push"); ok || err != nil {
		t.Fatalf("back under its own name the record must be gone: %v %v", ok, err)
	}
	if up := ctx.Repo.Upstream("fix_wt/kept-push"); up != "refs/remotes/origin/fix_wt/kept-push" {
		t.Fatalf("upstream %q", up)
	}
}
