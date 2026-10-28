package synccmd_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The state right after wt up --no-push is ahead and behind at once, and a
// second wt up on it has to run.
func TestUpRunsAgainOnABranchRebasedAndNotPushed(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	f.AdvanceOrigin(t, 1)
	if r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts); err != nil || r.Worktrees[0].Result != synccmd.ResultRebased {
		t.Fatalf("first up: %v\n%s", err, human)
	}
	if testkit.GitOut(t, x, "rev-parse", "HEAD") == testkit.GitOut(t, f.Main, "rev-parse", "origin/feat_wt/x") {
		t.Fatal("the branch was pushed under --no-push")
	}
	f.AdvanceOrigin(t, 1)
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", synccmdtest.At(opts, 100))
	if err != nil {
		t.Fatalf("second up: %v\n%s", err, human)
	}
	o := r.Worktrees[0].OwnRemoteSync
	if r.Worktrees[0].Result != synccmd.ResultRebased || o.State != "rebased" || o.FastForwarded || o.SkippedReason != nil {
		t.Fatalf("%+v %+v", r.Worktrees[0], o)
	}
}

// The same after a rebase a strategy resolved: the replayed commit is not
// the patch that was pushed, so only the tips the branch once had say it was
// rebased.
func TestSyncRunRunsAgainAfterAStrategyResolvedRebase(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	testkit.BareOrigin(t, ctx)
	main := ctx.Repo.MainRoot
	testkit.GitIn(t, main, "push", "-q", "origin", "main")
	testkit.GitIn(t, bump, "push", "-q", "-u", "origin", "feat_wt/bump")
	opts := synccmdtest.NoAgents()
	opts.NoFetch, opts.Push = false, syncrun.PushNever
	if r, err := synccmdtest.SyncRunJSON(t, ctx, []string{"bump"}, opts); err != nil || r.Worktrees[0].Result != synccmd.ResultRebased {
		t.Fatalf("first run: %v %+v", err, r)
	}
	if unmatched := testkit.GitOut(t, main, "rev-list", "--right-only", "--cherry-pick", "feat_wt/bump...origin/feat_wt/bump"); unmatched == "" {
		t.Fatal("the fixture's rebase replayed the pushed patch unchanged: it proves nothing about former tips")
	}
	testkit.WriteIn(t, main, "a.txt", "a2\n")
	testkit.GitIn(t, main, "commit", "-q", "-am", "trunk moves")
	testkit.GitIn(t, main, "push", "-q", "origin", "main")
	r, err := synccmdtest.SyncRunJSON(t, ctx, []string{"bump"}, synccmdtest.At(opts, 100))
	if err != nil {
		t.Fatalf("second run: %v %+v", err, r)
	}
	if o := r.Worktrees[0].OwnRemoteSync; r.Worktrees[0].Result != synccmd.ResultRebased || o == nil || o.State != "rebased" {
		t.Fatalf("%+v %+v", r.Worktrees[0], o)
	}
}

// Diverged for real refuses with nothing touched; --allow-diverged rebases
// the branch as it stands.
func TestUpRefusesABranchThatDivergedFromItsRemote(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	theirs := f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := testkit.Mine(t, x, "m.txt", "m\n")
	f.AdvanceOrigin(t, 1)

	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil {
		t.Fatalf("a diverged branch was rebased:\n%s", human)
	}
	w := r.Worktrees[0]
	o := w.OwnRemoteSync
	if w.Result != synccmd.ResultRefused || r.Outcome != kit.OutcomeRefused || !strings.Contains(testkit.Str(w.Reason), "origin/feat_wt/x has 1 commit this branch never had") {
		t.Fatalf("participant %+v", w)
	}
	if o.State != "diverged" || testkit.Str(o.Remote) != theirs || *o.LocalAhead != 1 || *o.RemoteAhead != 1 ||
		o.FastForwarded || o.Allowed || testkit.Str(o.SkippedReason) != wtsync.OwnBlockDiverged {
		t.Fatalf("ownRemoteSync %+v", o)
	}
	if !strings.Contains(human, "✗ refused: x: origin/feat_wt/x has 1 commit this branch never had") || !strings.Contains(human, "--allow-diverged") {
		t.Fatalf("the refusal does not say why or what goes on:\n%s", human)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatalf("the branch moved to %s", got)
	}
	if refs := testkit.SafetyRefs(t, f.Main); refs != "" {
		t.Fatalf("a refused run left refs:\n%s", refs)
	}

	opts.AllowDiverged = true
	r, human, err = synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err != nil {
		t.Fatalf("--allow-diverged: %v\n%s", err, human)
	}
	w, o = r.Worktrees[0], r.Worktrees[0].OwnRemoteSync
	if w.Result != synccmd.ResultRebased || o.State != "diverged" || !o.Allowed || o.SkippedReason != nil || o.FastForwarded {
		t.Fatalf("%+v %+v", w, o)
	}
	if !strings.Contains(human, "⚠ --allow-diverged: origin/feat_wt/x has 1 commit this branch never had; rebasing it as it stands") {
		t.Fatalf("nothing said about going on:\n%s", human)
	}
	if testkit.GitOut(t, x, "ls-tree", "--name-only", "HEAD", "t.txt") != "" {
		t.Fatal("the remote's commit was pulled in; --allow-diverged rebases the branch as it stands")
	}
}

// The opposite of rebased-and-not-pushed: a branch that was rebased, whose
// remote then gained a commit the branch never had.
func TestUpRefusesARebasedBranchWhoseRemoteGainedACommit(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	f.AdvanceOrigin(t, 1)
	if _, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts); err != nil {
		t.Fatalf("first up: %v\n%s", err, human)
	}
	rebased := testkit.GitOut(t, x, "rev-parse", "HEAD")
	f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.AdvanceOrigin(t, 1)
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", synccmdtest.At(opts, 100))
	if err == nil {
		t.Fatalf("rebased over a commit it never had:\n%s", human)
	}
	if o := r.Worktrees[0].OwnRemoteSync; o.State != "diverged" || testkit.Str(o.SkippedReason) != wtsync.OwnBlockDiverged {
		t.Fatalf("%+v", o)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != rebased {
		t.Fatalf("the branch moved to %s", got)
	}
}

// Behind, in a checkout a fast-forward is not safe in: nothing is touched,
// and the plan names it before anybody runs anything.
func TestUpBehindAndNotFastForwardableTouchesNothing(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.AdvanceOrigin(t, 1) // fetches everything, so the plan sees the remote
	testkit.WriteIn(t, x, "x.txt", "edited\n")

	p := synccmdtest.PlanOfWork(t, f.Ctx, "x")
	if p.UpEligible || testkit.Str(p.UpIneligibleCode) != synccmd.IneligibleOwnBehind || p.Token != nil {
		t.Fatalf("plan: eligible %v, code %s, token %s", p.UpEligible, testkit.Str(p.UpIneligibleCode), testkit.Str(p.Token))
	}
	if want := "feat_wt/x is 1 commit behind origin/feat_wt/x and cannot be fast-forwarded to it: tracked changes in the worktree."; testkit.Str(p.UpIneligibleReason) != want {
		t.Fatalf("reason %q, want %q", testkit.Str(p.UpIneligibleReason), want)
	}
	if o := p.Worktree.OwnRemote; o.State != "behind" || testkit.Str(o.Blocks) != wtsync.OwnBlockDirty || *o.Behind != 1 || o.Fetched {
		t.Fatalf("ownRemote %+v", o)
	}

	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil {
		t.Fatalf("a dirty worktree was rebased:\n%s", human)
	}
	if o := r.Worktrees[0].OwnRemoteSync; r.Worktrees[0].Result != synccmd.ResultRefused || o.State != "behind" ||
		testkit.Str(o.SkippedReason) != wtsync.OwnBlockDirty || o.FastForwarded {
		t.Fatalf("%+v %+v", r.Worktrees[0], o)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before || testkit.SafetyRefs(t, f.Main) != "" {
		t.Fatalf("something moved: HEAD %s, refs %q", got, testkit.SafetyRefs(t, f.Main))
	}
	// A busy session blocks it too, and --force, which takes the run past
	// the session, takes the fast-forward past it as well.
	testkit.GitIn(t, x, "checkout", "-q", "--", "x.txt")
	busy := []agents.Agent{{Name: "busy-1", Cwd: x, Status: "busy"}}
	opts.Agents, opts.Relist = busy, func() ([]agents.Agent, error) { return busy, nil }
	r, human, err = synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil || testkit.Str(r.Worktrees[0].OwnRemoteSync.SkippedReason) != wtsync.OwnBlockSession {
		t.Fatalf("a busy session: %v %+v\n%s", err, r.Worktrees[0].OwnRemoteSync, human)
	}
	opts.Force = true
	r, human, err = synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded || r.Worktrees[0].Result != synccmd.ResultRebased {
		t.Fatalf("--force: %v %+v\n%s", err, r.Worktrees[0], human)
	}
}

// legacyPlanToken is planToken as wt computed it before it looked at any
// remote: a caller holding a token from then must still be let through.
func legacyPlanToken(ctx *kit.Context, trunk string, stack []string) string {
	h := sha256.New()
	h.Write([]byte("wt-plan-1\x00" + trunk + "\x00"))
	h.Write(kit.ConfigFingerprint(ctx))
	h.Write([]byte("\x00" + strings.Join(stack, "\x00")))
	return "1:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// The plan reports the worktree and each stack member against its own
// remote. A plan held back by divergence alone still has a token, which
// covers the remote commit the caller was shown: wt up --allow-diverged
// --expect goes ahead over that divergence and no other.
func TestPlanHoldsADivergedPlanToItsToken(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	if p := synccmdtest.PlanOfWork(t, f.Ctx, "x"); !p.UpEligible || testkit.Str(p.Token) != legacyPlanToken(f.Ctx, "main", []string{"feat_wt/x"}) ||
		p.Worktree.OwnRemote.State != "inSync" || p.Stack[0].OwnRemote.State != "inSync" {
		t.Fatalf("in sync: eligible %v token %s ownRemote %+v", p.UpEligible, testkit.Str(p.Token), p.Worktree.OwnRemote)
	}
	theirs := f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := testkit.Mine(t, x, "m.txt", "m\n")
	f.AdvanceOrigin(t, 1)

	p := synccmdtest.PlanOfWork(t, f.Ctx, "x")
	if p.UpEligible || testkit.Str(p.UpIneligibleCode) != synccmd.IneligibleOwnDiverged || p.Token == nil {
		t.Fatalf("plan: eligible %v, code %s, token %s", p.UpEligible, testkit.Str(p.UpIneligibleCode), testkit.Str(p.Token))
	}
	if *p.Token == legacyPlanToken(f.Ctx, "main", []string{"feat_wt/x"}) {
		t.Fatal("the token does not cover the divergence")
	}
	want := "feat_wt/x has diverged from origin/feat_wt/x: it has 1 commit origin/feat_wt/x lacks, and origin/feat_wt/x has 1 commit it never had. " +
		"Pull it in, or rebase it as it stands with --allow-diverged."
	if testkit.Str(p.UpIneligibleReason) != want {
		t.Fatalf("reason %q, want %q", testkit.Str(p.UpIneligibleReason), want)
	}
	for _, o := range []syncrun.OwnRemote{p.Worktree.OwnRemote, p.Stack[0].OwnRemote} {
		if o.State != "diverged" || testkit.Str(o.Ref) != "origin/feat_wt/x" || testkit.Str(o.Commit) != theirs || *o.Ahead != 1 || *o.Behind != 1 ||
			o.Fetched || testkit.Str(o.Blocks) != wtsync.OwnBlockDiverged {
			t.Fatalf("ownRemote %+v", o)
		}
	}

	// The remote moves again: what the caller allowed is not what is there.
	f.Theirs(t, "feat_wt/x", "t2.txt", "t2\n")
	opts.Expect, opts.AllowDiverged = *p.Token, true
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil || len(r.Worktrees) != 0 || !strings.Contains(testkit.Str(r.Error), "the plan changed since it was read") {
		t.Fatalf("a remote that moved since the look: %v %+v\n%s", err, r, human)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before || testkit.SafetyRefs(t, f.Main) != "" {
		t.Fatal("a refused run touched something")
	}
	// Read again, and the run goes over what was shown.
	opts.Expect = *synccmdtest.PlanOfWork(t, f.Ctx, "x").Token
	if r, human, err = synccmdtest.UpOut(t, f.Ctx, "x", opts); err != nil || r.Worktrees[0].Result != synccmd.ResultRebased || !r.Worktrees[0].OwnRemoteSync.Allowed {
		t.Fatalf("the plan as read: %v %+v\n%s", err, r, human)
	}
}

// A remote that goes from in sync to behind between the look and the run
// does not bounce the caller: wt fast-forwards, and the token has no remote
// commit in it.
func TestUpExpectIsNotBouncedByARemoteThatMovedAhead(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	_, opts := f.PushedWork(t)
	f.AdvanceOrigin(t, 1)
	opts.Expect = *synccmdtest.PlanOfWork(t, f.Ctx, "x").Token
	f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
}

// One refusal refuses the whole stack, so the plan is held back by any
// member's own remote, and names the member. A member that cannot be
// fast-forwarded comes before one that diverged: --allow-diverged does not
// clear it, so there is no token.
func TestPlanIsHeldBackByAnyStackMembersOwnRemote(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	var buf bytes.Buffer
	y, err := provision.New(f.Ctx, "feat/y", provision.NewOptions{NoSetup: true, Base: "feat_wt/x"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	testkit.Mine(t, y, "y.txt", "y\n")
	testkit.GitIn(t, y, "push", "-q", "-u", "origin", "feat_wt/y")
	f.Theirs(t, "feat_wt/y", "t.txt", "t\n")
	testkit.Mine(t, y, "y2.txt", "y2\n")
	f.AdvanceOrigin(t, 1)

	p := synccmdtest.PlanOfWork(t, f.Ctx, "x")
	if testkit.Str(p.UpIneligibleCode) != synccmd.IneligibleOwnDiverged || p.Token == nil {
		t.Fatalf("code %s token %s", testkit.Str(p.UpIneligibleCode), testkit.Str(p.Token))
	}
	if want := "feat_wt/y (y), a worktree in this stack, has diverged from origin/feat_wt/y"; !strings.HasPrefix(testkit.Str(p.UpIneligibleReason), want) {
		t.Fatalf("reason %q", testkit.Str(p.UpIneligibleReason))
	}
	if p.Worktree.OwnRemote.State != "inSync" || p.Stack[1].Branch != "feat_wt/y" || testkit.Str(p.Stack[1].OwnRemote.Blocks) != wtsync.OwnBlockDiverged {
		t.Fatalf("worktree %+v stack %+v", p.Worktree.OwnRemote, p.Stack)
	}
	// The run refuses both, the diverged member for itself and the other
	// because of it.
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil || len(r.Worktrees) != 2 || r.Worktrees[0].Result != synccmd.ResultNotRun || r.Worktrees[1].Result != synccmd.ResultRefused {
		t.Fatalf("%v %+v\n%s", err, r.Worktrees, human)
	}
	if r.Worktrees[0].OwnRemoteSync.SkippedReason != nil || testkit.Str(r.Worktrees[1].OwnRemoteSync.SkippedReason) != wtsync.OwnBlockDiverged {
		t.Fatalf("skipped reasons %+v %+v", r.Worktrees[0].OwnRemoteSync, r.Worktrees[1].OwnRemoteSync)
	}

	// x falls behind as well, with tracked changes in it.
	f.Theirs(t, "feat_wt/x", "tx.txt", "tx\n")
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	testkit.WriteIn(t, x, "x.txt", "edited\n")
	p = synccmdtest.PlanOfWork(t, f.Ctx, "y")
	if testkit.Str(p.UpIneligibleCode) != synccmd.IneligibleOwnBehind || p.Token != nil {
		t.Fatalf("both kinds: code %s token %s", testkit.Str(p.UpIneligibleCode), testkit.Str(p.Token))
	}
	if want := "feat_wt/x (x), a worktree in this stack, is 1 commit behind origin/feat_wt/x"; !strings.HasPrefix(testkit.Str(p.UpIneligibleReason), want) {
		t.Fatalf("reason %q", testkit.Str(p.UpIneligibleReason))
	}
}

// A stack whose parent is behind its remote: the parent is fast-forwarded
// and rebased, and the child lands on the parent's final tip with its own
// commits and nothing else.
func TestUpFastForwardsAStackParent(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	var buf bytes.Buffer
	y, err := provision.New(f.Ctx, "feat/y", provision.NewOptions{NoSetup: true, Base: "feat_wt/x"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	testkit.Mine(t, y, "y.txt", "y\n")
	f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.AdvanceOrigin(t, 1)
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "y", opts)
	if err != nil || len(r.Worktrees) != 2 {
		t.Fatalf("%v %+v\n%s", err, r.Worktrees, human)
	}
	if px := r.Worktrees[0]; px.Branch != "feat_wt/x" || px.Result != synccmd.ResultRebased || !px.OwnRemoteSync.FastForwarded {
		t.Fatalf("parent %+v %+v", px, px.OwnRemoteSync)
	}
	if py := r.Worktrees[1]; py.Result != synccmd.ResultRebased || py.OwnRemoteSync.State != "none" || py.OwnRemoteSync.FastForwarded {
		t.Fatalf("child %+v %+v", py, py.OwnRemoteSync)
	}
	if testkit.GitOut(t, y, "rev-parse", "HEAD~1") != testkit.GitOut(t, x, "rev-parse", "HEAD") {
		t.Fatalf("the child is not one commit on the parent's final tip:\n%s", human)
	}
	if testkit.GitOut(t, y, "show", "HEAD:t.txt") != "t" {
		t.Fatal("the child lacks what the parent's fast-forward brought")
	}
}

// A branch cut from trunk that tracks trunk has no ref of its own on origin:
// nothing to check, on every run, however far its upstream is ahead. It is
// never fast-forwarded onto trunk and never refused.
func TestUpLeavesABranchThatTracksTrunkUnchecked(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	opts := f.WithWork(t)
	wt, err := kit.Locate(f.Ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, wt.Path, "branch", "-q", "--set-upstream-to=origin/main")
	for i, nanos := range []int64{99, 100} {
		f.AdvanceOrigin(t, 2)
		if o := synccmdtest.PlanOfWork(t, f.Ctx, "x").Worktree.OwnRemote; o.State != "none" || o.Ref != nil || o.Blocks != nil {
			t.Fatalf("run %d plan: %+v", i+1, o)
		}
		r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", synccmdtest.At(opts, nanos))
		if err != nil {
			t.Fatalf("run %d: %v\n%s", i+1, err, human)
		}
		if o := r.Worktrees[0].OwnRemoteSync; r.Worktrees[0].Result != synccmd.ResultRebased || o.State != "none" || o.FastForwarded || o.Ref != nil {
			t.Fatalf("run %d: %+v %+v", i+1, r.Worktrees[0], o)
		}
		if strings.Contains(human, "fast-forwarded to origin/main") && !strings.Contains(human, "local main") {
			t.Fatalf("run %d fast-forwarded the branch onto trunk:\n%s", i+1, human)
		}
		if n := testkit.GitOut(t, wt.Path, "rev-list", "--count", "origin/main..HEAD"); n != "1" {
			t.Fatalf("run %d: %s commits on top of trunk, want its own one", i+1, n)
		}
	}
}

// A ref that went away on the remote, and a remote where a push lands
// somewhere a tracking ref cannot show: the run goes on, and says so for the
// one it could not check.
func TestUpGoesOnWhenTheRemoteIsGoneOrCannotBeRead(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	t.Run("gone", func(t *testing.T) {
		f := synccmdtest.NewTrunkFixture(t)
		_, opts := f.PushedWork(t)
		testkit.GitIn(t, f.Origin, "update-ref", "-d", "refs/heads/feat_wt/x")
		f.AdvanceOrigin(t, 1)
		r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
		if err != nil {
			t.Fatalf("%v\n%s", err, human)
		}
		if o := r.Worktrees[0].OwnRemoteSync; r.Worktrees[0].Result != synccmd.ResultRebased || o.State != "gone" || testkit.Str(o.Ref) != "origin/feat_wt/x" || o.Remote != nil || o.RemoteAhead != nil {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		f := synccmdtest.NewTrunkFixture(t)
		_, opts := f.PushedWork(t)
		f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
		f.AdvanceOrigin(t, 1)
		testkit.GitIn(t, f.Main, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "elsewhere.git"))
		r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
		if err != nil {
			t.Fatalf("%v\n%s", err, human)
		}
		if o := r.Worktrees[0].OwnRemoteSync; r.Worktrees[0].Result != synccmd.ResultRebased || o.State != "unknown" || o.Ref != nil || o.Remote != nil || o.FastForwarded {
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
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	theirs := f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	f.AdvanceOrigin(t, 1)
	testkit.GitIn(t, f.Main, "remote", "add", "fork", filepath.Join(t.TempDir(), "no-such.git"))
	testkit.GitIn(t, f.Main, "config", "branch.feat_wt/x.pushRemote", "fork")
	testkit.GitIn(t, f.Main, "config", "push.default", "current")
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil || r.Error != nil || !r.Fetched || len(r.Worktrees) != 1 {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	w := r.Worktrees[0]
	if w.Result != synccmd.ResultRefused || testkit.Str(w.OwnRemoteSync.SkippedReason) != syncrun.OwnSkipFetchFailed || w.OwnRemoteSync.FastForwarded ||
		!strings.Contains(testkit.Str(w.Reason), "its own remote could not be fetched (") {
		t.Fatalf("%+v %+v\n%s", w, w.OwnRemoteSync, human)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before || testkit.SafetyRefs(t, f.Main) != "" {
		t.Fatal("a refused run touched something")
	}
	// Back on origin, without fetching: behind as last fetched, and said so.
	testkit.GitIn(t, f.Main, "config", "--unset", "branch.feat_wt/x.pushRemote")
	opts.NoFetch = true
	r, human, err = synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded {
		t.Fatalf("--no-fetch: %v %+v\n%s", err, r, human)
	}
	if !strings.Contains(human, "✓ fast-forwarded to origin/feat_wt/x  "+before[:7]+" → "+theirs[:7]+" (1 commit) (as last fetched)\n") {
		t.Fatalf("not said to be as last fetched:\n%s", human)
	}
}

// The run's own fetch brings the branch's remote: nobody fetched it before.
func TestUpFetchesTheBranchsOwnRemoteWithTrunk(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	stale := testkit.GitOut(t, f.Main, "rev-parse", "origin/feat_wt/x")
	theirs := f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	testkit.GitIn(t, f.Other, "checkout", "-q", "main")
	testkit.GitIn(t, f.Other, "commit", "-q", "--allow-empty", "-m", "origin moves")
	testkit.GitIn(t, f.Other, "push", "-q", "origin", "main")
	if testkit.GitOut(t, f.Main, "rev-parse", "origin/feat_wt/x") != stale {
		t.Fatal("the fixture fetched")
	}
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err != nil || !r.Worktrees[0].OwnRemoteSync.FastForwarded || testkit.Str(r.Worktrees[0].OwnRemoteSync.Remote) != theirs {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	if testkit.GitOut(t, x, "show", "HEAD:t.txt") != "t" {
		t.Fatal("the remote's commit is not in the rebased branch")
	}
}

// The overview carries the same object on each worktree, fetched with trunk,
// and refuses the diverged one in words; its token lets a run under
// --allow-diverged go ahead over exactly the divergence it showed.
func TestSyncOverviewReportsOwnRemoteAndItsTokenCoversADivergence(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	f.Declare(t)
	testkit.GitIn(t, f.Main, "fetch", "-q", "origin")
	x, opts := f.PushedWork(t)
	overview := func(noFetch bool) synccmd.SyncOverview {
		t.Helper()
		var buf bytes.Buffer
		if err := synccmd.SyncJSON(f.Ctx, synccmd.SyncOptions{NoFetch: noFetch}, &buf); err != nil {
			t.Fatalf("%v\n%s", err, buf.String())
		}
		testkit.ValidateJSON(t, "sync", buf.Bytes())
		var o synccmd.SyncOverview
		if err := json.Unmarshal(buf.Bytes(), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	f.AdvanceOrigin(t, 1)
	plain := overview(true)
	if row := synccmdtest.OverviewRow(t, plain, "x"); row.OwnRemote.State != "inSync" || row.OwnRemote.Fetched || !row.Runnable {
		t.Fatalf("in sync, not fetched: %+v", row)
	}

	theirs := f.Theirs(t, "feat_wt/x", "t.txt", "t\n")
	before := testkit.Mine(t, x, "m.txt", "m\n")
	o := overview(false)
	row := synccmdtest.OverviewRow(t, o, "x")
	if row.OwnRemote.State != "diverged" || !row.OwnRemote.Fetched || testkit.Str(row.OwnRemote.Commit) != theirs || testkit.Str(row.OwnRemote.Blocks) != wtsync.OwnBlockDiverged {
		t.Fatalf("ownRemote %+v", row.OwnRemote)
	}
	if row.Verdict != synccmd.VerdictRefuse || row.Runnable || row.Group != synccmd.GroupNeedsYou || row.Class != "conflict-free" ||
		!strings.Contains(testkit.Str(row.Reason), "origin/feat_wt/x has 1 commit this branch never had") {
		t.Fatalf("row %+v", row)
	}
	if o.Token == nil || *o.Token == *plain.Token {
		t.Fatalf("token %s does not cover the divergence (in sync it was %s)", testkit.Str(o.Token), testkit.Str(plain.Token))
	}

	// The command gittree's sheet runs.
	opts.IfReady, opts.Expect = true, *o.Token
	if r, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts); err == nil || r.Worktrees[0].Result != synccmd.ResultRefused {
		t.Fatalf("without --allow-diverged: %v %+v", err, r.Worktrees)
	}
	opts.AllowDiverged = true
	f.Theirs(t, "feat_wt/x", "t2.txt", "t2\n")
	r, err := synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
	if err == nil || len(r.Worktrees) != 0 || !strings.Contains(testkit.Str(r.Error), "the overview changed since it was read") {
		t.Fatalf("a remote that moved since the look: %v %+v", err, r)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("a refused run moved the branch")
	}
	opts.Expect = *overview(false).Token
	r, err = synccmdtest.SyncRunJSON(t, f.Ctx, []string{"x"}, opts)
	if err != nil || r.Worktrees[0].Result != synccmd.ResultRebased || !r.Worktrees[0].OwnRemoteSync.Allowed {
		t.Fatalf("the overview as read: %v %+v", err, r)
	}
}

// A run with nothing named leaves a diverged worktree alone and names what
// holds it; --if-ready makes that a failure.
func TestSyncRunWithNothingNamedLeavesADivergedWorktree(t *testing.T) {
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

	var out bytes.Buffer
	if err := synccmd.SyncRebase(f.Ctx, nil, opts, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "nothing is ready to rebase") || !strings.Contains(out.String(), "left as they are: x conflict-free, diverged from origin/feat_wt/x") {
		t.Fatalf("out:\n%s", out.String())
	}
	opts.IfReady = true
	out.Reset()
	if err := synccmd.SyncRebase(f.Ctx, nil, opts, &out); err == nil || !strings.Contains(err.Error(), "x (not ready)") {
		t.Fatalf("--if-ready: %v\n%s", err, out.String())
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before {
		t.Fatal("the branch moved")
	}
	// --allow-diverged takes it, named.
	opts.AllowDiverged = true
	out.Reset()
	if err := synccmd.SyncRebase(f.Ctx, []string{"x"}, opts, &out); err != nil {
		t.Fatalf("--allow-diverged: %v\n%s", err, out.String())
	}
	if !testkit.GitAncestor(t, f.Main, "origin/main", "feat_wt/x") {
		t.Fatal("not rebased")
	}
}

// Rebased on another machine and pushed, with this checkout left on the old
// versions: the remote's tip was never this branch's, and what only the
// remote has includes trunk's commits, so it is diverged and refused like
// any other. The sentence says what it looks like.
func TestUpRefusesABranchRebasedElsewhereAndSaysSo(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	f.AdvanceOrigin(t, 1)
	testkit.GitIn(t, f.Other, "fetch", "-q", "origin")
	testkit.GitIn(t, f.Other, "checkout", "-q", "-B", "feat_wt/x", "origin/feat_wt/x")
	testkit.GitIn(t, f.Other, "rebase", "-q", "origin/main")
	testkit.GitIn(t, f.Other, "push", "-q", "--force", "origin", "feat_wt/x")
	testkit.GitIn(t, f.Other, "checkout", "-q", "-B", "main", "origin/main")

	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil {
		t.Fatalf("rebased over the remote's rebase:\n%s", human)
	}
	w := r.Worktrees[0]
	want := "x: origin/feat_wt/x has every commit of this branch in another form, on 2 commits this branch never had: " +
		"it looks rebased elsewhere and pushed. git reset --hard origin/feat_wt/x takes the remote's, or --allow-diverged rebases this copy as it stands"
	if w.Result != synccmd.ResultRefused || testkit.Str(w.Reason) != want || w.OwnRemoteSync.State != "diverged" || testkit.Str(w.OwnRemoteSync.SkippedReason) != wtsync.OwnBlockDiverged {
		t.Fatalf("%+v %+v\n%s", w, w.OwnRemoteSync, human)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before || testkit.SafetyRefs(t, f.Main) != "" {
		t.Fatal("a refused run touched something")
	}
	p := synccmdtest.PlanOfWork(t, f.Ctx, "x")
	wantPlan := "feat_wt/x has diverged from origin/feat_wt/x: every commit it has (1) is there in another form, on 2 commits it never had. " +
		"It looks rebased elsewhere and pushed. Take the remote's with git reset --hard origin/feat_wt/x, or rebase this copy as it stands with --allow-diverged."
	if testkit.Str(p.UpIneligibleCode) != synccmd.IneligibleOwnDiverged || testkit.Str(p.UpIneligibleReason) != wantPlan || p.Token == nil {
		t.Fatalf("plan: %s %q", testkit.Str(p.UpIneligibleCode), testkit.Str(p.UpIneligibleReason))
	}
}

// A git that refuses the fast-forward itself touches nothing: the
// participant is refused with the reason failed, and no ref is left.
func TestUpRefusesWhenTheFastForwardItselfFails(t *testing.T) {
	t.Parallel()
	f := synccmdtest.NewTrunkFixture(t)
	x, opts := f.PushedWork(t)
	before := testkit.GitOut(t, x, "rev-parse", "HEAD")
	f.Theirs(t, "feat_wt/x", "t.txt", "theirs\n")
	f.AdvanceOrigin(t, 1)
	// An untracked file the remote's commit would overwrite.
	testkit.WriteIn(t, x, "t.txt", "scratch\n")
	r, human, err := synccmdtest.UpOut(t, f.Ctx, "x", opts)
	if err == nil {
		t.Fatalf("overwrote an untracked file:\n%s", human)
	}
	w := r.Worktrees[0]
	if w.Result != synccmd.ResultRefused || testkit.Str(w.OwnRemoteSync.SkippedReason) != syncrun.OwnSkipFailed || w.OwnRemoteSync.FastForwarded ||
		testkit.Str(w.Reason) != "cannot fast-forward to origin/feat_wt/x: untracked files in the way: t.txt" {
		t.Fatalf("%+v %+v\n%s", w, w.OwnRemoteSync, human)
	}
	if got := testkit.GitOut(t, x, "rev-parse", "HEAD"); got != before || testkit.SafetyRefs(t, f.Main) != "" {
		t.Fatalf("something moved: HEAD %s, refs %q", got, testkit.SafetyRefs(t, f.Main))
	}
}
