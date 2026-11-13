package syncfix

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/anlindstrom/wt/internal/commands/synccmd"
	"github.com/anlindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
	"github.com/anlindstrom/wt/internal/commands/testkit"
	"github.com/anlindstrom/wt/internal/gittest"
	"github.com/anlindstrom/wt/internal/wtsync"
)

func stackLayers(s *syncrun.Stack) map[string]syncrun.StackLayer {
	out := map[string]syncrun.StackLayer{}
	if s != nil {
		for _, l := range s.Layers {
			out[l.Branch] = l
		}
	}
	return out
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// undoBack checks every branch of s is back where the fixture left it.
func undoBack(t *testing.T, main string, s *gittest.StackLine, branches ...string) {
	t.Helper()
	for _, b := range branches {
		if got := testkit.GitOut(t, main, "rev-parse", b); got != s.Tips[b] {
			t.Errorf("%s is at %s after the undo, want %s", b, got, s.Tips[b])
		}
	}
}

// wt sync undo <tmp> puts back the line, every layer it moved and the clean
// worktree with its branch, while the held worktrees are still dirty and in
// use.
func TestSyncUndoPutsBackAStackRunWhileItsHeldLayersAreInUse(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	if _, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, synccmdtest.NoAgents()); err == nil {
		t.Fatalf("the run held nothing:\n%s", human)
	}
	main := ctx.Repo.MainRoot
	var out bytes.Buffer
	if err := SyncUndo(ctx, gittest.StackTmp, noAgentsUndo(), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	undoBack(t, main, s, append([]string{gittest.StackTmp}, s.Layers...)...)
	if head := testkit.GitOut(t, s.Paths["s1"], "rev-parse", "HEAD"); head != s.Tips["s1"] {
		t.Error("s1's worktree was not put back with its branch")
	}
	if testkit.GitOut(t, s.Paths["s3"], "status", "--porcelain", "--untracked-files=no") == "" {
		t.Error("s3's changes are gone")
	}
	if err := syscall.Kill(s.Process, 0); err != nil {
		t.Errorf("the process in s5 is gone: %v", err)
	}
	if refs := testkit.GitOut(t, main, "for-each-ref", wtsync.StackMapPrefix); refs != "" {
		t.Errorf("markers left: %s", refs)
	}
}

// A conflict a person owns in the middle of the line is handed over, the
// layers wait, and wt sync resume finishes the rebase and then moves them.
func TestSyncResumeFinishesAStackAndThenMovesItsLayers(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	// Nothing declared for v.txt: s3's bump is a conflict that is yours.
	ctx, s := synccmdtest.StackLine(t, "conflicts: []\n")
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, synccmdtest.NoAgents())
	if err == nil || !strings.Contains(err.Error(), "needs you") {
		t.Fatalf("err %v\n%s", err, human)
	}
	if p := synccmdtest.Participant(t, res, gittest.StackTmp); p.Result != syncrun.ResultHandedOver {
		t.Fatalf("line %+v\n%s", p, human)
	}
	main := ctx.Repo.MainRoot
	undoBack(t, main, s, s.Layers...)
	tmp := s.Paths[gittest.StackTmp]
	if err := os.WriteFile(filepath.Join(tmp, "v.txt"), []byte("1.0.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, tmp, "add", "v.txt")
	opts := noResumeAgents()
	opts.Push = syncrun.PushNever
	resumed, err := syncResumeJSON(t, ctx, gittest.StackTmp, opts)
	if err == nil || !strings.Contains(err.Error(), "2 layers held") {
		t.Fatalf("err %v, want the resume to say two layers were held", err)
	}
	layers := stackLayers(synccmdtest.Participant(t, resumed, gittest.StackTmp).Stack)
	for _, b := range []string{"s1", "s2", "s4"} {
		l := layers[b]
		if deref(l.Result) != wtsync.MoveMoved || testkit.GitOut(t, main, "rev-parse", b) != deref(l.NewTip) {
			t.Errorf("%s %+v", b, l)
		}
	}
	if got := testkit.GitOut(t, main, "show", deref(layers["s4"].NewTip)+":v.txt"); got != "1.0.6" {
		t.Errorf("s4's new tip has v.txt %s, want the resolution", got)
	}
}

// Moves stopped between two layers, as an interrupt stops them, leave the
// record on disk saying what moved, and undo puts back the line and that
// layer.
func TestSyncUndoPutsBackALayerMoveStoppedPartWay(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "")
	opts := synccmdtest.NoAgents()
	opts.Stack = &synccmd.StackOptions{AfterMove: func(m wtsync.LayerMove) error {
		if m.Branch == "s1" {
			return errors.New("interrupted")
		}
		return nil
	}}
	_, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts)
	if err == nil || !strings.Contains(err.Error(), "layer moves stopped") {
		t.Fatalf("err %v\n%s", err, human)
	}
	main := ctx.Repo.MainRoot
	rec, ok, rerr := wtsync.ReadStackRecord(main, 99)
	if rerr != nil || !ok {
		t.Fatalf("no record: %v", rerr)
	}
	results := map[string]string{}
	for _, m := range rec.Moves {
		results[m.Branch] = m.Result
	}
	if results["s1"] != wtsync.MoveMoved || results["s2"] != wtsync.MovePlanned {
		t.Fatalf("record %+v", rec.Moves)
	}
	var out bytes.Buffer
	if err := SyncUndo(ctx, gittest.StackTmp, noAgentsUndo(), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	undoBack(t, main, s, append([]string{gittest.StackTmp}, s.Layers...)...)
	if _, ok, _ := wtsync.ReadStackRecord(main, 99); ok {
		t.Error("the record outlived the undo")
	}
}

// A record with no plan is refused before the resume moves anything, and
// the refusal says how to put the run back.
func TestSyncResumeRefusesAStackRecordWithNoPlan(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "conflicts: []\n")
	if _, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, synccmdtest.NoAgents()); err == nil {
		t.Fatalf("no handover:\n%s", human)
	}
	main := ctx.Repo.MainRoot
	rec, ok, err := wtsync.ReadStackRecord(main, 99)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	rec.Plans = nil
	if err := wtsync.WriteStackRecord(main, rec); err != nil {
		t.Fatal(err)
	}
	tmp := s.Paths[gittest.StackTmp]
	if err := os.WriteFile(filepath.Join(tmp, "v.txt"), []byte("1.0.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, tmp, "add", "v.txt")
	var out bytes.Buffer
	err = SyncResume(ctx, gittest.StackTmp, noResumeAgents(), &out)
	if err == nil || !strings.Contains(err.Error(), "wt sync undo") {
		t.Fatalf("err %v, want a refusal naming the undo\n%s", err, out.String())
	}
	if busy, _ := wtsync.RebaseInProgress(tmp); !busy {
		t.Error("the resume went on with the rebase")
	}
	if _, ok, _ := wtsync.ReadStackRecord(main, 99); !ok {
		t.Error("the record was forgotten")
	}
	undoBack(t, main, s, s.Layers...)
	// The command it names works: undo aborts the rebase and puts the line
	// back.
	out.Reset()
	if err := SyncUndo(ctx, gittest.StackTmp, noAgentsUndo(), &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	undoBack(t, main, s, gittest.StackTmp)
	if _, ok, _ := wtsync.ReadStackRecord(main, 99); ok {
		t.Error("the record outlived the undo")
	}
}

// A record with no plan, handed over and finished by hand with git: resume
// refuses and names undo, and undo puts the line back and forgets it.
func TestSyncUndoPutsBackANoPlanLineFinishedByHand(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "conflicts: []\n")
	if _, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, synccmdtest.NoAgents()); err == nil {
		t.Fatalf("no handover:\n%s", human)
	}
	main := ctx.Repo.MainRoot
	rec, ok, err := wtsync.ReadStackRecord(main, 99)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	rec.Plans = nil
	if err := wtsync.WriteStackRecord(main, rec); err != nil {
		t.Fatal(err)
	}
	tmp := s.Paths[gittest.StackTmp]
	if err := os.WriteFile(filepath.Join(tmp, "v.txt"), []byte("1.0.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, tmp, "add", "v.txt")
	testkit.GitIn(t, tmp, "-c", "core.editor=true", "rebase", "--continue")
	var out bytes.Buffer
	if err := SyncResume(ctx, gittest.StackTmp, noResumeAgents(), &out); err == nil || !strings.Contains(err.Error(), "wt sync undo") {
		t.Fatalf("err %v, want a refusal naming undo\n%s", err, out.String())
	}
	out.Reset()
	if err := SyncUndo(ctx, gittest.StackTmp, noAgentsUndo(), &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	undoBack(t, main, s, append([]string{gittest.StackTmp}, s.Layers...)...)
	if _, ok, _ := wtsync.ReadStackRecord(main, 99); ok {
		t.Error("the record outlived the undo")
	}
	if refs := testkit.GitOut(t, main, "for-each-ref", wtsync.StackMapPrefix); refs != "" {
		t.Errorf("markers left: %s", refs)
	}
}

// A record a run could not remove is removed by undo, which finds it
// through the safety ref the run kept.
func TestSyncUndoRemovesARecordTheRunCouldNotRemove(t *testing.T) {
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
	if _, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts); err == nil {
		t.Fatalf("the run went on:\n%s", human)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := ctx.Repo.MainRoot
	var out bytes.Buffer
	if err := SyncUndo(ctx, gittest.StackTmp, noAgentsUndo(), &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if _, ok, _ := wtsync.ReadStackRecord(main, 99); ok {
		t.Error("the record is still there")
	}
	undoBack(t, main, s, gittest.StackTmp)
}

// A fold with no plan, handed over at a fixup that conflicts and finished
// by hand with a commit of the person's own: the folded fixup makes room
// for that commit in the count, which therefore cannot show it, so undo
// keeps the tip it discards under a safety ref, and says so.
func TestSyncUndoKeepsTheTipOfANoPlanFoldFinishedByHand(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	ctx, s := synccmdtest.StackLine(t, "conflicts: []\n")
	tmp := s.Paths[gittest.StackTmp]
	commit := func(file, content, msg string) {
		if err := os.WriteFile(filepath.Join(tmp, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		testkit.GitIn(t, tmp, "add", file)
		testkit.GitIn(t, tmp, "commit", "-q", "--no-verify", "-m", msg)
	}
	// The fixup changes what a later commit wrote, so folding it into s1's
	// commit, before that one, conflicts.
	commit("a1.txt", "x\n", "feat: rewrite a1")
	commit("a1.txt", "y\n", "fixup! feat(s1): a1.txt")
	head := testkit.GitOut(t, tmp, "rev-parse", "HEAD")
	opts := synccmdtest.NoAgents()
	opts.Stack = &synccmd.StackOptions{Autosquash: true}
	if _, human, err := synccmdtest.StackRun(t, ctx, gittest.StackTmp, opts); err == nil || !strings.Contains(err.Error(), "needs you") {
		t.Fatalf("err %v, want a handover\n%s", err, human)
	}
	main := ctx.Repo.MainRoot
	rec, ok, err := wtsync.ReadStackRecord(main, 99)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	rec.Plans = nil
	if err := wtsync.WriteStackRecord(main, rec); err != nil {
		t.Fatal(err)
	}
	// Resolved by keeping s1's own a1.txt, so the later commit applies.
	if err := os.WriteFile(filepath.Join(tmp, "a1.txt"), []byte("s1 a1.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, tmp, "add", "a1.txt")
	commit("mine.txt", "mine\n", "mine")
	mine := testkit.GitOut(t, tmp, "rev-parse", "HEAD")
	if out, err := gittest.Try(t, tmp, "rebase", "--continue"); err != nil {
		t.Fatalf("continue: %v\n%s", err, out)
	}
	var out bytes.Buffer
	if err := SyncUndo(ctx, gittest.StackTmp, noAgentsUndo(), &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	if got := testkit.GitOut(t, main, "rev-parse", gittest.StackTmp); got != head {
		t.Errorf("the line is at %s after the undo, want %s", got, head)
	}
	if !strings.Contains(out.String(), "kept under") {
		t.Errorf("undo does not say where it kept the tip:\n%s", out.String())
	}
	kept := testkit.GitOut(t, main, "for-each-ref", "--format=%(objectname)", "refs/wt-sync/tmp/stack/")
	found := false
	for _, tip := range strings.Fields(kept) {
		if _, err := gittest.Try(t, main, "merge-base", "--is-ancestor", mine, tip); err == nil {
			found = true
		}
	}
	if !found {
		t.Errorf("the commit made by hand is not reachable from a safety ref (%q)", kept)
	}
}
