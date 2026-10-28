package synccmd_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// A rebase that failed after the fast-forward and was put back, with no
// undo at all.
func TestAFastForwardThatWasRestoredLeavesNoFormerTip(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	f.Declare(t)
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	x, opts := f.PushedWork(t)
	var buf bytes.Buffer
	y, err := provision.New(f.Ctx, "feat/y", provision.NewOptions{NoSetup: true, Base: "feat_wt/x"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	testkit.Mine(t, y, "y.txt", "y\n")
	theirs := f.Theirs(t, "feat_wt/x", "c.txt", "theirs\n")
	f.Theirs(t, "main", "c.txt", "trunk\n")
	run, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
	if err == nil || run.Worktrees[0].Result != synccmd.ResultRestored || !run.Worktrees[0].OwnRemoteSync.FastForwarded {
		t.Fatalf("%v %+v", err, run.Worktrees[0])
	}
	// The child goes, so that x is a worktree a run can take by itself.
	testkit.GitIn(t, f.Main, "worktree", "remove", "--force", y)
	testkit.GitIn(t, f.Main, "branch", "-D", "feat_wt/y")
	synccmdtest.MustBeDiverged(t, f, x, opts, theirs)
}

// The fetch asks for exactly the branches it checks. A remote branch that
// went away and came back as a directory of branches clashes with the stale
// tracking ref when it is fetched by pattern; a sibling whose name merely
// starts the same is nobody's business.
func TestUpFetchesExactlyTheBranchItChecks(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	t.Run("a directory where the branch was", func(t *testing.T) {
		f := synccmdtest.NewTrunkFixture(t)
		_, opts := f.PushedWork(t)
		tip := testkit.GitOut(t, f.Origin, "rev-parse", "refs/heads/feat_wt/x")
		testkit.GitIn(t, f.Origin, "update-ref", "-d", "refs/heads/feat_wt/x")
		testkit.GitIn(t, f.Origin, "update-ref", "refs/heads/feat_wt/x/a", tip)
		// Trunk moves; nothing here fetches, so origin/feat_wt/x stays as it
		// was, in the way of feat_wt/x/a.
		testkit.GitIn(t, f.Other, "commit", "-q", "--allow-empty", "-m", "origin moves")
		testkit.GitIn(t, f.Other, "push", "-q", "origin", "main")
		r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
		if err != nil || r.Worktrees[0].Result != synccmd.ResultRebased || r.Worktrees[0].OwnRemoteSync.State != "gone" {
			t.Fatalf("%v %+v\n%s", err, r, human)
		}
	})
	t.Run("a sibling that starts the same", func(t *testing.T) {
		f := synccmdtest.NewTrunkFixture(t)
		_, opts := f.PushedWork(t)
		tip := testkit.GitOut(t, f.Origin, "rev-parse", "refs/heads/feat_wt/x")
		testkit.GitIn(t, f.Origin, "update-ref", "refs/heads/feat_wt/x-2", tip)
		testkit.GitIn(t, f.Origin, "update-ref", "refs/heads/feat_wt/xy", tip)
		f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
		testkit.GitIn(t, f.Other, "commit", "-q", "--allow-empty", "-m", "origin moves")
		testkit.GitIn(t, f.Other, "push", "-q", "origin", "main")
		r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
		if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded {
			t.Fatalf("%v %+v\n%s", err, r, human)
		}
		if refs := testkit.GitOut(t, f.Main, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/feat_wt/"); refs != "refs/remotes/origin/feat_wt/x" {
			t.Fatalf("fetched more than the branch:\n%s", refs)
		}
	})
}

// wt up must run on any git main runs on: nothing this check adds may need
// a newer one. A git that does not know fetch --porcelain stands in for one
// older than 2.41.
func TestUpRunsOnAGitWithoutFetchPorcelain(t *testing.T) {
	// Not parallel: puts a fake git on the process-wide PATH.
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	theirs := f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.AdvanceOrigin(t, 1)
	bin := t.TempDir()
	// Only fetch: git status --porcelain is as old as git.
	script := "#!/bin/sh\nfetch=\nfor a in \"$@\"; do case \"$a\" in fetch) fetch=1;; --porcelain) [ -n \"$fetch\" ] && { echo \"error: unknown option \\`porcelain'\" >&2; exit 129; };; esac; done\nexec " + realGit + " \"$@\"\n"
	gittest.WriteExecutable(t, filepath.Join(bin, "git"), script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded || testkit.Str(r.Worktrees[0].OwnRemoteSync.Remote) != theirs {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	if testkit.GitOut(t, x, "show", "HEAD:t.txt") != "t" {
		t.Fatal("the remote's commit is not in the rebased branch")
	}
}

// --allow-diverged is consent to rebase one branch over what its remote
// has: like --force it needs the worktree named.
func TestSyncRunAllowDivergedNeedsTheWorktreeNamed(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	f.Declare(t)
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	x, opts := f.PushedWork(t)
	f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := testkit.Mine(t, x, "m.txt", "m\n")
	f.AdvanceOrigin(t, 1)
	opts.AllowDiverged = true
	var out bytes.Buffer
	err := synccmd.SyncRebase(f.Ctx, nil, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "--allow-diverged") || !strings.Contains(err.Error(), "name it") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("the branch moved")
	}
	if err := synccmd.SyncRebase(f.Ctx, []string{"x"}, opts, &out); err != nil {
		t.Fatalf("named: %v\n%s", err, out.String())
	}
}

// allowed says the run went on over a divergence: not that the flag was
// given to a run that then refused the worktree for something else.
func TestAllowedIsOnlyARunThatWentOn(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	testkit.Mine(t, x, "m.txt", "m\n")
	f.AdvanceOrigin(t, 1)
	testkit.WriteIn(t, x, "x.txt", "edited\n")
	opts.AllowDiverged = true
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil || r.Worktrees[0].Result != synccmd.ResultRefused || r.Worktrees[0].OwnRemoteSync.Allowed {
		t.Fatalf("%v %+v %+v\n%s", err, r.Worktrees[0], r.Worktrees[0].OwnRemoteSync, human)
	}
}

// The overview's token covers the remote commit of a diverged row also when
// a busy session keeps a run off it: --force lifts the session, and what the
// run then goes over has to be what the caller was shown.
func TestSyncTokenCoversADivergedRowBehindABusySession(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	f.Declare(t)
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	x, opts := f.PushedWork(t)
	f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := testkit.Mine(t, x, "m.txt", "m\n")
	f.AdvanceOrigin(t, 1)
	busy := []agents.Agent{{Name: "busy-1", Cwd: x, Status: "busy"}}
	token := func() string {
		t.Helper()
		_, sha, err := synccmd.TrunkTip(f.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := wtsync.LoadFromRef(f.Main, sha)
		if err != nil {
			t.Fatal(err)
		}
		sv, err := synccmd.SurveyRepo(f.Ctx, sha, cfg, busy, wtsync.ReadOwnOrUnknown(f.Main, "main"))
		if err != nil {
			t.Fatal(err)
		}
		if row := sv.Rows[0]; row.Group != synccmd.GroupSkipped || testkit.Str(row.OwnRemote.Blocks) != wtsync.OwnBlockDiverged {
			t.Fatalf("row %+v", row)
		}
		tok := sv.Token(f.Ctx, "main", sha)
		if tok == nil {
			t.Fatal("no token for a row a run with --force --allow-diverged starts on")
		}
		return *tok
	}
	shown := token()
	f.Theirs(t, "feat_wt/x", "t2.txt", "t2\n")
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	if token() == shown {
		t.Fatal("the remote moved and the token did not")
	}
	opts.Agents, opts.Relist = busy, func() ([]agents.Agent, error) { return busy, nil }
	opts.Force, opts.AllowDiverged, opts.Expect = true, true, shown
	r, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
	if err == nil || len(r.Worktrees) != 0 || !strings.Contains(testkit.Str(r.Error), "the overview changed since it was read") {
		t.Fatalf("%v %+v", err, r)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("the branch moved")
	}
}

// Diverged from its remote and on trunk already: wt up would skip it, so
// nothing is pushed over anything and the divergence keeps no run off. The
// look says the same as the run does.
func TestADivergedBranchARunWouldNotRebaseIsNotBlocked(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := testkit.Mine(t, x, "m.txt", "m\n")
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	p := synccmdtest.PlanOfWork(t, f.Ctx, "x")
	if o := p.Worktree.OwnRemote; !p.UpEligible || p.UpIneligibleCode != nil || o.State != "diverged" || o.Blocks != nil {
		t.Fatalf("plan: eligible %v, code %s, %+v", p.UpEligible, testkit.Str(p.UpIneligibleCode), o)
	}
	if testkit.Str(p.Token) != legacyPlanToken(f.Ctx, "main", []string{"feat_wt/x"}) {
		t.Fatal("a divergence that blocks nothing is in the token")
	}
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err != nil || r.Worktrees[0].Result != synccmd.ResultSkipped || r.Worktrees[0].OwnRemoteSync.State != "diverged" || r.Worktrees[0].OwnRemoteSync.SkippedReason != nil {
		t.Fatalf("%v %+v %+v\n%s", err, r.Worktrees[0], r.Worktrees[0].OwnRemoteSync, human)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("the branch moved")
	}
}

// A branch deleted on the remote between the listing and the fetch is gone,
// not a fetch that failed: the run goes on, and trunk is not blamed.
func TestABranchDeletedBetweenTheListingAndTheFetchIsGone(t *testing.T) {
	// Not parallel: puts a fake git on the process-wide PATH.
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	f := synccmdtest.NewTrunkFixture(t)
	_, opts := f.PushedWork(t)
	testkit.GitIn(t, f.Other, "commit", "-q", "--allow-empty", "-m", "origin moves")
	testkit.GitIn(t, f.Other, "push", "-q", "origin", "main")
	bin, marker := t.TempDir(), filepath.Join(t.TempDir(), "deleted")
	// The first ls-remote answers, and then the branch goes.
	script := "#!/bin/sh\ncase \" $* \" in *\" ls-remote \"*) " + realGit + " \"$@\"; s=$?; [ -e " + marker + " ] || { " + realGit + " -C " + f.Origin +
		" update-ref -d refs/heads/feat_wt/x; : > " + marker + "; }; exit $s;; esac\nexec " + realGit + " \"$@\"\n"
	gittest.WriteExecutable(t, filepath.Join(bin, "git"), script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err != nil || r.Error != nil || r.Worktrees[0].Result != synccmd.ResultRebased || r.Worktrees[0].OwnRemoteSync.State != "gone" {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the fixture never deleted the branch")
	}
}

// A signal that lands after the merge and before the run has said so still
// reports where the branch is: at its remote's commit, fast-forwarded.
func TestAnInterruptedRunReportsAFastForwardItHadMade(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	x, _ := f.PushedWork(t)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	theirs := f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	own, err := wtsync.ReadOwn(f.Main, "main")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	j := synccmd.NewRunJournal(&out)
	j.Repo(f.Main)
	j.Join("x", "feat_wt/x", x, before, synccmd.OwnRemoteSyncOf(own.State("feat_wt/x")))
	testkit.GitIn(t, x, "merge", "-q", "--ff-only", theirs)
	j.Interrupted("x", "interrupted while fast-forwarding x: wt sync undo x puts it back")
	testkit.ValidateJSON(t, "up", out.Bytes())
	var r synccmd.UpResult
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	w := r.Worktrees[0]
	if w.Result != synccmd.ResultInterrupted || testkit.Str(w.Before) != before || testkit.Str(w.After) != theirs || !w.OwnRemoteSync.FastForwarded {
		t.Fatalf("%+v %+v", w, w.OwnRemoteSync)
	}
}

// A branch whose own ref will not fetch, because a stale tracking ref here
// is in its way, is that branch's trouble: refused by itself with a reason
// that is a whole clause, and trunk fetched all the same.
func TestUpRefusesOnlyTheBranchWhoseRefWillNotFetch(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	testkit.GitIn(t, f.Other, "commit", "-q", "--allow-empty", "-m", "origin moves")
	testkit.GitIn(t, f.Other, "push", "-q", "origin", "main")
	trunk := testkit.GitOut(t, f.Other, "rev-parse", "HEAD")
	// A stale origin/feat_wt/x/a here, where origin has feat_wt/x.
	testkit.GitIn(t, f.Main, "update-ref", "-d", "refs/remotes/origin/feat_wt/x")
	testkit.GitIn(t, f.Main, "update-ref", "refs/remotes/origin/feat_wt/x/a", before)
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil || r.Error != nil || !r.Fetched || testkit.Str(r.Onto) != trunk {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	w := r.Worktrees[0]
	reason := testkit.Str(w.Reason)
	if w.Result != synccmd.ResultRefused || testkit.Str(w.OwnRemoteSync.SkippedReason) != synccmd.OwnSkipFetchFailed || !strings.Contains(reason, "its own remote could not be fetched (") {
		t.Fatalf("%+v %+v\n%s", w, w.OwnRemoteSync, human)
	}
	if strings.Contains(reason, "try running") || strings.Contains(reason, "…") {
		t.Fatalf("the reason is cut mid-sentence: %q", reason)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("the branch moved")
	}
}
