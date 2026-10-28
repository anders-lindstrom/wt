package synccmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

func ffOn() synccmd.TrunkSyncOptions {
	return synccmd.TrunkSyncOptions{Apply: true, Agents: func() ([]agents.Agent, error) { return []agents.Agent{}, nil }}
}

func reasonOf(ts *synccmd.TrunkSync) string {
	if ts.SkippedReason == nil {
		return "<nil>"
	}
	return *ts.SkippedReason
}

func TestTrunkSyncFastForwardsTheCleanMainCheckout(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	before := f.Local(t)
	remote := f.AdvanceOrigin(t, 2)

	ts := synccmd.SyncLocalTrunk(f.Ctx, remote, ffOn())

	if !ts.FastForwarded || ts.SkippedReason != nil {
		t.Fatalf("want fast-forwarded, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.Local(t); got != remote {
		t.Fatalf("local main at %s, want %s", got, remote)
	}
	if *ts.Local != remote || *ts.Remote != remote || *ts.RemoteAhead != 2 || *ts.LocalAhead != 0 {
		t.Fatalf("want local=remote=%s, remoteAhead 2; got %+v", remote, ts)
	}
	if ts.Before != before {
		t.Fatalf("before %s, want %s", ts.Before, before)
	}
	// The working tree moved with the branch: nothing staged or changed.
	if out := testkit.GitOut(t, f.Main, "status", "--porcelain"); out != "" {
		t.Fatalf("main checkout not clean after the fast-forward:\n%s", out)
	}
}

func TestTrunkSyncUpdatesTrunkCheckedOutNowhere(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	testkit.GitIn(t, f.Main, "switch", "-q", "-c", "elsewhere")
	remote := f.AdvanceOrigin(t, 1)

	ts := synccmd.SyncLocalTrunk(f.Ctx, remote, ffOn())

	if !ts.FastForwarded {
		t.Fatalf("want fast-forwarded, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.Local(t); got != remote {
		t.Fatalf("local main at %s, want %s", got, remote)
	}
	if head := testkit.GitOut(t, f.Main, "symbolic-ref", "--short", "HEAD"); head != "elsewhere" {
		t.Fatalf("HEAD moved to %s", head)
	}
}

func TestTrunkSyncNothingToDoWhenEqual(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	ts := synccmd.SyncLocalTrunk(f.Ctx, f.Local(t), ffOn())
	if ts.FastForwarded || ts.SkippedReason != nil || *ts.LocalAhead != 0 || *ts.RemoteAhead != 0 {
		t.Fatalf("want nothing to do, got %+v (%s)", ts, reasonOf(ts))
	}
	if ts.Line() != "" {
		t.Fatalf("an equal trunk prints nothing, got %q", ts.Line())
	}
}

func TestTrunkSyncSkips(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T, f *synccmdtest.TrunkFixture, o *synccmd.TrunkSyncOptions)
		want  string
	}{
		{"optedOut", func(_ *testing.T, _ *synccmdtest.TrunkFixture, o *synccmd.TrunkSyncOptions) {
			o.OptedOut = true
		}, synccmd.TrunkSkipOptedOut},
		{"dirty tracked", func(t *testing.T, f *synccmdtest.TrunkFixture, _ *synccmd.TrunkSyncOptions) {
			testkit.WriteIn(t, f.Main, "bin/worktree/worktree.conf", testkit.MinimalConf+"# edited\n")
		}, synccmd.TrunkSkipDirty},
		{"dirty untracked", func(t *testing.T, f *synccmdtest.TrunkFixture, _ *synccmd.TrunkSyncOptions) {
			testkit.WriteIn(t, f.Main, "scratch.txt", "mine\n")
		}, synccmd.TrunkSkipDirty},
		{"merge in progress", func(t *testing.T, f *synccmdtest.TrunkFixture, _ *synccmd.TrunkSyncOptions) {
			gitDir := testkit.GitOut(t, f.Main, "rev-parse", "--absolute-git-dir")
			testkit.WriteIn(t, gitDir, "MERGE_HEAD", f.Local(t)+"\n")
		}, synccmd.TrunkSkipOperation},
		{"rebase stopped on trunk", func(t *testing.T, f *synccmdtest.TrunkFixture, _ *synccmd.TrunkSyncOptions) {
			testkit.GitIn(t, f.Main, "-c", "sequence.editor=perl -pi -e s/^pick/edit/", "rebase", "-q", "-i", "HEAD~1")
		}, synccmd.TrunkSkipOperation},
		{"bisect from trunk", func(t *testing.T, f *synccmdtest.TrunkFixture, _ *synccmd.TrunkSyncOptions) {
			testkit.GitIn(t, f.Main, "bisect", "start")
			testkit.GitIn(t, f.Main, "bisect", "bad")
		}, synccmd.TrunkSkipOperation},
		{"busy session", func(_ *testing.T, f *synccmdtest.TrunkFixture, o *synccmd.TrunkSyncOptions) {
			o.Agents = func() ([]agents.Agent, error) {
				return []agents.Agent{{Name: "s", Cwd: f.Main, Status: "busy"}}, nil
			}
		}, synccmd.TrunkSkipSession},
		{"background session", func(_ *testing.T, f *synccmdtest.TrunkFixture, o *synccmd.TrunkSyncOptions) {
			o.Agents = func() ([]agents.Agent, error) {
				return []agents.Agent{{Name: "s", Cwd: f.Main, Status: "idle", Kind: "background"}}, nil
			}
		}, synccmd.TrunkSkipSession},
		{"sessions unknown", func(_ *testing.T, _ *synccmdtest.TrunkFixture, o *synccmd.TrunkSyncOptions) {
			o.Agents = func() ([]agents.Agent, error) { return nil, os.ErrPermission }
		}, synccmd.TrunkSkipSession},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := synccmdtest.NewTrunkFixture(t)
			before := f.Local(t)
			remote := f.AdvanceOrigin(t, 1)
			o := ffOn()
			c.setup(t, f, &o)

			ts := synccmd.SyncLocalTrunk(f.Ctx, remote, o)

			if ts.FastForwarded || reasonOf(ts) != c.want {
				t.Fatalf("want skipped %s, got %+v (%s)", c.want, ts, reasonOf(ts))
			}
			if got := f.Local(t); got != before {
				t.Fatalf("local main moved to %s", got)
			}
			if ts.Line() == "" {
				t.Fatal("a skip prints a line")
			}
		})
	}
}

func TestTrunkSyncNeverMovesALocalTrunkWithCommitsOfItsOwn(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	testkit.GitIn(t, f.Main, "commit", "-q", "--allow-empty", "-m", "unpushed")
	before := f.Local(t)

	ts := synccmd.SyncLocalTrunk(f.Ctx, testkit.GitOut(t, f.Main, "rev-parse", "origin/main"), ffOn())
	if reasonOf(ts) != synccmd.TrunkSkipAhead || *ts.LocalAhead != 1 || *ts.RemoteAhead != 0 {
		t.Fatalf("want ahead 1/0, got %+v (%s)", ts, reasonOf(ts))
	}

	remote := f.AdvanceOrigin(t, 2)
	ts = synccmd.SyncLocalTrunk(f.Ctx, remote, ffOn())
	if reasonOf(ts) != synccmd.TrunkSkipDiverged || *ts.LocalAhead != 1 || *ts.RemoteAhead != 2 {
		t.Fatalf("want diverged 1/2, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.Local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
	if !strings.Contains(ts.Line(), "diverged") {
		t.Fatalf("line %q does not say diverged", ts.Line())
	}
}

// The compare-and-swap: trunk checked out nowhere gains a commit of its own
// between the read and the update. The update must not land.
func TestTrunkSyncLosesTheRaceToAMovedTrunk(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	testkit.GitIn(t, f.Main, "switch", "-q", "-c", "elsewhere")
	remote := f.AdvanceOrigin(t, 1)
	var moved string
	o := ffOn()
	o.BeforeUpdate = func() {
		testkit.GitIn(t, f.Main, "update-ref", "refs/heads/main", testkit.GitOut(t, f.Main, "commit-tree", "-p", "main", "-m", "raced", "main^{tree}"))
		moved = f.Local(t)
	}

	ts := synccmd.SyncLocalTrunk(f.Ctx, remote, o)

	if ts.FastForwarded || reasonOf(ts) != synccmd.TrunkSkipNotFastForward {
		t.Fatalf("want notFastForward, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.Local(t); got != moved {
		t.Fatalf("local main %s, want the raced %s", got, moved)
	}
}

// The main checkout's trunk gains a commit between the checks and the merge:
// merge --ff-only refuses, and it is reported, not an error.
func TestTrunkSyncMergeRefusedIsNotFastForward(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	remote := f.AdvanceOrigin(t, 1)
	o := ffOn()
	o.BeforeUpdate = func() { testkit.GitIn(t, f.Main, "commit", "-q", "--allow-empty", "-m", "raced") }

	ts := synccmd.SyncLocalTrunk(f.Ctx, remote, o)

	if ts.FastForwarded || reasonOf(ts) != synccmd.TrunkSkipNotFastForward {
		t.Fatalf("want notFastForward, got %+v (%s)", ts, reasonOf(ts))
	}
}

// An ignored file in the main checkout that origin now tracks: the merge
// would overwrite it, so it fails and says so.
func TestTrunkSyncNeverOverwritesAnIgnoredFile(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	testkit.WriteIn(t, f.Main, ".git/info/exclude", "keep.txt\n")
	testkit.WriteIn(t, f.Main, "keep.txt", "mine\n")
	testkit.GitIn(t, f.Other, "pull", "-q", "--ff-only")
	testkit.WriteIn(t, f.Other, "keep.txt", "theirs\n")
	testkit.GitIn(t, f.Other, "add", "keep.txt")
	testkit.GitIn(t, f.Other, "commit", "-q", "-m", "track keep.txt")
	testkit.GitIn(t, f.Other, "push", "-q", "origin", "main")
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	before := f.Local(t)

	ts := synccmd.SyncLocalTrunk(f.Ctx, testkit.GitOut(t, f.Main, "rev-parse", "origin/main"), ffOn())

	if ts.FastForwarded || reasonOf(ts) != synccmd.TrunkSkipFailed {
		t.Fatalf("want failed, got %+v (%s)", ts, reasonOf(ts))
	}
	if b, _ := os.ReadFile(filepath.Join(f.Main, "keep.txt")); string(b) != "mine\n" {
		t.Fatalf("keep.txt overwritten: %q", b)
	}
	if got := f.Local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
}

// wt status only reads: it says what a run would do and moves nothing.
func TestTrunkSyncWithoutApplySaysWhatARunWouldDo(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	before := f.Local(t)
	remote := f.AdvanceOrigin(t, 1)
	o := ffOn()
	o.Apply = false

	ts := synccmd.SyncLocalTrunk(f.Ctx, remote, o)

	if ts.FastForwarded || ts.SkippedReason != nil || *ts.RemoteAhead != 1 {
		t.Fatalf("want would-fast-forward, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.Local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
}

func TestTrunkSyncWithNoLocalTrunkHasNothingToDo(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	testkit.GitIn(t, f.Main, "switch", "-q", "-c", "elsewhere")
	testkit.GitIn(t, f.Main, "branch", "-q", "-D", "main")
	ts := synccmd.SyncLocalTrunk(f.Ctx, testkit.GitOut(t, f.Main, "rev-parse", "origin/main"), ffOn())
	if ts.Local != nil || ts.LocalAhead != nil || ts.FastForwarded || ts.SkippedReason != nil {
		t.Fatalf("want nothing to do, got %+v (%s)", ts, reasonOf(ts))
	}
	if gittest.Git(t, f.Main, "branch", "--list", "main") != "" {
		t.Fatal("local main was created")
	}
}

// The main checkout switches away from trunk between the checks and the
// merge: the merge must not fast-forward whatever branch it is on now.
func TestTrunkSyncNeverMergesIntoAnotherBranch(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	remote := f.AdvanceOrigin(t, 1)
	other := f.Local(t)
	o := ffOn()
	o.BeforeUpdate = func() { testkit.GitIn(t, f.Main, "switch", "-q", "-c", "switched") }

	ts := synccmd.SyncLocalTrunk(f.Ctx, remote, o)

	if ts.FastForwarded || reasonOf(ts) != synccmd.TrunkSkipNotFastForward {
		t.Fatalf("want notFastForward, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := testkit.GitOut(t, f.Main, "rev-parse", "switched"); got != other {
		t.Fatalf("the switched-to branch moved to %s", got)
	}
}

// Trunk gets checked out between the listing and the compare-and-swap: the
// ref must not move under a checkout whose files would not follow.
func TestTrunkSyncLeavesTrunkCheckedOutSinceTheListing(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	testkit.GitIn(t, f.Main, "switch", "-q", "-c", "elsewhere")
	before := f.Local(t)
	remote := f.AdvanceOrigin(t, 1)
	o := ffOn()
	o.BeforeUpdate = func() { testkit.GitIn(t, f.Main, "switch", "-q", "main") }

	ts := synccmd.SyncLocalTrunk(f.Ctx, remote, o)

	if ts.FastForwarded || reasonOf(ts) != synccmd.TrunkSkipNotFastForward {
		t.Fatalf("want notFastForward, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.Local(t); got != before {
		t.Fatalf("local main moved to %s under its checkout", got)
	}
}

// Trunk checked out in two worktrees at once: a merge in one would leave the
// other's files behind, so neither is touched.
func TestTrunkSyncRefusesTrunkCheckedOutTwice(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	before := f.Local(t)
	second := filepath.Join(t.TempDir(), "second")
	testkit.GitIn(t, f.Main, "worktree", "add", "-q", "-f", second, "main")
	remote := f.AdvanceOrigin(t, 1)

	ts := synccmd.SyncLocalTrunk(f.Ctx, remote, ffOn())

	if ts.FastForwarded || reasonOf(ts) != synccmd.TrunkSkipFailed || !strings.Contains(ts.Line(), "more than one") {
		t.Fatalf("want failed, checked out twice; got %+v (%s) %q", ts, reasonOf(ts), ts.Line())
	}
	if got := f.Local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
}

// An untracked file counts as dirt even where git status is told to hide
// them.
func TestTrunkSyncSeesUntrackedFilesStatusHides(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	testkit.GitIn(t, f.Main, "config", "status.showUntrackedFiles", "no")
	testkit.WriteIn(t, f.Main, "scratch.txt", "mine\n")
	remote := f.AdvanceOrigin(t, 1)

	if ts := synccmd.SyncLocalTrunk(f.Ctx, remote, ffOn()); reasonOf(ts) != synccmd.TrunkSkipDirty {
		t.Fatalf("want dirty, got %+v (%s)", ts, reasonOf(ts))
	}
}

// An idle session in the checkout does not hold trunk back: the checkout is
// clean and the merge overwrites nothing, so it loses nothing.
func TestTrunkSyncFastForwardsUnderAnIdleSession(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	remote := f.AdvanceOrigin(t, 1)
	o := ffOn()
	o.Agents = func() ([]agents.Agent, error) {
		return []agents.Agent{{Name: "s", Cwd: f.Main, Status: "idle"}}, nil
	}

	ts := synccmd.SyncLocalTrunk(f.Ctx, remote, o)

	if !ts.FastForwarded || f.Local(t) != remote {
		t.Fatalf("want fast-forwarded under an idle session, got %+v (%s)", ts, reasonOf(ts))
	}
}
