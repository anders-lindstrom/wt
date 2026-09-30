package commands

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

func keptCodes(p SweepPlan, name string) []string {
	for _, b := range p.CheckedOut {
		if b.Name == name {
			return b.KeptCodes
		}
	}
	return nil
}

func TestSweepPlanKeepsAMergedWorktreeMidOperation(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/login-crash")
	mustWrite(t, filepath.Join(gitDirFor(t, path), "MERGE_HEAD"), "x\n")

	p := planOf(t, ctx)
	if len(p.Remove) != 0 {
		t.Fatalf("a worktree mid-merge must not be removed: %v", worktreeNames(p.Remove))
	}
	if codes := keptCodes(p, "fix_wt/login-crash"); !slices.Contains(codes, KeptOperation) {
		t.Errorf("codes = %v, want %s", codes, KeptOperation)
	}
	if out := rendered(p); !strings.Contains(out, "a merge is in progress") {
		t.Errorf("the row must say why:\n%s", out)
	}
}

func TestSweepPlanKeepsAMergedWorktreeWithHiddenChanges(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	mustWrite(t, filepath.Join(main, "a.txt"), "a")
	gitIn(t, main, "add", "a.txt")
	gitIn(t, main, "commit", "-qm", "a")
	gitIn(t, main, "push", "-q", "origin", "main")
	path := mergedWorktree(t, ctx, "fix/login-crash")
	gitIn(t, path, "update-index", "--skip-worktree", "a.txt")

	p := planOf(t, ctx)
	if codes := keptCodes(p, "fix_wt/login-crash"); !slices.Contains(codes, KeptHiddenChanges) {
		t.Errorf("codes = %v, want %s", codes, KeptHiddenChanges)
	}
}

func TestSweepPlanKeepsAMergedWorktreeWithWorkInASubmodule(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	inner := filepath.Join(t.TempDir(), "inner")
	gitIn(t, filepath.Dir(inner), "init", "-q", "-b", "main", "inner")
	gitIn(t, inner, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "i")
	gitIn(t, main, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "sm")
	gitIn(t, main, "commit", "-qm", "sm")
	gitIn(t, main, "push", "-q", "origin", "main")
	path := mergedWorktree(t, ctx, "fix/login-crash")
	gitIn(t, path, "-c", "protocol.file.allow=always", "submodule", "update", "-q", "--init")
	gitIn(t, path, "config", "submodule.sm.ignore", "all")
	mustWrite(t, filepath.Join(path, "sm", "scratch"), "x")

	p := planOf(t, ctx)
	if codes := keptCodes(p, "fix_wt/login-crash"); !slices.Contains(codes, KeptDirty) {
		t.Errorf("codes = %v, want %s", codes, KeptDirty)
	}
}

// Two worktrees whose branches share a tip trunk has only as a cherry-pick:
// each holds the other's commits while the plan is made, and removing the
// first is the sweep's own doing, not a change that should keep the second.
func TestSweepRemovesTwoWorktreesSharingAnAppliedTip(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	a := mergedWorktree(t, ctx, "fix/first")
	mustWrite(t, filepath.Join(a, "change.txt"), "the change\n")
	gitIn(t, a, "add", "change.txt")
	gitIn(t, a, "commit", "-qm", "the change")
	tip := strings.TrimSpace(gitOut(t, a, "rev-parse", "HEAD"))
	b := mergedWorktree(t, ctx, "fix/second")
	gitIn(t, b, "reset", "-q", "--hard", tip)
	gitIn(t, main, "cherry-pick", "-x", tip)
	gitIn(t, main, "push", "-q", "origin", "main")

	var buf bytes.Buffer
	if err := Sweep(ctx, SweepOptions{Yes: true, NoFetch: true, Agents: []wtsync.Agent{}}, &buf); err != nil {
		t.Fatalf("Sweep: %v\n%s", err, buf.String())
	}
	if exists(a) || exists(b) {
		t.Errorf("both worktrees should have gone:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "--only refs/heads/fix_wt/second --yes puts the branch back") {
		t.Errorf("the last branch holding the commit says how to get it back:\n%s", buf.String())
	}
}
