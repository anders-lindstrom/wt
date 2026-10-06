package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// pushedWork is trunkFixture.withWork with worktree x pushed to origin under
// its own name and tracking it: x is the checkout, opts the run options.
func (f *trunkFixture) pushedWork(t *testing.T) (x string, opts RunOptions) {
	t.Helper()
	opts = f.withWork(t)
	wt, err := Locate(f.ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt.Path, "push", "-q", "-u", "origin", "feat_wt/x")
	return wt.Path, opts
}

// theirs commits file on branch in the second clone and pushes it: a commit
// origin has and the main checkout has not fetched.
func (f *trunkFixture) theirs(t *testing.T, branch, file, content string) string {
	t.Helper()
	gitIn(t, f.other, "fetch", "-q", "origin")
	gitIn(t, f.other, "checkout", "-q", "-B", branch, "origin/"+branch)
	writeIn(t, f.other, file, content)
	gitIn(t, f.other, "add", "-A")
	gitIn(t, f.other, "commit", "-q", "-m", "theirs "+file)
	gitIn(t, f.other, "push", "-q", "origin", branch)
	sha := gitOut(t, f.other, "rev-parse", "HEAD")
	// Back on main, where advanceOrigin commits.
	gitIn(t, f.other, "checkout", "-q", "-B", "main", "origin/main")
	return sha
}

// mine commits file in the checkout at dir.
func mine(t *testing.T, dir, file, content string) string {
	t.Helper()
	writeIn(t, dir, file, content)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "mine "+file)
	return gitOut(t, dir, "rev-parse", "HEAD")
}

// at is run options for a later run of the same test: every run pins its
// safety ref under its own clock.
func at(opts RunOptions, nanos int64) RunOptions {
	opts.Now = func() time.Time { return time.Unix(0, nanos) }
	return opts
}

// upOut is upJSON with what a person would have read.
func upOut(t *testing.T, ctx *Context, work string, opts RunOptions) (UpResult, string, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = NewRunJournal(&out)
	err := Up(ctx, work, opts, &human)
	validateJSON(t, "up", out.Bytes())
	var r UpResult
	if jerr := json.Unmarshal(out.Bytes(), &r); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	return r, human.String(), err
}

func str(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func safetyRefs(t *testing.T, main string) string {
	t.Helper()
	return gitOut(t, main, "for-each-ref", "--format=%(refname)", "refs/wt-sync/", "refs/wt-sync-result/")
}

// A branch behind its own remote is fast-forwarded and then rebased, with
// the remote's commit in it; undo puts it back where it stood before both.
func TestUpFastForwardsABranchBehindItsRemoteThenRebases(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	before := gitOut(t, x, "rev-parse", "HEAD")
	theirs := f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.advanceOrigin(t, 1)

	r, human, err := upOut(t, f.ctx, "x", opts)
	if err != nil {
		t.Fatalf("up: %v\n%s", err, human)
	}
	w := r.Worktrees[0]
	o := w.OwnRemoteSync
	if w.Result != ResultRebased || str(w.Before) != before || o == nil {
		t.Fatalf("participant %+v", w)
	}
	if o.State != "behind" || str(o.Ref) != "origin/feat_wt/x" || str(o.Local) != before || str(o.Remote) != theirs ||
		*o.RemoteAhead != 1 || *o.LocalAhead != 0 || !o.FastForwarded || o.Allowed || o.SkippedReason != nil {
		t.Fatalf("ownRemoteSync %+v", o)
	}
	if !strings.Contains(human, "✓ fast-forwarded to origin/feat_wt/x  "+before[:7]+" → "+theirs[:7]+" (1 commit)\n") {
		t.Fatalf("no line about the fast-forward:\n%s", human)
	}
	if strings.Index(human, "fast-forwarded to origin/feat_wt/x") > strings.Index(human, "✓ rebased 2 commits") {
		t.Fatalf("the fast-forward is not before the rebase:\n%s", human)
	}
	if gitOut(t, x, "show", "HEAD:t.txt") != "t" || !gitAncestor(t, f.main, "origin/main", "feat_wt/x") {
		t.Fatal("the rebased branch lacks the remote's commit, or is not on trunk")
	}
	// What is on the remote now is the old version of what the branch has.
	if got := planOfWork(t, f.ctx, "x").Worktree.OwnRemote; got.State != "rebased" || got.Blocks != nil || got.Fetched {
		t.Fatalf("after the run: %+v", got)
	}
	var out bytes.Buffer
	if err := SyncUndo(f.ctx, "x", UndoOptions{verbOptions: opts.verbOptions}, &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatalf("undo left the branch at %s, want %s as before the fast-forward\n%s", got, before, out.String())
	}
}

// The state right after wt up --no-push is ahead and behind at once, and a
// second wt up on it has to run.
func TestUpRunsAgainOnABranchRebasedAndNotPushed(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	f.advanceOrigin(t, 1)
	if r, human, err := upOut(t, f.ctx, "x", opts); err != nil || r.Worktrees[0].Result != ResultRebased {
		t.Fatalf("first up: %v\n%s", err, human)
	}
	if gitOut(t, x, "rev-parse", "HEAD") == gitOut(t, f.main, "rev-parse", "origin/feat_wt/x") {
		t.Fatal("the branch was pushed under --no-push")
	}
	f.advanceOrigin(t, 1)
	r, human, err := upOut(t, f.ctx, "x", at(opts, 100))
	if err != nil {
		t.Fatalf("second up: %v\n%s", err, human)
	}
	o := r.Worktrees[0].OwnRemoteSync
	if r.Worktrees[0].Result != ResultRebased || o.State != "rebased" || o.FastForwarded || o.SkippedReason != nil {
		t.Fatalf("%+v %+v", r.Worktrees[0], o)
	}
}

// The same after a rebase a strategy resolved: the replayed commit is not
// the patch that was pushed, so only the tips the branch once had say it was
// rebased.
func TestSyncRunRunsAgainAfterAStrategyResolvedRebase(t *testing.T) {
	ctx, bump := runFixture(t, false)
	bareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	gitIn(t, main, "push", "-q", "origin", "main")
	gitIn(t, bump, "push", "-q", "-u", "origin", "feat_wt/bump")
	opts := noAgents()
	opts.NoFetch, opts.Push = false, PushNever
	if r, err := syncRunJSON(t, ctx, []string{"bump"}, opts); err != nil || r.Worktrees[0].Result != ResultRebased {
		t.Fatalf("first run: %v %+v", err, r)
	}
	if unmatched := gitOut(t, main, "rev-list", "--right-only", "--cherry-pick", "feat_wt/bump...origin/feat_wt/bump"); unmatched == "" {
		t.Fatal("the fixture's rebase replayed the pushed patch unchanged: it proves nothing about former tips")
	}
	writeIn(t, main, "a.txt", "a2\n")
	gitIn(t, main, "commit", "-q", "-am", "trunk moves")
	gitIn(t, main, "push", "-q", "origin", "main")
	r, err := syncRunJSON(t, ctx, []string{"bump"}, at(opts, 100))
	if err != nil {
		t.Fatalf("second run: %v %+v", err, r)
	}
	if o := r.Worktrees[0].OwnRemoteSync; r.Worktrees[0].Result != ResultRebased || o == nil || o.State != "rebased" {
		t.Fatalf("%+v %+v", r.Worktrees[0], o)
	}
}

// Diverged for real refuses with nothing touched; --allow-diverged rebases
// the branch as it stands.
func TestUpRefusesABranchThatDivergedFromItsRemote(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	theirs := f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := mine(t, x, "m.txt", "m\n")
	f.advanceOrigin(t, 1)

	r, human, err := upOut(t, f.ctx, "x", opts)
	if err == nil {
		t.Fatalf("a diverged branch was rebased:\n%s", human)
	}
	w := r.Worktrees[0]
	o := w.OwnRemoteSync
	if w.Result != ResultRefused || r.Outcome != OutcomeRefused || !strings.Contains(str(w.Reason), "origin/feat_wt/x has 1 commit this branch never had") {
		t.Fatalf("participant %+v", w)
	}
	if o.State != "diverged" || str(o.Remote) != theirs || *o.LocalAhead != 1 || *o.RemoteAhead != 1 ||
		o.FastForwarded || o.Allowed || str(o.SkippedReason) != wtsync.OwnBlockDiverged {
		t.Fatalf("ownRemoteSync %+v", o)
	}
	if !strings.Contains(human, "✗ refused: x: origin/feat_wt/x has 1 commit this branch never had") || !strings.Contains(human, "--allow-diverged") {
		t.Fatalf("the refusal does not say why or what goes on:\n%s", human)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatalf("the branch moved to %s", got)
	}
	if refs := safetyRefs(t, f.main); refs != "" {
		t.Fatalf("a refused run left refs:\n%s", refs)
	}

	opts.AllowDiverged = true
	r, human, err = upOut(t, f.ctx, "x", opts)
	if err != nil {
		t.Fatalf("--allow-diverged: %v\n%s", err, human)
	}
	w, o = r.Worktrees[0], r.Worktrees[0].OwnRemoteSync
	if w.Result != ResultRebased || o.State != "diverged" || !o.Allowed || o.SkippedReason != nil || o.FastForwarded {
		t.Fatalf("%+v %+v", w, o)
	}
	if !strings.Contains(human, "⚠ --allow-diverged: origin/feat_wt/x has 1 commit this branch never had; rebasing it as it stands") {
		t.Fatalf("nothing said about going on:\n%s", human)
	}
	if gitOut(t, x, "ls-tree", "--name-only", "HEAD", "t.txt") != "" {
		t.Fatal("the remote's commit was pulled in; --allow-diverged rebases the branch as it stands")
	}
}

// The opposite of rebased-and-not-pushed: a branch that was rebased, whose
// remote then gained a commit the branch never had.
func TestUpRefusesARebasedBranchWhoseRemoteGainedACommit(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	f.advanceOrigin(t, 1)
	if _, human, err := upOut(t, f.ctx, "x", opts); err != nil {
		t.Fatalf("first up: %v\n%s", err, human)
	}
	rebased := gitOut(t, x, "rev-parse", "HEAD")
	f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.advanceOrigin(t, 1)
	r, human, err := upOut(t, f.ctx, "x", at(opts, 100))
	if err == nil {
		t.Fatalf("rebased over a commit it never had:\n%s", human)
	}
	if o := r.Worktrees[0].OwnRemoteSync; o.State != "diverged" || str(o.SkippedReason) != wtsync.OwnBlockDiverged {
		t.Fatalf("%+v", o)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != rebased {
		t.Fatalf("the branch moved to %s", got)
	}
}

// Behind, in a checkout a fast-forward is not safe in: nothing is touched,
// and the plan names it before anybody runs anything.
func TestUpBehindAndNotFastForwardableTouchesNothing(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	before := gitOut(t, x, "rev-parse", "HEAD")
	f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.advanceOrigin(t, 1) // fetches everything, so the plan sees the remote
	writeIn(t, x, "x.txt", "edited\n")

	p := planOfWork(t, f.ctx, "x")
	if p.UpEligible || str(p.UpIneligibleCode) != IneligibleOwnBehind || p.Token != nil {
		t.Fatalf("plan: eligible %v, code %s, token %s", p.UpEligible, str(p.UpIneligibleCode), str(p.Token))
	}
	if want := "feat_wt/x is 1 commit behind origin/feat_wt/x and cannot be fast-forwarded to it: tracked changes in the worktree."; str(p.UpIneligibleReason) != want {
		t.Fatalf("reason %q, want %q", str(p.UpIneligibleReason), want)
	}
	if o := p.Worktree.OwnRemote; o.State != "behind" || str(o.Blocks) != wtsync.OwnBlockDirty || *o.Behind != 1 || o.Fetched {
		t.Fatalf("ownRemote %+v", o)
	}

	r, human, err := upOut(t, f.ctx, "x", opts)
	if err == nil {
		t.Fatalf("a dirty worktree was rebased:\n%s", human)
	}
	if o := r.Worktrees[0].OwnRemoteSync; r.Worktrees[0].Result != ResultRefused || o.State != "behind" ||
		str(o.SkippedReason) != wtsync.OwnBlockDirty || o.FastForwarded {
		t.Fatalf("%+v %+v", r.Worktrees[0], o)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before || safetyRefs(t, f.main) != "" {
		t.Fatalf("something moved: HEAD %s, refs %q", got, safetyRefs(t, f.main))
	}
	// A busy session blocks it too, and --force, which takes the run past
	// the session, takes the fast-forward past it as well.
	gitIn(t, x, "checkout", "-q", "--", "x.txt")
	busy := []wtsync.Agent{{Name: "busy-1", Cwd: x, Status: "busy"}}
	opts.Agents, opts.Relist = busy, func() ([]wtsync.Agent, error) { return busy, nil }
	r, human, err = upOut(t, f.ctx, "x", opts)
	if err == nil || str(r.Worktrees[0].OwnRemoteSync.SkippedReason) != wtsync.OwnBlockSession {
		t.Fatalf("a busy session: %v %+v\n%s", err, r.Worktrees[0].OwnRemoteSync, human)
	}
	opts.Force = true
	r, human, err = upOut(t, f.ctx, "x", opts)
	if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded || r.Worktrees[0].Result != ResultRebased {
		t.Fatalf("--force: %v %+v\n%s", err, r.Worktrees[0], human)
	}
}

// legacyPlanToken is planToken as wt computed it before it looked at any
// remote: a caller holding a token from then must still be let through.
func legacyPlanToken(ctx *Context, trunk string, stack []string) string {
	h := sha256.New()
	h.Write([]byte("wt-plan-1\x00" + trunk + "\x00"))
	h.Write(configFingerprint(ctx))
	h.Write([]byte("\x00" + strings.Join(stack, "\x00")))
	return "1:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// The plan reports the worktree and each stack member against its own
// remote. A plan held back by divergence alone still has a token, which
// covers the remote commit the caller was shown: wt up --allow-diverged
// --expect goes ahead over that divergence and no other.
func TestPlanHoldsADivergedPlanToItsToken(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	if p := planOfWork(t, f.ctx, "x"); !p.UpEligible || str(p.Token) != legacyPlanToken(f.ctx, "main", []string{"feat_wt/x"}) ||
		p.Worktree.OwnRemote.State != "inSync" || p.Stack[0].OwnRemote.State != "inSync" {
		t.Fatalf("in sync: eligible %v token %s ownRemote %+v", p.UpEligible, str(p.Token), p.Worktree.OwnRemote)
	}
	theirs := f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := mine(t, x, "m.txt", "m\n")
	f.advanceOrigin(t, 1)

	p := planOfWork(t, f.ctx, "x")
	if p.UpEligible || str(p.UpIneligibleCode) != IneligibleOwnDiverged || p.Token == nil {
		t.Fatalf("plan: eligible %v, code %s, token %s", p.UpEligible, str(p.UpIneligibleCode), str(p.Token))
	}
	if *p.Token == legacyPlanToken(f.ctx, "main", []string{"feat_wt/x"}) {
		t.Fatal("the token does not cover the divergence")
	}
	want := "feat_wt/x has diverged from origin/feat_wt/x: it has 1 commit origin/feat_wt/x lacks, and origin/feat_wt/x has 1 commit it never had. " +
		"Pull it in, or rebase it as it stands with --allow-diverged."
	if str(p.UpIneligibleReason) != want {
		t.Fatalf("reason %q, want %q", str(p.UpIneligibleReason), want)
	}
	for _, o := range []OwnRemote{p.Worktree.OwnRemote, p.Stack[0].OwnRemote} {
		if o.State != "diverged" || str(o.Ref) != "origin/feat_wt/x" || str(o.Commit) != theirs || *o.Ahead != 1 || *o.Behind != 1 ||
			o.Fetched || str(o.Blocks) != wtsync.OwnBlockDiverged {
			t.Fatalf("ownRemote %+v", o)
		}
	}

	// The remote moves again: what the caller allowed is not what is there.
	f.theirs(t, "feat_wt/x", "t2.txt", "t2\n")
	opts.Expect, opts.AllowDiverged = *p.Token, true
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err == nil || len(r.Worktrees) != 0 || !strings.Contains(str(r.Error), "the plan changed since it was read") {
		t.Fatalf("a remote that moved since the look: %v %+v\n%s", err, r, human)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before || safetyRefs(t, f.main) != "" {
		t.Fatal("a refused run touched something")
	}
	// Read again, and the run goes over what was shown.
	opts.Expect = *planOfWork(t, f.ctx, "x").Token
	if r, human, err = upOut(t, f.ctx, "x", opts); err != nil || r.Worktrees[0].Result != ResultRebased || !r.Worktrees[0].OwnRemoteSync.Allowed {
		t.Fatalf("the plan as read: %v %+v\n%s", err, r, human)
	}
}

// A remote that goes from in sync to behind between the look and the run
// does not bounce the caller: wt fast-forwards, and the token has no remote
// commit in it.
func TestUpExpectIsNotBouncedByARemoteThatMovedAhead(t *testing.T) {
	f := newTrunkFixture(t)
	_, opts := f.pushedWork(t)
	f.advanceOrigin(t, 1)
	opts.Expect = *planOfWork(t, f.ctx, "x").Token
	f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
}

// One refusal refuses the whole stack, so the plan is held back by any
// member's own remote, and names the member. A member that cannot be
// fast-forwarded comes before one that diverged: --allow-diverged does not
// clear it, so there is no token.
func TestPlanIsHeldBackByAnyStackMembersOwnRemote(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	var buf bytes.Buffer
	y, err := New(f.ctx, "feat/y", NewOptions{NoSetup: true, Base: "feat_wt/x"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	mine(t, y, "y.txt", "y\n")
	gitIn(t, y, "push", "-q", "-u", "origin", "feat_wt/y")
	f.theirs(t, "feat_wt/y", "t.txt", "t\n")
	mine(t, y, "y2.txt", "y2\n")
	f.advanceOrigin(t, 1)

	p := planOfWork(t, f.ctx, "x")
	if str(p.UpIneligibleCode) != IneligibleOwnDiverged || p.Token == nil {
		t.Fatalf("code %s token %s", str(p.UpIneligibleCode), str(p.Token))
	}
	if want := "feat_wt/y (y), a worktree in this stack, has diverged from origin/feat_wt/y"; !strings.HasPrefix(str(p.UpIneligibleReason), want) {
		t.Fatalf("reason %q", str(p.UpIneligibleReason))
	}
	if p.Worktree.OwnRemote.State != "inSync" || p.Stack[1].Branch != "feat_wt/y" || str(p.Stack[1].OwnRemote.Blocks) != wtsync.OwnBlockDiverged {
		t.Fatalf("worktree %+v stack %+v", p.Worktree.OwnRemote, p.Stack)
	}
	// The run refuses both, the diverged member for itself and the other
	// because of it.
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err == nil || len(r.Worktrees) != 2 || r.Worktrees[0].Result != ResultNotRun || r.Worktrees[1].Result != ResultRefused {
		t.Fatalf("%v %+v\n%s", err, r.Worktrees, human)
	}
	if r.Worktrees[0].OwnRemoteSync.SkippedReason != nil || str(r.Worktrees[1].OwnRemoteSync.SkippedReason) != wtsync.OwnBlockDiverged {
		t.Fatalf("skipped reasons %+v %+v", r.Worktrees[0].OwnRemoteSync, r.Worktrees[1].OwnRemoteSync)
	}

	// x falls behind as well, with tracked changes in it.
	f.theirs(t, "feat_wt/x", "tx.txt", "tx\n")
	gitIn(t, f.main, "fetch", "-q", "origin")
	writeIn(t, x, "x.txt", "edited\n")
	p = planOfWork(t, f.ctx, "y")
	if str(p.UpIneligibleCode) != IneligibleOwnBehind || p.Token != nil {
		t.Fatalf("both kinds: code %s token %s", str(p.UpIneligibleCode), str(p.Token))
	}
	if want := "feat_wt/x (x), a worktree in this stack, is 1 commit behind origin/feat_wt/x"; !strings.HasPrefix(str(p.UpIneligibleReason), want) {
		t.Fatalf("reason %q", str(p.UpIneligibleReason))
	}
}

// A stack whose parent is behind its remote: the parent is fast-forwarded
// and rebased, and the child lands on the parent's final tip with its own
// commits and nothing else.
func TestUpFastForwardsAStackParent(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	var buf bytes.Buffer
	y, err := New(f.ctx, "feat/y", NewOptions{NoSetup: true, Base: "feat_wt/x"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	mine(t, y, "y.txt", "y\n")
	f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.advanceOrigin(t, 1)
	r, human, err := upOut(t, f.ctx, "y", opts)
	if err != nil || len(r.Worktrees) != 2 {
		t.Fatalf("%v %+v\n%s", err, r.Worktrees, human)
	}
	if px := r.Worktrees[0]; px.Branch != "feat_wt/x" || px.Result != ResultRebased || !px.OwnRemoteSync.FastForwarded {
		t.Fatalf("parent %+v %+v", px, px.OwnRemoteSync)
	}
	if py := r.Worktrees[1]; py.Result != ResultRebased || py.OwnRemoteSync.State != "none" || py.OwnRemoteSync.FastForwarded {
		t.Fatalf("child %+v %+v", py, py.OwnRemoteSync)
	}
	if gitOut(t, y, "rev-parse", "HEAD~1") != gitOut(t, x, "rev-parse", "HEAD") {
		t.Fatalf("the child is not one commit on the parent's final tip:\n%s", human)
	}
	if gitOut(t, y, "show", "HEAD:t.txt") != "t" {
		t.Fatal("the child lacks what the parent's fast-forward brought")
	}
}

// A branch cut from trunk that tracks trunk has no ref of its own on origin:
// nothing to check, on every run, however far its upstream is ahead. It is
// never fast-forwarded onto trunk and never refused.
func TestUpLeavesABranchThatTracksTrunkUnchecked(t *testing.T) {
	f := newTrunkFixture(t)
	opts := f.withWork(t)
	wt, err := Locate(f.ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt.Path, "branch", "-q", "--set-upstream-to=origin/main")
	for i, nanos := range []int64{99, 100} {
		f.advanceOrigin(t, 2)
		if o := planOfWork(t, f.ctx, "x").Worktree.OwnRemote; o.State != "none" || o.Ref != nil || o.Blocks != nil {
			t.Fatalf("run %d plan: %+v", i+1, o)
		}
		r, human, err := upOut(t, f.ctx, "x", at(opts, nanos))
		if err != nil {
			t.Fatalf("run %d: %v\n%s", i+1, err, human)
		}
		if o := r.Worktrees[0].OwnRemoteSync; r.Worktrees[0].Result != ResultRebased || o.State != "none" || o.FastForwarded || o.Ref != nil {
			t.Fatalf("run %d: %+v %+v", i+1, r.Worktrees[0], o)
		}
		if strings.Contains(human, "fast-forwarded to origin/main") && !strings.Contains(human, "local main") {
			t.Fatalf("run %d fast-forwarded the branch onto trunk:\n%s", i+1, human)
		}
		if n := gitOut(t, wt.Path, "rev-list", "--count", "origin/main..HEAD"); n != "1" {
			t.Fatalf("run %d: %s commits on top of trunk, want its own one", i+1, n)
		}
	}
}

// A ref that went away on the remote, and a remote where a push lands
// somewhere a tracking ref cannot show: the run goes on, and says so for the
// one it could not check.
func TestUpGoesOnWhenTheRemoteIsGoneOrCannotBeRead(t *testing.T) {
	t.Run("gone", func(t *testing.T) {
		f := newTrunkFixture(t)
		_, opts := f.pushedWork(t)
		gitIn(t, f.origin, "update-ref", "-d", "refs/heads/feat_wt/x")
		f.advanceOrigin(t, 1)
		r, human, err := upOut(t, f.ctx, "x", opts)
		if err != nil {
			t.Fatalf("%v\n%s", err, human)
		}
		if o := r.Worktrees[0].OwnRemoteSync; r.Worktrees[0].Result != ResultRebased || o.State != "gone" || str(o.Ref) != "origin/feat_wt/x" || o.Remote != nil || o.RemoteAhead != nil {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		f := newTrunkFixture(t)
		_, opts := f.pushedWork(t)
		f.theirs(t, "feat_wt/x", "t.txt", "t\n")
		f.advanceOrigin(t, 1)
		gitIn(t, f.main, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "elsewhere.git"))
		r, human, err := upOut(t, f.ctx, "x", opts)
		if err != nil {
			t.Fatalf("%v\n%s", err, human)
		}
		if o := r.Worktrees[0].OwnRemoteSync; r.Worktrees[0].Result != ResultRebased || o.State != "unknown" || o.Ref != nil || o.Remote != nil || o.FastForwarded {
			t.Fatalf("%+v", o)
		}
		if !strings.Contains(human, "⚠ not checked against its remote: origin pushes to another URL than it fetches from\n") {
			t.Fatalf("nothing said:\n%s", human)
		}
	})
}

// A branch whose own remote cannot be fetched is refused by itself, with a
// code of its own and trunk not blamed; --no-fetch compares with what was
// last fetched and says so.
func TestUpRefusesWhenTheBranchsRemoteCannotBeFetched(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	before := gitOut(t, x, "rev-parse", "HEAD")
	theirs := f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.advanceOrigin(t, 1)
	gitIn(t, f.main, "remote", "add", "fork", filepath.Join(t.TempDir(), "no-such.git"))
	gitIn(t, f.main, "config", "branch.feat_wt/x.pushRemote", "fork")
	gitIn(t, f.main, "config", "push.default", "current")
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err == nil || r.Error != nil || !r.Fetched || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	w := r.Worktrees[0]
	if w.Result != ResultRefused || str(w.OwnRemoteSync.SkippedReason) != OwnSkipFetchFailed || w.OwnRemoteSync.FastForwarded ||
		!strings.Contains(str(w.Reason), "its own remote could not be fetched (") {
		t.Fatalf("%+v %+v\n%s", w, w.OwnRemoteSync, human)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before || safetyRefs(t, f.main) != "" {
		t.Fatal("a refused run touched something")
	}
	// Back on origin, without fetching: behind as last fetched, and said so.
	gitIn(t, f.main, "config", "--unset", "branch.feat_wt/x.pushRemote")
	opts.NoFetch = true
	r, human, err = upOut(t, f.ctx, "x", opts)
	if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded {
		t.Fatalf("--no-fetch: %v %+v\n%s", err, r, human)
	}
	if !strings.Contains(human, "✓ fast-forwarded to origin/feat_wt/x  "+before[:7]+" → "+theirs[:7]+" (1 commit) (as last fetched)\n") {
		t.Fatalf("not said to be as last fetched:\n%s", human)
	}
}

// The run's own fetch brings the branch's remote: nobody fetched it before.
func TestUpFetchesTheBranchsOwnRemoteWithTrunk(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	stale := gitOut(t, f.main, "rev-parse", "origin/feat_wt/x")
	theirs := f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	gitIn(t, f.other, "checkout", "-q", "main")
	gitIn(t, f.other, "commit", "-q", "--allow-empty", "-m", "origin moves")
	gitIn(t, f.other, "push", "-q", "origin", "main")
	if gitOut(t, f.main, "rev-parse", "origin/feat_wt/x") != stale {
		t.Fatal("the fixture fetched")
	}
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded || str(r.Worktrees[0].OwnRemoteSync.Remote) != theirs {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	if gitOut(t, x, "show", "HEAD:t.txt") != "t" {
		t.Fatal("the remote's commit is not in the rebased branch")
	}
}

// wt up promises a rebase that goes through without anybody: a branch that
// is behind its remote is judged at the remote's commit, the one the run
// would rebase, so a conflict that commit brings refuses before anything
// moves. wt sync run takes the same branch, fast-forwards it, hands the
// conflict over, and undo puts the branch back where it stood before both.
func TestABranchBehindItsRemoteIsJudgedAtTheRemotesCommit(t *testing.T) {
	f := newTrunkFixture(t)
	f.declare(t)
	gitIn(t, f.main, "fetch", "-q", "origin")
	x, opts := f.pushedWork(t)
	before := gitOut(t, x, "rev-parse", "HEAD")
	theirs := f.theirs(t, "feat_wt/x", "c.txt", "theirs\n")
	f.theirs(t, "main", "c.txt", "trunk\n")

	r, human, err := upOut(t, f.ctx, "x", opts)
	if err == nil || r.Worktrees[0].Result != ResultRefused || !strings.Contains(str(r.Worktrees[0].Reason), "would not sync cleanly") {
		t.Fatalf("wt up: %v %+v\n%s", err, r.Worktrees, human)
	}
	if o := r.Worktrees[0].OwnRemoteSync; o.State != "behind" || o.FastForwarded || o.SkippedReason != nil {
		t.Fatalf("ownRemoteSync %+v", o)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before || safetyRefs(t, f.main) != "" {
		t.Fatal("a refused wt up touched something")
	}

	run, err := syncRunJSON(t, f.ctx, []string{"x"}, at(opts, 100))
	w := run.Worktrees[0]
	if err == nil || w.Result != ResultHandedOver || !w.OwnRemoteSync.FastForwarded || str(w.OwnRemoteSync.Local) != before {
		t.Fatalf("wt sync run: %v %+v %+v", err, w, w.OwnRemoteSync)
	}
	// The fast-forward stands under the rebase in progress.
	if got := gitOut(t, f.main, "rev-parse", "refs/heads/feat_wt/x"); got != theirs || str(w.After) != theirs {
		t.Fatalf("the branch is at %s, after %s, want the remote's %s", got, str(w.After), theirs)
	}
	// The handover says which tip the safety ref pins, so a resume neither
	// refuses it nor forgets where undo goes.
	writeIn(t, x, "c.txt", "both\n")
	gitIn(t, x, "add", "c.txt")
	resumed, err := syncResumeJSON(t, f.ctx, "x", ResumeOptions{pushOptions: pushOptions{Push: PushNever}, verbOptions: opts.verbOptions})
	if err != nil || resumed.Worktrees[0].Result != ResultRebased || resumed.Worktrees[0].OwnRemoteSync != nil {
		t.Fatalf("resume: %v %+v", err, resumed.Worktrees[0])
	}
	if gitOut(t, x, "show", "HEAD:c.txt") != "both" || !gitAncestor(t, f.main, "origin/main", "feat_wt/x") {
		t.Fatal("the resumed rebase did not finish on trunk with the resolution")
	}
	undo, err := syncUndoJSON(t, f.ctx, "x", UndoOptions{verbOptions: opts.verbOptions})
	if err != nil || undo.Worktrees[0].Result != ResultUndone || str(undo.Worktrees[0].After) != before || undo.Worktrees[0].OwnRemoteSync != nil {
		t.Fatalf("undo: %v %+v", err, undo.Worktrees[0])
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatalf("undo left the branch at %s, want %s", got, before)
	}
	if busy, _ := wtsync.RebaseInProgress(x); busy {
		t.Fatal("still mid-rebase after undo")
	}
}

// A rebase that is put back after the fast-forward takes the branch back to
// the tip the run found: fastForwarded stays as a record, after is local,
// and nothing is left for undo to trip over.
func TestSyncRunRestoresAStackParentToBeforeItsFastForward(t *testing.T) {
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
	before := gitOut(t, x, "rev-parse", "HEAD")
	f.theirs(t, "feat_wt/x", "c.txt", "theirs\n")
	f.theirs(t, "main", "c.txt", "trunk\n")

	run, err := syncRunJSON(t, f.ctx, []string{"x"}, opts)
	if err == nil || len(run.Worktrees) != 2 {
		t.Fatalf("%v %+v", err, run.Worktrees)
	}
	px := run.Worktrees[0]
	if px.Result != ResultRestored || !px.OwnRemoteSync.FastForwarded || str(px.After) != before || str(px.OwnRemoteSync.Local) != before {
		t.Fatalf("parent %+v %+v", px, px.OwnRemoteSync)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatalf("restored to %s, want %s", got, before)
	}
	if run.Worktrees[1].Result != ResultNotRun {
		t.Fatalf("child %+v", run.Worktrees[1])
	}
	if refs := gitOut(t, f.main, "for-each-ref", "--format=%(refname)", "refs/wt-sync-result/"); refs != "" {
		t.Fatalf("a result ref outlived the restore:\n%s", refs)
	}
	var out bytes.Buffer
	if err := SyncUndo(f.ctx, "x", UndoOptions{verbOptions: opts.verbOptions}, &out); err != nil || !strings.Contains(out.String(), "already at") {
		t.Fatalf("undo after a restore: %v\n%s", err, out.String())
	}
}

// The overview carries the same object on each worktree, fetched with trunk,
// and refuses the diverged one in words; its token lets a run under
// --allow-diverged go ahead over exactly the divergence it showed.
func TestSyncOverviewReportsOwnRemoteAndItsTokenCoversADivergence(t *testing.T) {
	f := newTrunkFixture(t)
	f.declare(t)
	gitIn(t, f.main, "fetch", "-q", "origin")
	x, opts := f.pushedWork(t)
	overview := func(noFetch bool) SyncOverview {
		t.Helper()
		var buf bytes.Buffer
		if err := SyncJSON(f.ctx, SyncOptions{NoFetch: noFetch}, &buf); err != nil {
			t.Fatalf("%v\n%s", err, buf.String())
		}
		validateJSON(t, "sync", buf.Bytes())
		var o SyncOverview
		if err := json.Unmarshal(buf.Bytes(), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	f.advanceOrigin(t, 1)
	plain := overview(true)
	if row := overviewRow(t, plain, "x"); row.OwnRemote.State != "inSync" || row.OwnRemote.Fetched || !row.Runnable {
		t.Fatalf("in sync, not fetched: %+v", row)
	}

	theirs := f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := mine(t, x, "m.txt", "m\n")
	o := overview(false)
	row := overviewRow(t, o, "x")
	if row.OwnRemote.State != "diverged" || !row.OwnRemote.Fetched || str(row.OwnRemote.Commit) != theirs || str(row.OwnRemote.Blocks) != wtsync.OwnBlockDiverged {
		t.Fatalf("ownRemote %+v", row.OwnRemote)
	}
	if row.Verdict != VerdictRefuse || row.Runnable || row.Group != GroupNeedsYou || row.Class != "conflict-free" ||
		!strings.Contains(str(row.Reason), "origin/feat_wt/x has 1 commit this branch never had") {
		t.Fatalf("row %+v", row)
	}
	if o.Token == nil || *o.Token == *plain.Token {
		t.Fatalf("token %s does not cover the divergence (in sync it was %s)", str(o.Token), str(plain.Token))
	}

	// The command gittree's sheet runs.
	opts.IfReady, opts.Expect = true, *o.Token
	if r, err := syncRunJSON(t, f.ctx, []string{"x"}, opts); err == nil || r.Worktrees[0].Result != ResultRefused {
		t.Fatalf("without --allow-diverged: %v %+v", err, r.Worktrees)
	}
	opts.AllowDiverged = true
	f.theirs(t, "feat_wt/x", "t2.txt", "t2\n")
	r, err := syncRunJSON(t, f.ctx, []string{"x"}, opts)
	if err == nil || len(r.Worktrees) != 0 || !strings.Contains(str(r.Error), "the overview changed since it was read") {
		t.Fatalf("a remote that moved since the look: %v %+v", err, r)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("a refused run moved the branch")
	}
	opts.Expect = *overview(false).Token
	r, err = syncRunJSON(t, f.ctx, []string{"x"}, opts)
	if err != nil || r.Worktrees[0].Result != ResultRebased || !r.Worktrees[0].OwnRemoteSync.Allowed {
		t.Fatalf("the overview as read: %v %+v", err, r)
	}
}

// A run with nothing named leaves a diverged worktree alone and names what
// holds it; --if-ready makes that a failure.
func TestSyncRunWithNothingNamedLeavesADivergedWorktree(t *testing.T) {
	f := newTrunkFixture(t)
	f.declare(t)
	gitIn(t, f.main, "fetch", "-q", "origin")
	x, opts := f.pushedWork(t)
	f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := mine(t, x, "m.txt", "m\n")
	f.advanceOrigin(t, 1)

	var out bytes.Buffer
	if err := SyncRun(f.ctx, nil, opts, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "nothing is ready to rebase") || !strings.Contains(out.String(), "left as they are: x conflict-free, diverged from origin/feat_wt/x") {
		t.Fatalf("out:\n%s", out.String())
	}
	opts.IfReady = true
	out.Reset()
	if err := SyncRun(f.ctx, nil, opts, &out); err == nil || !strings.Contains(err.Error(), "x (not ready)") {
		t.Fatalf("--if-ready: %v\n%s", err, out.String())
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("the branch moved")
	}
	// --allow-diverged takes it, named.
	opts.AllowDiverged = true
	out.Reset()
	if err := SyncRun(f.ctx, []string{"x"}, opts, &out); err != nil {
		t.Fatalf("--allow-diverged: %v\n%s", err, out.String())
	}
	if !gitAncestor(t, f.main, "origin/main", "feat_wt/x") {
		t.Fatal("not rebased")
	}
}

// The overview a person reads: one line under the row for what a run would
// refuse or do first, and the detail of one worktree says how it stands.
func TestSyncSaysHowAWorktreeStandsAgainstItsRemote(t *testing.T) {
	f := newTrunkFixture(t)
	f.declare(t)
	gitIn(t, f.main, "fetch", "-q", "origin")
	x, _ := f.pushedWork(t)
	f.theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.advanceOrigin(t, 1)
	var out bytes.Buffer
	if err := Sync(f.ctx, SyncOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\n    1 behind origin/feat_wt/x; a run fast-forwards it first\n") {
		t.Fatalf("behind:\n%s", out.String())
	}
	mine(t, x, "m.txt", "m\n")
	out.Reset()
	if err := Sync(f.ctx, SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "needs you") || !strings.Contains(s, "diverged from origin/feat_wt/x\n") ||
		!strings.Contains(s, "\n    origin/feat_wt/x has 1 commit this branch never had: pull it in, or --allow-diverged rebases it as it stands\n") {
		t.Fatalf("diverged:\n%s", s)
	}
	out.Reset()
	if err := SyncWorktree(f.ctx, "x", SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "  remote  diverged from origin/feat_wt/x: 1 of its own, and 1 there it never had (as last fetched)\n") ||
		!strings.Contains(out.String(), "  run     refused: origin/feat_wt/x has 1 commit this branch never had") {
		t.Fatalf("detail:\n%s", out.String())
	}
	out.Reset()
	if err := StatusWorktree(f.ctx, "x", StatusOptions{NoPR: true, Agents: []wtsync.Agent{}}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "  remote") || !strings.Contains(out.String(), "diverged from origin/feat_wt/x") {
		t.Fatalf("status:\n%s", out.String())
	}
}

// Rebased on another machine and pushed, with this checkout left on the old
// versions: the remote's tip was never this branch's, and what only the
// remote has includes trunk's commits, so it is diverged and refused like
// any other. The sentence says what it looks like.
func TestUpRefusesABranchRebasedElsewhereAndSaysSo(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	before := gitOut(t, x, "rev-parse", "HEAD")
	f.advanceOrigin(t, 1)
	gitIn(t, f.other, "fetch", "-q", "origin")
	gitIn(t, f.other, "checkout", "-q", "-B", "feat_wt/x", "origin/feat_wt/x")
	gitIn(t, f.other, "rebase", "-q", "origin/main")
	gitIn(t, f.other, "push", "-q", "--force", "origin", "feat_wt/x")
	gitIn(t, f.other, "checkout", "-q", "-B", "main", "origin/main")

	r, human, err := upOut(t, f.ctx, "x", opts)
	if err == nil {
		t.Fatalf("rebased over the remote's rebase:\n%s", human)
	}
	w := r.Worktrees[0]
	want := "x: origin/feat_wt/x has every commit of this branch in another form, on 2 commits this branch never had: " +
		"it looks rebased elsewhere and pushed. git reset --hard origin/feat_wt/x takes the remote's, or --allow-diverged rebases this copy as it stands"
	if w.Result != ResultRefused || str(w.Reason) != want || w.OwnRemoteSync.State != "diverged" || str(w.OwnRemoteSync.SkippedReason) != wtsync.OwnBlockDiverged {
		t.Fatalf("%+v %+v\n%s", w, w.OwnRemoteSync, human)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before || safetyRefs(t, f.main) != "" {
		t.Fatal("a refused run touched something")
	}
	p := planOfWork(t, f.ctx, "x")
	wantPlan := "feat_wt/x has diverged from origin/feat_wt/x: every commit it has (1) is there in another form, on 2 commits it never had. " +
		"It looks rebased elsewhere and pushed. Take the remote's with git reset --hard origin/feat_wt/x, or rebase this copy as it stands with --allow-diverged."
	if str(p.UpIneligibleCode) != IneligibleOwnDiverged || str(p.UpIneligibleReason) != wantPlan || p.Token == nil {
		t.Fatalf("plan: %s %q", str(p.UpIneligibleCode), str(p.UpIneligibleReason))
	}
}

// A git that refuses the fast-forward itself touches nothing: the
// participant is refused with the reason failed, and no ref is left.
func TestUpRefusesWhenTheFastForwardItselfFails(t *testing.T) {
	f := newTrunkFixture(t)
	x, opts := f.pushedWork(t)
	before := gitOut(t, x, "rev-parse", "HEAD")
	f.theirs(t, "feat_wt/x", "t.txt", "theirs\n")
	f.advanceOrigin(t, 1)
	// An untracked file the remote's commit would overwrite.
	writeIn(t, x, "t.txt", "scratch\n")
	r, human, err := upOut(t, f.ctx, "x", opts)
	if err == nil {
		t.Fatalf("overwrote an untracked file:\n%s", human)
	}
	w := r.Worktrees[0]
	if w.Result != ResultRefused || str(w.OwnRemoteSync.SkippedReason) != OwnSkipFailed || w.OwnRemoteSync.FastForwarded ||
		str(w.Reason) != "cannot fast-forward to origin/feat_wt/x: untracked files in the way: t.txt" {
		t.Fatalf("%+v %+v\n%s", w, w.OwnRemoteSync, human)
	}
	if got := gitOut(t, x, "rev-parse", "HEAD"); got != before || safetyRefs(t, f.main) != "" {
		t.Fatalf("something moved: HEAD %s, refs %q", got, safetyRefs(t, f.main))
	}
}
