package synccmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/refs/refstest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// TracksTheirs puts a branch theirs on the remote, at the commit bump was
// cut from, and makes the fixture's bump track it: git checkout -b
// feat_wt/bump origin/theirs, as far as the configuration goes.
func TracksTheirs(t *testing.T, ctx *kit.Context, bare string) string {
	t.Helper()
	main := ctx.Repo.MainRoot
	testkit.GitOut(t, main, "push", "-q", "origin", "main~1:refs/heads/theirs")
	testkit.GitOut(t, main, "branch", "-q", "--set-upstream-to=origin/theirs", "feat_wt/bump")
	return testkit.OnBare(t, bare, "theirs")
}

func TestSyncPushToRecordsSaysAndForgets(t *testing.T) {
	t.Parallel()
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.OwnBareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	TracksTheirs(t, ctx, bare)
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
		testkit.OnBare(t, bare, "theirs") + " origin refs/heads/feat_wt/bump:refs/heads/theirs\n"
	if got := say("origin/theirs", false); got != want {
		t.Fatalf("recorded: %q", got)
	}
	if got := testkit.GitOut(t, main, "config", "--get", "branch.feat_wt/bump.wtPushTo"); got != "origin theirs" {
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
	testkit.GitOut(t, main, "config", "remote.origin/team.url", bare)
	if got := say("origin/team/x", false); !strings.HasPrefix(got, "feat_wt/bump pushes to origin/team/x (recorded)") {
		t.Fatalf("%q", got)
	}
	if got := testkit.GitOut(t, main, "config", "--get", "branch.feat_wt/bump.wtPushTo"); got != "origin/team x" {
		t.Fatalf("written %q, which splits at the first slash", got)
	}
	testkit.GitOut(t, main, "config", "--remove-section", "remote.origin/team")
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

// wt remove keeps an unmerged branch under another name, and wt restore gives
// it its name back: its pushes stay where they went through both.
func TestRemoveAndRestoreKeepWhereAKeptBranchPushes(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, path := provisiontest.SafetyWorktree(t, "fix/kept-push")
	main := ctx.Repo.MainRoot
	testkit.GitIn(t, path, "commit", "-q", "--allow-empty", "-m", "work")
	bare := filepath.Join(t.TempDir(), "origin.git")
	testkit.GitOut(t, main, "init", "-q", "--bare", bare)
	testkit.GitOut(t, main, "remote", "add", "origin", bare)
	testkit.GitOut(t, path, "push", "-q", "-u", "origin", "fix_wt/kept-push")
	dir := testkit.TrashFor(t, ctx)
	var out bytes.Buffer
	if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir}, &out); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "; wt still pushes it to origin/fix_wt/kept-push") {
		t.Fatalf("the removal does not say where the kept branch pushes:\n%s", out.String())
	}
	if got := testkit.GitOut(t, main, "config", "--get", "branch.kept-push.wtPushTo"); got != "origin fix_wt/kept-push" {
		t.Fatalf("recorded %q", got)
	}
	refstest.Restore(t, ctx, dir)
	if _, ok, err := ctx.Repo.PushTo("fix_wt/kept-push"); ok || err != nil {
		t.Fatalf("back under its own name the record must be gone: %v %v", ok, err)
	}
	if up := ctx.Repo.Upstream("fix_wt/kept-push"); up != "refs/remotes/origin/fix_wt/kept-push" {
		t.Fatalf("upstream %q", up)
	}
}
