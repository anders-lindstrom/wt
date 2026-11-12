package synccmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

func layersOf(s *syncrun.Stack) map[string]syncrun.StackLayer {
	out := map[string]syncrun.StackLayer{}
	if s == nil {
		return out
	}
	for _, l := range s.Layers {
		out[l.Branch] = l
	}
	return out
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// wt sync <tmp> --stack --json lists the layers bottom first with what holds
// each worktree, and moves nothing.
func TestSyncStackLookListsTheLayersAndMovesNothing(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	o := synccmdtest.OverviewJSONWith(t, ctx, synccmd.SyncOptions{NoFetch: true, Stack: &synccmd.StackLook{Work: gittest.StackTmp}})
	if o.Stack == nil || len(o.Stack.Layers) != 5 {
		t.Fatalf("stack %+v", o.Stack)
	}
	var order []string
	for _, l := range o.Stack.Layers {
		order = append(order, l.Branch)
		if l.NewTip != nil || l.Result != nil {
			t.Errorf("%s has a run's fields in the look: %+v", l.Branch, l)
		}
	}
	if strings.Join(order, " ") != "s1 s2 s3 s4 s5" {
		t.Fatalf("order %v", order)
	}
	layers := layersOf(o.Stack)
	for b, want := range map[string]string{"s1": wtsync.HolderClean, "s2": wtsync.HolderNone, "s3": wtsync.HolderDirty,
		"s4": wtsync.HolderNone, "s5": wtsync.HolderProcess} {
		if l := layers[b]; l.State != want {
			t.Errorf("%s is %s (%s), want %s", b, l.State, str(l.StateDetail), want)
		}
	}
	if l := layers["s3"]; l.Commits != 7 || l.Subject != "chore(s3): bump to 1.0.1" || l.OldTip != s.Tips["s3"] || str(l.Worktree) != s.Paths["s3"] {
		t.Errorf("s3 %+v", l)
	}
	for _, b := range append([]string{gittest.StackTmp}, s.Layers...) {
		if testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", b) != s.Tips[b] {
			t.Errorf("%s moved", b)
		}
	}
}

// One --stack run rebases the line once and puts each layer on its new tip:
// the clean worktree is reset with its branch, the branches in no worktree
// are moved, the dirty worktree and the one a process works in are held with
// the command that moves them, and the emptied and tip-dropped layers are
// reported as such.
func TestSyncRebaseStackRebasesOnceAndMovesTheLayers(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, synccmdtest.NoAgents())
	if err == nil || !strings.Contains(err.Error(), "2 layers held") {
		t.Fatalf("err %v, want the run to say two layers were held\n%s", err, human)
	}
	p := synccmdtest.Participant(t, res, gittest.StackTmp)
	if p.Result != syncrun.ResultRebased || p.Stack == nil {
		t.Fatalf("line %+v\n%s", p, human)
	}
	if len(res.Worktrees) != 1 {
		t.Errorf("the run rebased %d worktrees, want only the line", len(res.Worktrees))
	}
	main := ctx.Repo.MainRoot
	layers := layersOf(p.Stack)
	for b, want := range map[string]struct{ result, mapped, count string }{
		"s1": {wtsync.MoveMoved, wtsync.MappedTip, "2"},
		"s2": {wtsync.MoveMoved, wtsync.MappedTipDropped, "4"},
		"s3": {wtsync.MoveHeld, wtsync.MappedTip, "6"},
		"s4": {wtsync.MoveMoved, wtsync.MappedEmptied, "6"},
		"s5": {wtsync.MoveHeld, wtsync.MappedTip, "9"},
	} {
		l := layers[b]
		if str(l.Result) != want.result || str(l.Mapped) != want.mapped {
			t.Errorf("%s: %s %s, want %s %s (%s)", b, str(l.Result), str(l.Mapped), want.result, want.mapped, str(l.Held))
			continue
		}
		if n := testkit.GitOut(t, main, "rev-list", "--count", "origin/main.."+str(l.NewTip)); n != want.count {
			t.Errorf("%s's new tip has %s commits on trunk, want %s", b, n, want.count)
		}
		tip := testkit.GitOut(t, main, "rev-parse", b)
		switch want.result {
		case wtsync.MoveMoved:
			if tip != str(l.NewTip) {
				t.Errorf("%s is at %s, want %s", b, tip, str(l.NewTip))
			}
		case wtsync.MoveHeld:
			if tip != s.Tips[b] || !strings.Contains(str(l.MoveCommand), "reset --keep "+str(l.NewTip)) {
				t.Errorf("%s: at %s, command %q", b, tip, str(l.MoveCommand))
			}
		}
	}
	if head := testkit.GitOut(t, s.Paths["s1"], "rev-parse", "HEAD"); head != str(layers["s1"].NewTip) {
		t.Error("s1's worktree was not reset with its branch")
	}
	if st := testkit.GitOut(t, s.Paths["s1"], "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Errorf("s1's worktree is not clean: %s", st)
	}
	if !strings.Contains(str(layers["s3"].Held), "dirty") || !strings.Contains(str(layers["s5"].Held), "process") {
		t.Errorf("held: s3 %q, s5 %q", str(layers["s3"].Held), str(layers["s5"].Held))
	}
	if refs := testkit.GitOut(t, main, "for-each-ref", wtsync.StackMapPrefix); refs != "" {
		t.Errorf("markers left behind: %s", refs)
	}
	if testkit.GitOut(t, main, "rev-parse", "tmp/stack~0") == s.Tips[gittest.StackTmp] {
		t.Error("the line was not rebased")
	}
}

// A line with a merge commit is refused before anything moves.
func TestSyncRebaseStackRefusesALineWithAMerge(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	tmp := s.Paths[gittest.StackTmp]
	main := ctx.Repo.MainRoot
	testkit.GitIn(t, main, "branch", "side", s.Tips["s1"])
	testkit.GitIn(t, main, "worktree", "add", "-q", main+"-side", "side")
	if err := os.WriteFile(filepath.Join(main+"-side", "side.txt"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main+"-side", "add", "-A")
	testkit.GitIn(t, main+"-side", "commit", "-q", "-m", "side")
	testkit.GitIn(t, tmp, "merge", "-q", "--no-edit", "side")
	head := testkit.GitOut(t, tmp, "rev-parse", "HEAD")
	_, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, synccmdtest.NoAgents())
	if err == nil || !strings.Contains(err.Error(), "merge commit") {
		t.Fatalf("err %v\n%s", err, human)
	}
	if testkit.GitOut(t, tmp, "rev-parse", "HEAD") != head {
		t.Fatal("the line moved")
	}
	if refs := testkit.GitOut(t, main, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Fatalf("a safety ref was written: %s", refs)
	}
}

// A check of the new tips that fails moves no layer, and leaves the line
// rebased.
func TestSyncRebaseStackMovesNoLayerWhenACheckFails(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	opts := synccmdtest.NoAgents()
	// A hand-made mismatch: the plan expects another subject at s2's marker.
	opts.Stack = &synccmd.StackOptions{BeforeCheck: func(rec *wtsync.StackRecord) { rec.Plans[1].Subject = "not this" }}
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts)
	if err == nil || !strings.Contains(err.Error(), "no layer moved") {
		t.Fatalf("err %v\n%s", err, human)
	}
	p := synccmdtest.Participant(t, res, gittest.StackTmp)
	if p.Stack == nil || !strings.Contains(str(p.Stack.Refusal), "s2") {
		t.Fatalf("stack %+v", p.Stack)
	}
	for _, b := range s.Layers {
		if testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", b) != s.Tips[b] {
			t.Errorf("%s moved", b)
		}
	}
	if testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", gittest.StackTmp) == s.Tips[gittest.StackTmp] {
		t.Error("the line was not left rebased")
	}
}
