package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// mustBeDiverged is the check every shape below ends with: one commit made
// on a branch that no longer has its remote's commit, though a wt run once
// fast-forwarded it there. The remote's commit is not a former tip of the
// branch: wt put it there and wt took it away. A run refuses, and git's own
// --force-if-includes refuses the push a person might try by hand.
func mustBeDiverged(t *testing.T, f *trunkFixture, x string, opts RunOptions, theirs string) {
	t.Helper()
	mine(t, x, "after.txt", "after\n")
	p := planOfWork(t, f.ctx, "x")
	if o := p.Worktree.OwnRemote; o.State != "diverged" || str(o.Blocks) != wtsync.OwnBlockDiverged || str(p.UpIneligibleCode) != IneligibleOwnDiverged {
		t.Fatalf("the remote's commit reads as a former tip: %+v, code %s", o, str(p.UpIneligibleCode))
	}
	r, human, err := upOut(t, f.ctx, "x", at(opts, 500))
	if err == nil || r.Worktrees[0].Result != ResultRefused || str(r.Worktrees[0].OwnRemoteSync.SkippedReason) != wtsync.OwnBlockDiverged {
		t.Fatalf("a run went on over the remote's commit: %v %+v\n%s", err, r.Worktrees, human)
	}
	if out, err := gittest.Try(t, x, "push", "--force-with-lease", "--force-if-includes", "origin", "feat_wt/x"); err == nil {
		t.Fatalf("git's own --force-if-includes let the push through:\n%s", out)
	}
	if got := gitOut(t, f.origin, "rev-parse", "refs/heads/feat_wt/x"); got != theirs {
		t.Fatalf("origin's branch is at %s, want it still at %s", got, theirs)
	}
}

// Behind, fast-forwarded and rebased by a run, then undone.
func TestAFastForwardThatWasUndoneLeavesNoFormerTip(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	before := gitOut(t, x, "rev-parse", "HEAD")
	theirs := f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.advanceOrigin(t, 1)
	if r, human, err := upOut(t, f.ctx, "x", opts); err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded {
		t.Fatalf("%v\n%s", err, human)
	}
	var out bytes.Buffer
	if err := SyncUndo(f.ctx, "x", UndoOptions{verbOptions: opts.verbOptions}, &out); err != nil || gitOut(t, x, "rev-parse", "HEAD") != before {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	mustBeDiverged(t, f, x, opts, theirs)
}

// The same for the fast-forward that is all a run did.
func TestAFastForwardAloneThatWasUndoneLeavesNoFormerTip(t *testing.T) {
	f, x, opts, local, remote := nothingToRebase(t)
	if r, err := syncRunJSON(t, f.ctx, []string{"x"}, opts); err != nil || r.Worktrees[0].Result != ResultFastForwarded {
		t.Fatalf("%v %+v", err, r.Worktrees)
	}
	var out bytes.Buffer
	if err := SyncUndo(f.ctx, "x", UndoOptions{verbOptions: opts.verbOptions}, &out); err != nil || gitOut(t, x, "rev-parse", "HEAD") != local {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	mustBeDiverged(t, f, x, opts, remote)
}

// A rebase that failed after the fast-forward and was put back, with no
// undo at all.
func TestAFastForwardThatWasRestoredLeavesNoFormerTip(t *testing.T) {
	f := newTrunkFixture(t)
	f.declare(t)
	gitIn(t, f.main, "fetch", "-q", "origin")
	x, opts := f.pushedWork(t)
	var buf bytes.Buffer
	y, err := New(f.ctx, "feat/y", NewOptions{NoSetup: true, Base: "feat_wt/x"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	mine(t, y, "y.txt", "y\n")
	theirs := f.theirs(t, "feat_wt/x", "c.txt", "theirs\n")
	f.theirs(t, "main", "c.txt", "trunk\n")
	run, err := syncRunJSON(t, f.ctx, []string{"x"}, opts)
	if err == nil || run.Worktrees[0].Result != ResultRestored || !run.Worktrees[0].OwnRemoteSync.FastForwarded {
		t.Fatalf("%v %+v", err, run.Worktrees[0])
	}
	// The child goes, so that x is a worktree a run can take by itself.
	gitIn(t, f.main, "worktree", "remove", "--force", y)
	gitIn(t, f.main, "branch", "-D", "feat_wt/y")
	mustBeDiverged(t, f, x, opts, theirs)
}

// The fetch asks for exactly the branches it checks. A remote branch that
// went away and came back as a directory of branches clashes with the stale
// tracking ref when it is fetched by pattern; a sibling whose name merely
// starts the same is nobody's business.
func TestUpFetchesExactlyTheBranchItChecks(t *testing.T) {
	t.Run("a directory where the branch was", func(t *testing.T) {
		f := newTrunkFixture(t)
		_, opts := f.pushedWork(t)
		tip := gitOut(t, f.origin, "rev-parse", "refs/heads/feat_wt/x")
		gitIn(t, f.origin, "update-ref", "-d", "refs/heads/feat_wt/x")
		gitIn(t, f.origin, "update-ref", "refs/heads/feat_wt/x/a", tip)
		// Trunk moves; nothing here fetches, so origin/feat_wt/x stays as it
		// was, in the way of feat_wt/x/a.
		gitIn(t, f.other, "commit", "-q", "--allow-empty", "-m", "origin moves")
		gitIn(t, f.other, "push", "-q", "origin", "main")
		r, human, err := upOut(t, f.ctx, "x", opts)
		if err != nil || r.Worktrees[0].Result != ResultRebased || r.Worktrees[0].OwnRemoteSync.State != "gone" {
			t.Fatalf("%v %+v\n%s", err, r, human)
		}
	})
	t.Run("a sibling that starts the same", func(t *testing.T) {
		f := newTrunkFixture(t)
		_, opts := f.pushedWork(t)
		tip := gitOut(t, f.origin, "rev-parse", "refs/heads/feat_wt/x")
		gitIn(t, f.origin, "update-ref", "refs/heads/feat_wt/x-2", tip)
		gitIn(t, f.origin, "update-ref", "refs/heads/feat_wt/xy", tip)
		f.theirs(t, "feat_wt/x", "t.txt", "t\n")
		gitIn(t, f.other, "commit", "-q", "--allow-empty", "-m", "origin moves")
		gitIn(t, f.other, "push", "-q", "origin", "main")
		r, human, err := upOut(t, f.ctx, "x", opts)
		if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded {
			t.Fatalf("%v %+v\n%s", err, r, human)
		}
		if refs := gitOut(t, f.main, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/feat_wt/"); refs != "refs/remotes/origin/feat_wt/x" {
			t.Fatalf("fetched more than the branch:\n%s", refs)
		}
	})
}

// wt up must run on any git main runs on: nothing this check adds may need
// a newer one. A git that does not know fetch --porcelain stands in for one
// older than 2.41.
func TestUpRunsOnAGitWithoutFetchPorcelain(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	theirs := f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.advanceOrigin(t, 1)
	bin := t.TempDir()
	// Only fetch: git status --porcelain is as old as git.
	script := "#!/bin/sh\nfetch=\nfor a in \"$@\"; do case \"$a\" in fetch) fetch=1;; --porcelain) [ -n \"$fetch\" ] && { echo \"error: unknown option \\`porcelain'\" >&2; exit 129; };; esac; done\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded || str(r.Worktrees[0].OwnRemoteSync.Remote) != theirs {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	if gitOut(t, x, "show", "HEAD:t.txt") != "t" {
		t.Fatal("the remote's commit is not in the rebased branch")
	}
}

// --allow-diverged is consent to rebase one branch over what its remote
// has: like --force it needs the worktree named.
func TestSyncRunAllowDivergedNeedsTheWorktreeNamed(t *testing.T) {
	f := newTrunkFixture(t)
	f.declare(t)
	gitIn(t, f.main, "fetch", "-q", "origin")
	x, opts := f.pushedWork(t)
	f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := mine(t, x, "m.txt", "m\n")
	f.advanceOrigin(t, 1)
	opts.AllowDiverged = true
	var out bytes.Buffer
	err := SyncRebase(f.ctx, nil, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "--allow-diverged") || !strings.Contains(err.Error(), "name it") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("the branch moved")
	}
	if err := SyncRebase(f.ctx, []string{"x"}, opts, &out); err != nil {
		t.Fatalf("named: %v\n%s", err, out.String())
	}
}

// allowed says the run went on over a divergence: not that the flag was
// given to a run that then refused the worktree for something else.
func TestAllowedIsOnlyARunThatWentOn(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	mine(t, x, "m.txt", "m\n")
	f.advanceOrigin(t, 1)
	writeIn(t, x, "x.txt", "edited\n")
	opts.AllowDiverged = true
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err == nil || r.Worktrees[0].Result != ResultRefused || r.Worktrees[0].OwnRemoteSync.Allowed {
		t.Fatalf("%v %+v %+v\n%s", err, r.Worktrees[0], r.Worktrees[0].OwnRemoteSync, human)
	}
}

// A fast-forward that is all a run did counts as finished: a commit made on
// top is plainly ahead, and once the run is undone the branch is behind
// again, nothing else.
func TestAFastForwardAloneIsFinishedAndItsUndoIsBehindAgain(t *testing.T) {
	f, x, opts, local, remote := nothingToRebase(t)
	if r, err := syncRunJSON(t, f.ctx, []string{"x"}, opts); err != nil || r.Worktrees[0].Result != ResultFastForwarded {
		t.Fatalf("%v %+v", err, r.Worktrees)
	}
	if refs := gitOut(t, f.main, "for-each-ref", "--format=%(refname) %(objectname)", "refs/wt-sync-result/", "refs/wt-sync-ff/"); strings.Count(refs, remote) != 2 {
		t.Fatalf("a finished fast-forward pins where it left the branch twice over:\n%s", refs)
	}
	mine(t, x, "on-top.txt", "on top\n")
	if o := planOfWork(t, f.ctx, "x").Worktree.OwnRemote; o.State != "ahead" || *o.Ahead != 1 {
		t.Fatalf("a commit on top: %+v", o)
	}
	gitIn(t, x, "reset", "-q", "--hard", remote)
	var out bytes.Buffer
	if err := SyncUndo(f.ctx, "x", UndoOptions{verbOptions: opts.verbOptions}, &out); err != nil || gitOut(t, x, "rev-parse", "HEAD") != local {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if o := planOfWork(t, f.ctx, "x").Worktree.OwnRemote; o.State != "behind" || o.Blocks != nil {
		t.Fatalf("after the undo: %+v", o)
	}
	if refs := gitOut(t, f.main, "for-each-ref", "--format=%(refname)", "refs/wt-sync-result/", "refs/wt-sync-ff/"); refs != "" {
		t.Fatalf("the undo left the run's word that the branch was there:\n%s", refs)
	}
}

// A fast-forward, a conflict handed over and a resume that finishes it leave
// the remote's commit a former tip, and a second run goes on; the same
// handover undone leaves none.
func TestAHandoverAfterAFastForwardKeepsOrDropsTheFormerTip(t *testing.T) {
	handedOver := func(t *testing.T) (f *trunkFixture, x string, opts RunOptions, theirs string) {
		t.Helper()
		f = newTrunkFixture(t)
		f.declare(t)
		gitIn(t, f.main, "fetch", "-q", "origin")
		x, opts = f.pushedWork(t)
		theirs = f.theirs(t, "feat_wt/x", "c.txt", "theirs\n")
		f.theirs(t, "main", "c.txt", "trunk\n")
		run, err := syncRunJSON(t, f.ctx, []string{"x"}, opts)
		if err == nil || run.Worktrees[0].Result != ResultHandedOver || !run.Worktrees[0].OwnRemoteSync.FastForwarded {
			t.Fatalf("%v %+v", err, run.Worktrees[0])
		}
		return f, x, opts, theirs
	}
	t.Run("resumed", func(t *testing.T) {
		f, x, opts, _ := handedOver(t)
		writeIn(t, x, "c.txt", "both\n")
		gitIn(t, x, "add", "c.txt")
		if r, err := syncResumeJSON(t, f.ctx, "x", ResumeOptions{pushOptions: pushOptions{Push: PushNever}, verbOptions: opts.verbOptions}); err != nil || r.Worktrees[0].Result != ResultRebased {
			t.Fatalf("resume: %v %+v", err, r.Worktrees)
		}
		if o := planOfWork(t, f.ctx, "x").Worktree.OwnRemote; o.State != "rebased" {
			t.Fatalf("after the resume: %+v", o)
		}
		f.advanceOrigin(t, 1)
		if r, human, err := upOut(t, f.ctx, "x", at(opts, 500)); err != nil || r.Worktrees[0].Result != ResultRebased || r.Worktrees[0].OwnRemoteSync.State != "rebased" {
			t.Fatalf("a second run: %v %+v\n%s", err, r.Worktrees, human)
		}
	})
	t.Run("undone", func(t *testing.T) {
		f, x, opts, theirs := handedOver(t)
		var out bytes.Buffer
		if err := SyncUndo(f.ctx, "x", UndoOptions{verbOptions: opts.verbOptions}, &out); err != nil {
			t.Fatalf("undo: %v\n%s", err, out.String())
		}
		// Trunk's c.txt is in the way of nothing here: the run that follows
		// is refused for the divergence before it rebases anything.
		mustBeDiverged(t, f, x, opts, theirs)
	})
}

// The overview's token covers the remote commit of a diverged row also when
// a busy session keeps a run off it: --force lifts the session, and what the
// run then goes over has to be what the caller was shown.
func TestSyncTokenCoversADivergedRowBehindABusySession(t *testing.T) {
	f := newTrunkFixture(t)
	f.declare(t)
	gitIn(t, f.main, "fetch", "-q", "origin")
	x, opts := f.pushedWork(t)
	f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := mine(t, x, "m.txt", "m\n")
	f.advanceOrigin(t, 1)
	busy := []wtsync.Agent{{Name: "busy-1", Cwd: x, Status: "busy"}}
	token := func() string {
		t.Helper()
		_, sha, err := trunkTip(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := wtsync.LoadFromRef(f.main, sha)
		if err != nil {
			t.Fatal(err)
		}
		sv, err := surveyRepo(f.ctx, sha, cfg, busy, wtsync.ReadOwnOrUnknown(f.main, "main"))
		if err != nil {
			t.Fatal(err)
		}
		if row := sv.rows[0]; row.Group != GroupSkipped || str(row.OwnRemote.Blocks) != wtsync.OwnBlockDiverged {
			t.Fatalf("row %+v", row)
		}
		tok := sv.token(f.ctx, "main", sha)
		if tok == nil {
			t.Fatal("no token for a row a run with --force --allow-diverged starts on")
		}
		return *tok
	}
	shown := token()
	f.theirs(t, "feat_wt/x", "t2.txt", "t2\n")
	gitIn(t, f.main, "fetch", "-q", "origin")
	if token() == shown {
		t.Fatal("the remote moved and the token did not")
	}
	opts.Agents, opts.Relist = busy, func() ([]wtsync.Agent, error) { return busy, nil }
	opts.Force, opts.AllowDiverged, opts.Expect = true, true, shown
	r, err := syncRunJSON(t, f.ctx, []string{"x"}, opts)
	if err == nil || len(r.Worktrees) != 0 || !strings.Contains(str(r.Error), "the overview changed since it was read") {
		t.Fatalf("%v %+v", err, r)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("the branch moved")
	}
}

// Diverged from its remote and on trunk already: wt up would skip it, so
// nothing is pushed over anything and the divergence keeps no run off. The
// look says the same as the run does.
func TestADivergedBranchARunWouldNotRebaseIsNotBlocked(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := mine(t, x, "m.txt", "m\n")
	gitIn(t, f.main, "fetch", "-q", "origin")
	p := planOfWork(t, f.ctx, "x")
	if o := p.Worktree.OwnRemote; !p.UpEligible || p.UpIneligibleCode != nil || o.State != "diverged" || o.Blocks != nil {
		t.Fatalf("plan: eligible %v, code %s, %+v", p.UpEligible, str(p.UpIneligibleCode), o)
	}
	if str(p.Token) != legacyPlanToken(f.ctx, "main", []string{"feat_wt/x"}) {
		t.Fatal("a divergence that blocks nothing is in the token")
	}
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err != nil || r.Worktrees[0].Result != ResultSkipped || r.Worktrees[0].OwnRemoteSync.State != "diverged" || r.Worktrees[0].OwnRemoteSync.SkippedReason != nil {
		t.Fatalf("%v %+v %+v\n%s", err, r.Worktrees[0], r.Worktrees[0].OwnRemoteSync, human)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("the branch moved")
	}
}

// A branch deleted on the remote between the listing and the fetch is gone,
// not a fetch that failed: the run goes on, and trunk is not blamed.
func TestABranchDeletedBetweenTheListingAndTheFetchIsGone(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	f := newTrunkFixture(t)
	_, opts := f.pushedWork(t)
	gitIn(t, f.other, "commit", "-q", "--allow-empty", "-m", "origin moves")
	gitIn(t, f.other, "push", "-q", "origin", "main")
	bin, marker := t.TempDir(), filepath.Join(t.TempDir(), "deleted")
	// The first ls-remote answers, and then the branch goes.
	script := "#!/bin/sh\ncase \" $* \" in *\" ls-remote \"*) " + realGit + " \"$@\"; s=$?; [ -e " + marker + " ] || { " + realGit + " -C " + f.origin +
		" update-ref -d refs/heads/feat_wt/x; : > " + marker + "; }; exit $s;; esac\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err != nil || r.Error != nil || r.Worktrees[0].Result != ResultRebased || r.Worktrees[0].OwnRemoteSync.State != "gone" {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the fixture never deleted the branch")
	}
}

// A forced undo of a handover that followed a fast-forward keeps what a
// person committed inside the rebase, as it does without a fast-forward:
// the branch ref sits at the remote's commit there, not at the tip the run
// found, and the pin must not depend on which.
func TestSyncUndoForcedPinsACommitInsideAHandoverThatFollowedAFastForward(t *testing.T) {
	f := newTrunkFixture(t)
	f.declare(t)
	gitIn(t, f.main, "fetch", "-q", "origin")
	x, opts := f.pushedWork(t)
	before := gitOut(t, x, "rev-parse", "HEAD")
	f.theirs(t, "feat_wt/x", "c.txt", "theirs\n")
	f.theirs(t, "main", "c.txt", "trunk\n")
	if run, err := syncRunJSON(t, f.ctx, []string{"x"}, opts); err == nil || run.Worktrees[0].Result != ResultHandedOver || !run.Worktrees[0].OwnRemoteSync.FastForwarded {
		t.Fatalf("%v %+v", err, run.Worktrees[0])
	}
	writeIn(t, x, "c.txt", "merged by hand\n")
	gitIn(t, x, "add", "--", "c.txt")
	gitIn(t, x, "commit", "-q", "-m", "resolved by hand")
	inside := gitOut(t, x, "rev-parse", "HEAD")

	undo := UndoOptions{verbOptions: at(opts, 100).verbOptions}
	var out bytes.Buffer
	if err := SyncUndo(f.ctx, "x", undo, &out); err == nil || !strings.Contains(err.Error(), "made inside the handed-over rebase") {
		t.Fatalf("a plain undo: %v\n%s", err, out.String())
	}
	undo.Force = true
	out.Reset()
	if err := SyncUndo(f.ctx, "x", undo, &out); err != nil {
		t.Fatalf("forced: %v\n%s", err, out.String())
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatalf("the branch is at %s, want the tip the run found %s", got, before)
	}
	if pinned, err := gittest.Try(t, f.main, "rev-parse", "--verify", "--quiet", "refs/wt-sync/feat_wt/x/100"); err != nil || strings.TrimSpace(pinned) != inside {
		t.Fatalf("the commit made inside the rebase is not pinned: %q %v\n%s", pinned, err, out.String())
	}
	if !strings.Contains(out.String(), inside[:7]+" kept under refs/wt-sync/feat_wt/x/100") {
		t.Fatalf("the row does not say where it kept it:\n%s", out.String())
	}
}

// An interrupt between the fast-forward and the rebase has no rebase to
// abort: the way back is wt sync undo, which also takes away the word that
// the branch was at its remote's commit.
func TestOnInterruptAfterAFastForwardPointsAtUndo(t *testing.T) {
	var out bytes.Buffer
	onInterrupt(&out, nil, &rebaseInFlight{work: "bump", path: "/w/bump", safety: "refs/wt-sync/feat_wt/bump/99", forwarding: true})
	s := out.String()
	if !strings.Contains(s, "interrupted while fast-forwarding bump") || !strings.Contains(s, "wt sync undo bump") {
		t.Fatalf("out %q", s)
	}
	if strings.Contains(s, "rebase --abort") || strings.Contains(s, "reset --hard") {
		t.Fatalf("the interrupt advises a way back that leaves the remote's commit a former tip: %q", s)
	}
}

// A signal that lands after the merge and before the run has said so still
// reports where the branch is: at its remote's commit, fast-forwarded.
func TestAnInterruptedRunReportsAFastForwardItHadMade(t *testing.T) {
	f := newTrunkFixture(t)
	x, _ := f.pushedWork(t)
	before := gitOut(t, x, "rev-parse", "HEAD")
	theirs := f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	gitIn(t, f.main, "fetch", "-q", "origin")
	own, err := wtsync.ReadOwn(f.main, "main")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	j := NewRunJournal(&out)
	j.repo(f.main)
	j.join("x", "feat_wt/x", x, before, ownRemoteSyncOf(own.State("feat_wt/x")))
	gitIn(t, x, "merge", "-q", "--ff-only", theirs)
	j.interrupted("x", "interrupted while fast-forwarding x: wt sync undo x puts it back")
	validateJSON(t, "up", out.Bytes())
	var r UpResult
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	w := r.Worktrees[0]
	if w.Result != ResultInterrupted || str(w.Before) != before || str(w.After) != theirs || !w.OwnRemoteSync.FastForwarded {
		t.Fatalf("%+v %+v", w, w.OwnRemoteSync)
	}
}

// A branch whose own ref will not fetch, because a stale tracking ref here
// is in its way, is that branch's trouble: refused by itself with a reason
// that is a whole clause, and trunk fetched all the same.
func TestUpRefusesOnlyTheBranchWhoseRefWillNotFetch(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	before := gitOut(t, x, "rev-parse", "HEAD")
	gitIn(t, f.other, "commit", "-q", "--allow-empty", "-m", "origin moves")
	gitIn(t, f.other, "push", "-q", "origin", "main")
	trunk := gitOut(t, f.other, "rev-parse", "HEAD")
	// A stale origin/feat_wt/x/a here, where origin has feat_wt/x.
	gitIn(t, f.main, "update-ref", "-d", "refs/remotes/origin/feat_wt/x")
	gitIn(t, f.main, "update-ref", "refs/remotes/origin/feat_wt/x/a", before)
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err == nil || r.Error != nil || !r.Fetched || str(r.Onto) != trunk {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	w := r.Worktrees[0]
	reason := str(w.Reason)
	if w.Result != ResultRefused || str(w.OwnRemoteSync.SkippedReason) != OwnSkipFetchFailed || !strings.Contains(reason, "its own remote could not be fetched (") {
		t.Fatalf("%+v %+v\n%s", w, w.OwnRemoteSync, human)
	}
	if strings.Contains(reason, "try running") || strings.Contains(reason, "…") {
		t.Fatalf("the reason is cut mid-sentence: %q", reason)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("the branch moved")
	}
}
