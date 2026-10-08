package commands

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// withWork adds worktree x (feat_wt/x) at trunk as it is now, one commit
// of its own ahead, and returns run options with nobody in any worktree.
func (f *trunkFixture) withWork(t *testing.T) RunOptions {
	t.Helper()
	var buf bytes.Buffer
	x, err := New(f.ctx, "feat/x", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	writeIn(t, x, "x.txt", "x\n")
	gitIn(t, x, "add", "x.txt")
	gitIn(t, x, "commit", "-q", "-m", "x")
	return RunOptions{pushOptions: pushOptions{Push: PushNever}, verbOptions: verbOptions{
		Agents: []wtsync.Agent{},
		Now:    func() time.Time { return time.Unix(0, 99) },
	}}
}

// declare has origin's trunk declare a .wt-sync.yaml, which wt sync rebase
// needs; the main checkout does not fetch it.
func (f *trunkFixture) declare(t *testing.T) {
	t.Helper()
	gitIn(t, f.other, "pull", "-q", "--ff-only")
	writeIn(t, f.other, ".wt-sync.yaml", "conflicts: []\n")
	gitIn(t, f.other, "add", "-A")
	gitIn(t, f.other, "commit", "-q", "-m", "declare")
	gitIn(t, f.other, "push", "-q", "origin", "main")
}

func TestUpFastForwardsLocalTrunkAfterItsFetch(t *testing.T) {
	f := newTrunkFixture(t)
	opts := f.withWork(t)
	before := f.local(t)
	remote := f.advanceOrigin(t, 2)
	gitIn(t, f.main, "update-ref", "refs/remotes/origin/main", before) // as if never fetched

	r, err := upJSON(t, f.ctx, "x", opts)
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if got := f.local(t); got != remote {
		t.Fatalf("local main at %s, want origin's %s", got, remote)
	}
	ts := r.TrunkSync
	if ts == nil || !ts.FastForwarded || ts.SkippedReason != nil || *ts.Local != remote || *ts.RemoteAhead != 2 {
		t.Fatalf("trunkSync = %+v", ts)
	}
	if len(r.Worktrees) != 1 || r.Worktrees[0].Result != ResultRebased {
		t.Fatalf("worktrees = %+v, want x rebased", r.Worktrees)
	}
}

func TestUpSaysWhatHappenedToLocalTrunk(t *testing.T) {
	f := newTrunkFixture(t)
	opts := f.withWork(t)
	f.advanceOrigin(t, 1)
	var out bytes.Buffer
	if err := Up(f.ctx, "x", opts, &out); err != nil {
		t.Fatalf("up: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "local main  ") || !strings.Contains(out.String(), "fast-forwarded to origin/main") {
		t.Fatalf("no line about local main:\n%s", out.String())
	}
}

func TestUpNoFetchLeavesLocalTrunkAndReportsNull(t *testing.T) {
	f := newTrunkFixture(t)
	opts := f.withWork(t)
	before := f.local(t)
	f.advanceOrigin(t, 1)
	opts.NoFetch = true

	r, err := upJSON(t, f.ctx, "x", opts)
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if r.TrunkSync != nil {
		t.Fatalf("trunkSync = %+v, want null without a fetch", r.TrunkSync)
	}
	if got := f.local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
}

func TestUpOptedOutLeavesLocalTrunk(t *testing.T) {
	for _, how := range []string{"flag", "config"} {
		t.Run(how, func(t *testing.T) {
			f := newTrunkFixture(t)
			opts := f.withWork(t)
			before := f.local(t)
			f.advanceOrigin(t, 1)
			if how == "flag" {
				opts.NoFFTrunk = true
			} else {
				f.ctx.UserConfig().FFTrunk = false
			}
			r, err := upJSON(t, f.ctx, "x", opts)
			if err != nil {
				t.Fatalf("up: %v", err)
			}
			if r.TrunkSync == nil || r.TrunkSync.SkippedReason == nil || *r.TrunkSync.SkippedReason != TrunkSkipOptedOut {
				t.Fatalf("trunkSync = %+v, want optedOut", r.TrunkSync)
			}
			if got := f.local(t); got != before {
				t.Fatalf("local main moved to %s", got)
			}
		})
	}
}

// A fast-forward that fails never fails the rebase: it is reported.
func TestSyncRunReportsASkippedTrunkAndStillRebases(t *testing.T) {
	f := newTrunkFixture(t)
	opts := f.withWork(t)
	gitIn(t, f.main, "commit", "-q", "--allow-empty", "-m", "unpushed")
	f.advanceOrigin(t, 1)
	x := gitOut(t, f.main, "rev-parse", "feat_wt/x")
	f.declare(t)

	r, err := syncRunJSON(t, f.ctx, []string{"x"}, opts)
	if err != nil {
		t.Fatalf("sync rebase: %v", err)
	}
	if r.TrunkSync == nil || r.TrunkSync.SkippedReason == nil || *r.TrunkSync.SkippedReason != TrunkSkipDiverged {
		t.Fatalf("trunkSync = %+v, want diverged", r.TrunkSync)
	}
	if len(r.Worktrees) != 1 || r.Worktrees[0].Result != ResultRebased {
		t.Fatalf("worktrees = %+v, want x rebased", *r.Worktrees[0])
	}
	if now := gitOut(t, f.main, "rev-parse", "feat_wt/x"); now == x {
		t.Fatal("x did not move")
	}
}

// The keeper fast-forwards local trunk after its fetch, even on a pass with
// nothing to rebase.
func TestSyncKeepRunFastForwardsLocalTrunk(t *testing.T) {
	f := newTrunkFixture(t)
	f.declare(t)
	f.advanceOrigin(t, 1)
	// Moved again after the last fetch: only the pass's own fetch sees it.
	gitIn(t, f.other, "commit", "-q", "--allow-empty", "-m", "again")
	gitIn(t, f.other, "push", "-q", "origin", "main")
	remote := gitOut(t, f.other, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := SyncKeepRun(f.ctx, keepOpts(PushNever), &out); err != nil {
		t.Fatalf("keep: %v\n%s", err, out.String())
	}
	if got := f.local(t); got != remote {
		t.Fatalf("local main at %s, want %s\n%s", got, remote, out.String())
	}
	if !strings.Contains(out.String(), "fast-forwarded to origin/main") {
		t.Fatalf("the pass does not say so:\n%s", out.String())
	}
	log, _ := keepFiles(t, f.ctx)
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "fast-forwarded to origin/main") {
		t.Fatalf("the log does not say so:\n%s", b)
	}
}

func TestSyncKeepRunNoFFTrunkLeavesLocalTrunk(t *testing.T) {
	f := newTrunkFixture(t)
	before := f.local(t)
	f.advanceOrigin(t, 1)
	opts := keepOpts(PushNever)
	opts.NoFFTrunk = true
	var out bytes.Buffer
	_ = SyncKeepRun(f.ctx, opts, &out)
	if got := f.local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
}

// wt status says what a run would do to local trunk and moves nothing.
func TestStatusJSONSaysWhatARunWouldDoToLocalTrunk(t *testing.T) {
	f := newTrunkFixture(t)
	f.withWork(t)
	before := f.local(t)
	f.advanceOrigin(t, 3)

	p := planOfWork(t, f.ctx, "x")

	ts := p.TrunkSync
	if ts == nil || ts.FastForwarded || ts.SkippedReason != nil || *ts.RemoteAhead != 3 || *ts.Local != before {
		t.Fatalf("trunkSync = %+v", ts)
	}
	if got := f.local(t); got != before {
		t.Fatalf("local main moved to %s", got)
	}
	writeIn(t, f.main, "scratch.txt", "mine\n")
	if p = planOfWork(t, f.ctx, "x"); p.TrunkSync.SkippedReason == nil || *p.TrunkSync.SkippedReason != TrunkSkipDirty {
		t.Fatalf("trunkSync = %+v, want dirty", p.TrunkSync)
	}
}

// An --expect that no longer holds touches nothing: local trunk included.
func TestExpectRefusedLeavesLocalTrunk(t *testing.T) {
	for _, verb := range []string{"up", "sync rebase"} {
		t.Run(verb, func(t *testing.T) {
			f := newTrunkFixture(t)
			f.declare(t)
			opts := f.withWork(t)
			before := f.local(t)
			f.advanceOrigin(t, 1)
			opts.Expect = "1:stale"
			var err error
			if verb == "up" {
				_, err = upJSON(t, f.ctx, "x", opts)
			} else {
				_, err = syncRunJSON(t, f.ctx, []string{"x"}, opts)
			}
			if err == nil || !strings.Contains(err.Error(), "changed since it was read") {
				t.Fatalf("want the stale token refused, got %v", err)
			}
			if got := f.local(t); got != before {
				t.Fatalf("local main moved to %s on a refused run", got)
			}
		})
	}
}

// A repository whose trunk declares nothing has nothing for wt sync rebase to
// rebase, but it fetched: local trunk still follows.
func TestSyncRunUndeclaredStillFastForwardsLocalTrunk(t *testing.T) {
	f := newTrunkFixture(t)
	opts := f.withWork(t)
	remote := f.advanceOrigin(t, 1)
	var out bytes.Buffer
	if err := SyncRebase(f.ctx, []string{"x"}, opts, &out); err == nil {
		t.Fatalf("an undeclared trunk ran:\n%s", out.String())
	}
	if got := f.local(t); got != remote {
		t.Fatalf("local main at %s, want %s\n%s", got, remote, out.String())
	}
}
