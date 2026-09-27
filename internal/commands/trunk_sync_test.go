package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// trunkFixture: a main checkout on main with a bare origin of its own, both
// at the same commit, and a second clone that can move origin on. push
// commits n times in that clone and pushes; the main checkout then fetches.
type trunkFixture struct {
	ctx    *Context
	main   string
	origin string
	other  string
}

func newTrunkFixture(t *testing.T) *trunkFixture {
	t.Helper()
	main := committedRepo(t, minimalConf)
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-q", "-m", "configure")
	origin := gittest.WithOrigin(t, main)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", origin, other)
	gitIn(t, other, "config", "user.email", "t@example.com")
	gitIn(t, other, "config", "user.name", "T")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return &trunkFixture{ctx: ctx, main: main, origin: origin, other: other}
}

// advanceOrigin commits n times on origin's main and fetches it into the
// main checkout, leaving local main where it was.
func (f *trunkFixture) advanceOrigin(t *testing.T, n int) string {
	t.Helper()
	gitIn(t, f.other, "pull", "-q", "--ff-only")
	for i := 0; i < n; i++ {
		gitIn(t, f.other, "commit", "-q", "--allow-empty", "-m", "origin moves")
	}
	gitIn(t, f.other, "push", "-q", "origin", "main")
	gitIn(t, f.main, "fetch", "-q", "origin")
	return gitOut(t, f.main, "rev-parse", "origin/main")
}

func (f *trunkFixture) local(t *testing.T) string {
	t.Helper()
	return gitOut(t, f.main, "rev-parse", "refs/heads/main")
}

func ffOn() trunkSyncOptions {
	return trunkSyncOptions{apply: true, agents: func() ([]wtsync.Agent, error) { return []wtsync.Agent{}, nil }}
}

func reasonOf(ts *TrunkSync) string {
	if ts.SkippedReason == nil {
		return "<nil>"
	}
	return *ts.SkippedReason
}

func TestTrunkSyncFastForwardsTheCleanMainCheckout(t *testing.T) {
	f := newTrunkFixture(t)
	before := f.local(t)
	remote := f.advanceOrigin(t, 2)

	ts := syncLocalTrunk(f.ctx, remote, ffOn())

	if !ts.FastForwarded || ts.SkippedReason != nil {
		t.Fatalf("want fast-forwarded, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.local(t); got != remote {
		t.Fatalf("local main at %s, want %s", got, remote)
	}
	if *ts.Local != remote || *ts.Remote != remote || *ts.RemoteAhead != 2 || *ts.LocalAhead != 0 {
		t.Fatalf("want local=remote=%s, remoteAhead 2; got %+v", remote, ts)
	}
	if ts.before != before {
		t.Fatalf("before %s, want %s", ts.before, before)
	}
	// The working tree moved with the branch: nothing staged or changed.
	if out := gitOut(t, f.main, "status", "--porcelain"); out != "" {
		t.Fatalf("main checkout not clean after the fast-forward:\n%s", out)
	}
}

func TestTrunkSyncUpdatesTrunkCheckedOutNowhere(t *testing.T) {
	f := newTrunkFixture(t)
	gitIn(t, f.main, "switch", "-q", "-c", "elsewhere")
	remote := f.advanceOrigin(t, 1)

	ts := syncLocalTrunk(f.ctx, remote, ffOn())

	if !ts.FastForwarded {
		t.Fatalf("want fast-forwarded, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.local(t); got != remote {
		t.Fatalf("local main at %s, want %s", got, remote)
	}
	if head := gitOut(t, f.main, "symbolic-ref", "--short", "HEAD"); head != "elsewhere" {
		t.Fatalf("HEAD moved to %s", head)
	}
}

func TestTrunkSyncNothingToDoWhenEqual(t *testing.T) {
	f := newTrunkFixture(t)
	ts := syncLocalTrunk(f.ctx, f.local(t), ffOn())
	if ts.FastForwarded || ts.SkippedReason != nil || *ts.LocalAhead != 0 || *ts.RemoteAhead != 0 {
		t.Fatalf("want nothing to do, got %+v (%s)", ts, reasonOf(ts))
	}
	if ts.Line() != "" {
		t.Fatalf("an equal trunk prints nothing, got %q", ts.Line())
	}
}

func TestTrunkSyncSkips(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *trunkFixture, o *trunkSyncOptions)
		want  string
	}{
		{"optedOut", func(_ *testing.T, _ *trunkFixture, o *trunkSyncOptions) {
			o.optedOut = true
		}, TrunkSkipOptedOut},
		{"dirty tracked", func(t *testing.T, f *trunkFixture, _ *trunkSyncOptions) {
			writeIn(t, f.main, "bin/worktree/worktree.conf", minimalConf+"# edited\n")
		}, TrunkSkipDirty},
		{"dirty untracked", func(t *testing.T, f *trunkFixture, _ *trunkSyncOptions) {
			writeIn(t, f.main, "scratch.txt", "mine\n")
		}, TrunkSkipDirty},
		{"merge in progress", func(t *testing.T, f *trunkFixture, _ *trunkSyncOptions) {
			gitDir := gitOut(t, f.main, "rev-parse", "--absolute-git-dir")
			writeIn(t, gitDir, "MERGE_HEAD", f.local(t)+"\n")
		}, TrunkSkipOperation},
		{"rebase stopped on trunk", func(t *testing.T, f *trunkFixture, _ *trunkSyncOptions) {
			gitIn(t, f.main, "-c", "sequence.editor=perl -pi -e s/^pick/edit/", "rebase", "-q", "-i", "HEAD~1")
		}, TrunkSkipOperation},
		{"bisect from trunk", func(t *testing.T, f *trunkFixture, _ *trunkSyncOptions) {
			gitIn(t, f.main, "bisect", "start")
			gitIn(t, f.main, "bisect", "bad")
		}, TrunkSkipOperation},
		{"busy session", func(_ *testing.T, f *trunkFixture, o *trunkSyncOptions) {
			o.agents = func() ([]wtsync.Agent, error) {
				return []wtsync.Agent{{Name: "s", Cwd: f.main, Status: "busy"}}, nil
			}
		}, TrunkSkipSession},
		{"background session", func(_ *testing.T, f *trunkFixture, o *trunkSyncOptions) {
			o.agents = func() ([]wtsync.Agent, error) {
				return []wtsync.Agent{{Name: "s", Cwd: f.main, Status: "idle", Kind: "background"}}, nil
			}
		}, TrunkSkipSession},
		{"sessions unknown", func(_ *testing.T, _ *trunkFixture, o *trunkSyncOptions) {
			o.agents = func() ([]wtsync.Agent, error) { return nil, os.ErrPermission }
		}, TrunkSkipSession},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newTrunkFixture(t)
			before := f.local(t)
			remote := f.advanceOrigin(t, 1)
			o := ffOn()
			c.setup(t, f, &o)

			ts := syncLocalTrunk(f.ctx, remote, o)

			if ts.FastForwarded || reasonOf(ts) != c.want {
				t.Fatalf("want skipped %s, got %+v (%s)", c.want, ts, reasonOf(ts))
			}
			if got := f.local(t); got != before {
				t.Fatalf("local main moved to %s", got)
			}
			if ts.Line() == "" {
				t.Fatal("a skip prints a line")
			}
		})
	}
}

func TestTrunkSyncNeverMovesALocalTrunkWithCommitsOfItsOwn(t *testing.T) {
	f := newTrunkFixture(t)
	gitIn(t, f.main, "commit", "-q", "--allow-empty", "-m", "unpushed")
	before := f.local(t)

	ts := syncLocalTrunk(f.ctx, gitOut(t, f.main, "rev-parse", "origin/main"), ffOn())
	if reasonOf(ts) != TrunkSkipAhead || *ts.LocalAhead != 1 || *ts.RemoteAhead != 0 {
		t.Fatalf("want ahead 1/0, got %+v (%s)", ts, reasonOf(ts))
	}

	remote := f.advanceOrigin(t, 2)
	ts = syncLocalTrunk(f.ctx, remote, ffOn())
	if reasonOf(ts) != TrunkSkipDiverged || *ts.LocalAhead != 1 || *ts.RemoteAhead != 2 {
		t.Fatalf("want diverged 1/2, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
	if !strings.Contains(ts.Line(), "diverged") {
		t.Fatalf("line %q does not say diverged", ts.Line())
	}
}

// The compare-and-swap: trunk checked out nowhere gains a commit of its own
// between the read and the update. The update must not land.
func TestTrunkSyncLosesTheRaceToAMovedTrunk(t *testing.T) {
	f := newTrunkFixture(t)
	gitIn(t, f.main, "switch", "-q", "-c", "elsewhere")
	remote := f.advanceOrigin(t, 1)
	var moved string
	o := ffOn()
	o.beforeUpdate = func() {
		gitIn(t, f.main, "update-ref", "refs/heads/main", gitOut(t, f.main, "commit-tree", "-p", "main", "-m", "raced", "main^{tree}"))
		moved = f.local(t)
	}

	ts := syncLocalTrunk(f.ctx, remote, o)

	if ts.FastForwarded || reasonOf(ts) != TrunkSkipNotFastForward {
		t.Fatalf("want notFastForward, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.local(t); got != moved {
		t.Fatalf("local main %s, want the raced %s", got, moved)
	}
}

// The main checkout's trunk gains a commit between the checks and the merge:
// merge --ff-only refuses, and it is reported, not an error.
func TestTrunkSyncMergeRefusedIsNotFastForward(t *testing.T) {
	f := newTrunkFixture(t)
	remote := f.advanceOrigin(t, 1)
	o := ffOn()
	o.beforeUpdate = func() { gitIn(t, f.main, "commit", "-q", "--allow-empty", "-m", "raced") }

	ts := syncLocalTrunk(f.ctx, remote, o)

	if ts.FastForwarded || reasonOf(ts) != TrunkSkipNotFastForward {
		t.Fatalf("want notFastForward, got %+v (%s)", ts, reasonOf(ts))
	}
}

// An ignored file in the main checkout that origin now tracks: the merge
// would overwrite it, so it fails and says so.
func TestTrunkSyncNeverOverwritesAnIgnoredFile(t *testing.T) {
	f := newTrunkFixture(t)
	writeIn(t, f.main, ".git/info/exclude", "keep.txt\n")
	writeIn(t, f.main, "keep.txt", "mine\n")
	gitIn(t, f.other, "pull", "-q", "--ff-only")
	writeIn(t, f.other, "keep.txt", "theirs\n")
	gitIn(t, f.other, "add", "keep.txt")
	gitIn(t, f.other, "commit", "-q", "-m", "track keep.txt")
	gitIn(t, f.other, "push", "-q", "origin", "main")
	gitIn(t, f.main, "fetch", "-q", "origin")
	before := f.local(t)

	ts := syncLocalTrunk(f.ctx, gitOut(t, f.main, "rev-parse", "origin/main"), ffOn())

	if ts.FastForwarded || reasonOf(ts) != TrunkSkipFailed {
		t.Fatalf("want failed, got %+v (%s)", ts, reasonOf(ts))
	}
	if b, _ := os.ReadFile(filepath.Join(f.main, "keep.txt")); string(b) != "mine\n" {
		t.Fatalf("keep.txt overwritten: %q", b)
	}
	if got := f.local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
}

// wt status only reads: it says what a run would do and moves nothing.
func TestTrunkSyncWithoutApplySaysWhatARunWouldDo(t *testing.T) {
	f := newTrunkFixture(t)
	before := f.local(t)
	remote := f.advanceOrigin(t, 1)
	o := ffOn()
	o.apply = false

	ts := syncLocalTrunk(f.ctx, remote, o)

	if ts.FastForwarded || ts.SkippedReason != nil || *ts.RemoteAhead != 1 {
		t.Fatalf("want would-fast-forward, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
}

func TestTrunkSyncWithNoLocalTrunkHasNothingToDo(t *testing.T) {
	f := newTrunkFixture(t)
	gitIn(t, f.main, "switch", "-q", "-c", "elsewhere")
	gitIn(t, f.main, "branch", "-q", "-D", "main")
	ts := syncLocalTrunk(f.ctx, gitOut(t, f.main, "rev-parse", "origin/main"), ffOn())
	if ts.Local != nil || ts.LocalAhead != nil || ts.FastForwarded || ts.SkippedReason != nil {
		t.Fatalf("want nothing to do, got %+v (%s)", ts, reasonOf(ts))
	}
	if gittest.Git(t, f.main, "branch", "--list", "main") != "" {
		t.Fatal("local main was created")
	}
}

func writeIn(t *testing.T, dir, rel, content string) {
	t.Helper()
	gittest.WriteFile(t, filepath.Join(dir, rel), content)
}

// The main checkout switches away from trunk between the checks and the
// merge: the merge must not fast-forward whatever branch it is on now.
func TestTrunkSyncNeverMergesIntoAnotherBranch(t *testing.T) {
	f := newTrunkFixture(t)
	remote := f.advanceOrigin(t, 1)
	other := f.local(t)
	o := ffOn()
	o.beforeUpdate = func() { gitIn(t, f.main, "switch", "-q", "-c", "switched") }

	ts := syncLocalTrunk(f.ctx, remote, o)

	if ts.FastForwarded || reasonOf(ts) != TrunkSkipNotFastForward {
		t.Fatalf("want notFastForward, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := gitOut(t, f.main, "rev-parse", "switched"); got != other {
		t.Fatalf("the switched-to branch moved to %s", got)
	}
}

// Trunk gets checked out between the listing and the compare-and-swap: the
// ref must not move under a checkout whose files would not follow.
func TestTrunkSyncLeavesTrunkCheckedOutSinceTheListing(t *testing.T) {
	f := newTrunkFixture(t)
	gitIn(t, f.main, "switch", "-q", "-c", "elsewhere")
	before := f.local(t)
	remote := f.advanceOrigin(t, 1)
	o := ffOn()
	o.beforeUpdate = func() { gitIn(t, f.main, "switch", "-q", "main") }

	ts := syncLocalTrunk(f.ctx, remote, o)

	if ts.FastForwarded || reasonOf(ts) != TrunkSkipNotFastForward {
		t.Fatalf("want notFastForward, got %+v (%s)", ts, reasonOf(ts))
	}
	if got := f.local(t); got != before {
		t.Fatalf("local main moved to %s under its checkout", got)
	}
}

// Trunk checked out in two worktrees at once: a merge in one would leave the
// other's files behind, so neither is touched.
func TestTrunkSyncRefusesTrunkCheckedOutTwice(t *testing.T) {
	f := newTrunkFixture(t)
	before := f.local(t)
	second := filepath.Join(t.TempDir(), "second")
	gitIn(t, f.main, "worktree", "add", "-q", "-f", second, "main")
	remote := f.advanceOrigin(t, 1)

	ts := syncLocalTrunk(f.ctx, remote, ffOn())

	if ts.FastForwarded || reasonOf(ts) != TrunkSkipFailed || !strings.Contains(ts.Line(), "more than one") {
		t.Fatalf("want failed, checked out twice; got %+v (%s) %q", ts, reasonOf(ts), ts.Line())
	}
	if got := f.local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
}

// An untracked file counts as dirt even where git status is told to hide
// them.
func TestTrunkSyncSeesUntrackedFilesStatusHides(t *testing.T) {
	f := newTrunkFixture(t)
	gitIn(t, f.main, "config", "status.showUntrackedFiles", "no")
	writeIn(t, f.main, "scratch.txt", "mine\n")
	remote := f.advanceOrigin(t, 1)

	if ts := syncLocalTrunk(f.ctx, remote, ffOn()); reasonOf(ts) != TrunkSkipDirty {
		t.Fatalf("want dirty, got %+v (%s)", ts, reasonOf(ts))
	}
}

// An idle session in the checkout does not hold trunk back: the checkout is
// clean and the merge overwrites nothing, so it loses nothing.
func TestTrunkSyncFastForwardsUnderAnIdleSession(t *testing.T) {
	f := newTrunkFixture(t)
	remote := f.advanceOrigin(t, 1)
	o := ffOn()
	o.agents = func() ([]wtsync.Agent, error) {
		return []wtsync.Agent{{Name: "s", Cwd: f.main, Status: "idle"}}, nil
	}

	ts := syncLocalTrunk(f.ctx, remote, o)

	if !ts.FastForwarded || f.local(t) != remote {
		t.Fatalf("want fast-forwarded under an idle session, got %+v (%s)", ts, reasonOf(ts))
	}
}
