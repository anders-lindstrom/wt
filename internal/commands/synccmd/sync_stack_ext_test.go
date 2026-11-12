package synccmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
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

// --autosquash folds two fixups at the top of the line, whose targets are in
// layers 2 and 4, onto the line's own base: the line's content is the same,
// the targets carry the fixes, every layer is re-pointed with its count
// unchanged, and nothing is rebased onto trunk.
func TestSyncRebaseStackAutosquashFoldsInPlace(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	tmp := s.Paths[gittest.StackTmp]
	for _, f := range []struct{ file, target string }{{"b1.txt", "feat(s2): b1.txt"}, {"d1.txt", "feat(s4): d1.txt"}} {
		if err := os.WriteFile(filepath.Join(tmp, f.file), []byte("fixed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		testkit.GitIn(t, tmp, "commit", "-q", "--no-verify", "-am", "fixup! "+f.target)
	}
	tree := testkit.GitOut(t, tmp, "rev-parse", "HEAD^{tree}")
	opts := synccmdtest.NoAgents()
	opts.Stack = &synccmd.StackOptions{Autosquash: true}
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts)
	if err == nil || !strings.Contains(err.Error(), "2 layers held") {
		t.Fatalf("err %v\n%s", err, human)
	}
	if got := testkit.GitOut(t, tmp, "rev-parse", "HEAD^{tree}"); got != tree {
		t.Fatal("the fold changed the line's content")
	}
	main := ctx.Repo.MainRoot
	if n := testkit.GitOut(t, main, "rev-list", "--count", s.Base+".."+gittest.StackTmp); n != "12" {
		t.Errorf("the line has %s commits from its base, want 12: both fixups folded, nothing from trunk", n)
	}
	p := synccmdtest.Participant(t, res, gittest.StackTmp)
	if p.Stack == nil || !p.Stack.Autosquash {
		t.Fatalf("stack %+v", p.Stack)
	}
	layers := layersOf(p.Stack)
	for b, count := range map[string]int{"s1": 2, "s2": 5, "s3": 7, "s4": 9, "s5": 12} {
		l := layers[b]
		if l.CommitsAfter == nil || *l.CommitsAfter != count {
			t.Errorf("%s: %+v, want %d commits", b, l.CommitsAfter, count)
		}
	}
	for b, file := range map[string]string{"s2": "b1.txt", "s4": "d1.txt"} {
		if got := testkit.GitOut(t, main, "show", str(layers[b].NewTip)+":"+file); got != "fixed" {
			t.Errorf("%s's new tip has %s %q", b, file, got)
		}
		if testkit.GitOut(t, main, "rev-parse", b) != str(layers[b].NewTip) {
			t.Errorf("%s not re-pointed", b)
		}
	}
	if str(layers["s1"].Result) != wtsync.MoveUnchanged {
		t.Errorf("s1 %s, want unchanged: nothing below it changed", str(layers["s1"].Result))
	}
}

// A layer that gains a commit after the look and before its move is held,
// not reset.
func TestSyncRebaseStackHoldsALayerThatMovedDuringTheRun(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	s1 := s.Paths["s1"]
	opts := synccmdtest.NoAgents()
	opts.Stack = &synccmd.StackOptions{BeforeCheck: func(*wtsync.StackRecord) {
		if err := os.WriteFile(filepath.Join(s1, "late.txt"), []byte("late\n"), 0o644); err != nil {
			t.Error(err)
		}
		testkit.GitIn(t, s1, "add", "-A")
		testkit.GitIn(t, s1, "commit", "-q", "--no-verify", "-m", "late")
	}}
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts)
	if err == nil || !strings.Contains(err.Error(), "3 layers held") {
		t.Fatalf("err %v\n%s", err, human)
	}
	l := layersOf(synccmdtest.Participant(t, res, gittest.StackTmp).Stack)["s1"]
	if str(l.Result) != wtsync.MoveHeld || !strings.Contains(str(l.Held), "moved during the run") {
		t.Fatalf("s1 %+v", l)
	}
	if testkit.GitOut(t, s1, "log", "-1", "--format=%s") != "late" {
		t.Error("s1 was reset over its new commit")
	}
}

// A line whose tip moves after the run read it is refused under the lock,
// before its rebase: nothing is pinned and no layer moves.
func TestSyncRebaseStackRefusesALineThatMovedAfterItWasRead(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	tmp := s.Paths[gittest.StackTmp]
	opts := synccmdtest.NoAgents()
	opts.Stack = &synccmd.StackOptions{BeforeRebase: func() {
		if err := os.WriteFile(filepath.Join(tmp, "late.txt"), []byte("late\n"), 0o644); err != nil {
			t.Error(err)
		}
		testkit.GitIn(t, tmp, "add", "-A")
		testkit.GitIn(t, tmp, "commit", "-q", "--no-verify", "-m", "late")
	}}
	_, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts)
	if err == nil || !strings.Contains(human, "moved since") {
		t.Fatalf("err %v\n%s", err, human)
	}
	main := ctx.Repo.MainRoot
	if testkit.GitOut(t, tmp, "log", "-1", "--format=%s") != "late" {
		t.Error("the line was rebased")
	}
	for _, b := range s.Layers {
		if testkit.GitOut(t, main, "rev-parse", b) != s.Tips[b] {
			t.Errorf("%s moved", b)
		}
	}
	if refs := testkit.GitOut(t, main, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Errorf("a safety ref was written: %s", refs)
	}
}

// A session that became busy in a layer's worktree during the run holds the
// layer: the sessions are listed again before the moves.
func TestSyncRebaseStackListsTheSessionsAgainBeforeTheMoves(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	opts := synccmdtest.NoAgents()
	opts.Relist = func() ([]agents.Agent, error) {
		return []agents.Agent{testkit.CodexSession(s.Paths["s1"], "busy")}, nil
	}
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts)
	if err == nil {
		t.Fatalf("nothing held:\n%s", human)
	}
	l := layersOf(synccmdtest.Participant(t, res, gittest.StackTmp).Stack)["s1"]
	if str(l.Result) != wtsync.MoveHeld || !strings.Contains(str(l.Held), "busy") {
		t.Fatalf("s1 %+v\n%s", l, human)
	}
	if testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", "s1") != s.Tips["s1"] {
		t.Error("s1 moved under a busy session")
	}
}

// A plan that cannot be recorded stops the run: the stop that would have
// been handed over is put back, so no handover is left with no plan.
func TestSyncRebaseStackStopsWhenItsPlanCannotBeRecorded(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "conflicts: []\n")
	dir := filepath.Join(ctx.Repo.MainRoot, ".git", "wt-sync-stack")
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	opts := synccmdtest.NoAgents()
	opts.Stack = &synccmd.StackOptions{BeforeRebase: func() {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Error(err)
		}
	}}
	_, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts)
	if err == nil {
		t.Fatalf("the run went on:\n%s", human)
	}
	tmp := s.Paths[gittest.StackTmp]
	if busy, _ := wtsync.RebaseInProgress(tmp); busy {
		t.Fatalf("the line was left mid-rebase:\n%s", human)
	}
	if testkit.GitOut(t, tmp, "rev-parse", "HEAD") != s.Tips[gittest.StackTmp] {
		t.Error("the line was not put back")
	}
	// The record could not be removed either: it is said, and the safety
	// ref that lets undo find and remove it stays.
	if !strings.Contains(human, "could not be removed") {
		t.Errorf("the left record is not named:\n%s", human)
	}
	if refs := testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-sync/"); refs == "" {
		t.Error("the safety ref undo needs to find the record is gone")
	}
}

// A target worktree switched to another branch after the line was read is
// refused under the lock: the other branch is not rebased.
func TestSyncRebaseStackRefusesAWorktreeSwitchedOffTheLine(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	tmp := s.Paths[gittest.StackTmp]
	opts := synccmdtest.NoAgents()
	opts.Stack = &synccmd.StackOptions{BeforeRebase: func() { testkit.GitIn(t, tmp, "switch", "-q", "-c", "other") }}
	_, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts)
	if err == nil || !strings.Contains(human, "no longer on") {
		t.Fatalf("err %v\n%s", err, human)
	}
	main := ctx.Repo.MainRoot
	if testkit.GitOut(t, main, "rev-parse", "other") != s.Tips[gittest.StackTmp] {
		t.Error("the other branch was rebased")
	}
	if refs := testkit.GitOut(t, main, "for-each-ref", "refs/wt-sync/"); refs != "" {
		t.Errorf("a safety ref was written: %s", refs)
	}
}

// A move whose put-back fails is a layer that needs recovery: the run fails
// naming wt sync undo, and the layer keeps its pins.
func TestSyncRebaseStackReportsALayerWhosePutBackFailed(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	main := ctx.Repo.MainRoot
	late := main + "-late-s2"
	opts := synccmdtest.NoAgents()
	opts.Stack = &synccmd.StackOptions{Between: func(stage string, m wtsync.LayerMove) error {
		if m.Branch != "s2" {
			return nil
		}
		switch stage {
		case wtsync.StageBeforeMove:
			testkit.GitIn(t, main, "worktree", "add", "-q", late, "s2")
		case wtsync.StageAfterRef:
			// Somebody moves it again before the run can put it back.
			testkit.GitIn(t, main, "update-ref", "refs/heads/s2", s.Tips["s1"])
		}
		return nil
	}}
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts)
	if err == nil || !strings.Contains(human, "wt sync undo "+gittest.StackTmp) {
		t.Fatalf("err %v\n%s", err, human)
	}
	l := layersOf(synccmdtest.Participant(t, res, gittest.StackTmp).Stack)["s2"]
	if str(l.Result) != wtsync.MoveRecovery {
		t.Fatalf("s2 %+v, want needsRecovery\n%s", l, human)
	}
	if refs := testkit.GitOut(t, main, "for-each-ref", "refs/wt-sync/s2/"); refs == "" {
		t.Error("the layer lost its pin")
	}
}
