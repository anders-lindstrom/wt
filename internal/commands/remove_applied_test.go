package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// commitFile commits a file of its own in the checkout at dir, so the commit
// carries a change git cherry can find elsewhere.
func commitFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-q", "-m", "add "+name)
}

// rebasedOntoOrigin moves trunk on, then replays the branch's commits onto it
// and pushes that as origin's main: what "rebase and merge" on GitHub leaves.
// The local branch keeps its old ids, so no trunk contains it.
func rebasedOntoOrigin(t *testing.T, main, branch string) {
	t.Helper()
	commitFile(t, main, "trunk-moved-on")
	gitIn(t, main, "cherry-pick", "main.."+branch)
	gitIn(t, main, "push", "-q", "origin", "main")
	gitIn(t, main, "fetch", "-q", "origin")
}

// A branch GitHub rebased before merging looks unmerged to git for ever, and
// the pull request's head is the rebased tip, not this one. Every change is
// on trunk all the same, so nothing is lost by deleting it.
func TestRemoveDeletesABranchWhoseCommitsWereRebasedOntoTrunk(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	var buf bytes.Buffer
	path, err := New(ctx, "feat/rates", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, path, "one")
	commitFile(t, path, "two")
	rebasedOntoOrigin(t, main, "feat_wt/rates")
	buf.Reset()

	if err := RemoveAt(ctx, path, RemoveOptions{Agents: []wtsync.Agent{}}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if ctx.Repo.BranchExists("feat_wt/rates") || ctx.Repo.BranchExists("rates") {
		t.Errorf("a branch whose every commit is on trunk should be deleted, not kept:\n%s", buf.String())
	}
	for _, want := range []string{
		"every commit is on origin/main under a new id, rebased or cherry-picked, so git still counts it 2 commits ahead of origin/main",
		"the branch will be deleted (every commit is on origin/main)",
		"✓ worktree removed; every commit of feat_wt/rates is on origin/main, so the branch has been deleted",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("want %q in:\n%s", want, buf.String())
		}
	}
}

// One commit trunk does not have is work at stake, however many others made
// it: the branch is kept.
func TestRemoveKeepsABranchWithACommitTrunkLacks(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	var buf bytes.Buffer
	path, err := New(ctx, "feat/rates", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, path, "one")
	rebasedOntoOrigin(t, main, "feat_wt/rates")
	commitFile(t, path, "not-landed")
	buf.Reset()

	if err := RemoveAt(ctx, path, RemoveOptions{Agents: []wtsync.Agent{}}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if !ctx.Repo.BranchExists("rates") {
		t.Fatalf("a commit trunk lacks must keep the branch:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "not merged: 2 commits ahead of origin/main") {
		t.Errorf("the plan says what is at stake:\n%s", buf.String())
	}
}

// Sweep sees the same branch as done, so its worktree goes with it.
func TestSweepRemovesAWorktreeWhoseCommitsWereRebasedOntoTrunk(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	var buf bytes.Buffer
	path, err := New(ctx, "feat/rates", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, path, "one")
	rebasedOntoOrigin(t, main, "feat_wt/rates")

	p := planOf(t, ctx)
	if !slices.Equal(worktreeNames(p.Remove), []string{"feat_wt/rates"}) {
		t.Fatalf("want the worktree removed:\n%s", rendered(p))
	}
	if out := rendered(p); !strings.Contains(out, "every commit is on origin/main under a new id (rebased or cherry-picked)") {
		t.Errorf("the plan says why it counts as done:\n%s", out)
	}
	if err := p.apply(ctx, nobodyIn, &buf); err != nil {
		t.Fatalf("apply: %v\n%s", err, buf.String())
	}
	if exists(path) || ctx.Repo.BranchExists("feat_wt/rates") {
		t.Errorf("the worktree and its branch should be gone:\n%s", buf.String())
	}
}

// A branch left behind once its worktree is gone, upstream deleted on merge,
// is deleted the same way.
func TestSweepPlanDeletesAGoneBranchWhoseCommitsWereRebasedOntoTrunk(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	gitIn(t, main, "checkout", "-q", "-b", "left-behind")
	commitFile(t, main, "one")
	gitIn(t, main, "checkout", "-q", "main")
	goneUpstream(t, main, "left-behind")
	rebasedOntoOrigin(t, main, "left-behind")

	p := planOf(t, ctx)
	if !slices.Contains(branchNames(p.Delete), "left-behind") {
		t.Fatalf("want the branch deleted:\n%s", rendered(p))
	}
}

// Something else — a session ending, an editor closing its workspace — can
// remove the worktree while the prompt is open. Saying "run the command
// again" would send the user after a checkout that is gone.
func TestRemoveSaysSomethingElseRemovedTheWorktree(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	var buf bytes.Buffer
	path, err := New(ctx, "feat/elsewhere", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, path, "kept-work")
	buf.Reset()

	for name, alsoBranch := range map[string]bool{"branch gone too": true, "branch left": false} {
		t.Run(name, func(t *testing.T) {
			if !exists(path) {
				var err error
				if path, err = New(ctx, "feat/elsewhere", NewOptions{NoSetup: true}, &buf); err != nil {
					t.Fatal(err)
				}
				commitFile(t, path, "kept-work")
			}
			buf.Reset()
			opts := RemoveOptions{Agents: []wtsync.Agent{}, Confirm: func(Plan) (bool, error) {
				gitIn(t, main, "worktree", "remove", "--force", path)
				if alsoBranch {
					gitIn(t, main, "branch", "-D", "feat_wt/elsewhere")
				}
				return true, nil
			}}
			if err := RemoveAt(ctx, path, opts, &buf); err != nil {
				t.Fatalf("the worktree is gone, which is what was asked: %v\n%s", err, buf.String())
			}
			out := buf.String()
			if !strings.Contains(out, "Something else removed the worktree while the prompt was open; wt removed nothing.") {
				t.Errorf("say what happened:\n%s", out)
			}
			want := "branch feat_wt/elsewhere is gone too"
			if !alsoBranch {
				want = "branch feat_wt/elsewhere is still here (1 commit ahead of origin/main); wt sweep lists it"
			}
			if !strings.Contains(out, want) {
				t.Errorf("want %q in:\n%s", want, out)
			}
			if strings.Contains(out, "run the command again") {
				t.Errorf("there is nothing left to run it on:\n%s", out)
			}
			if !alsoBranch {
				gitIn(t, main, "branch", "-D", "feat_wt/elsewhere")
			}
		})
	}
}

// Files disappearing from a checkout that was clean are a deletion in
// progress, not somebody's edits.
func TestRemoveSaysWhenSomethingElseIsStillRemovingTheWorktree(t *testing.T) {
	was := vanishWait
	vanishWait = 50 * time.Millisecond
	t.Cleanup(func() { vanishWait = was })

	ctx, _, _ := sweepRepo(t)
	var buf bytes.Buffer
	path, err := New(ctx, "feat/elsewhere", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, path, "going")
	buf.Reset()

	opts := RemoveOptions{Agents: []wtsync.Agent{}, Confirm: func(Plan) (bool, error) {
		return true, os.Remove(filepath.Join(path, "going"))
	}}
	err = RemoveAt(ctx, path, opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "is being removed by something else") {
		t.Fatalf("want an error naming the other removal, got %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "wt removed nothing.") || strings.Contains(buf.String(), "Removal would now do this") {
		t.Errorf("say what is happening instead of re-planning:\n%s", buf.String())
	}
	if !ctx.Repo.BranchExists("feat_wt/elsewhere") {
		t.Error("wt must not touch the branch")
	}
}
