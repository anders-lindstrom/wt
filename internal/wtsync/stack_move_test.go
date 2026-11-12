package wtsync

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// rebasedLine rebases s's line onto trunk, pins the line's result as a run
// does once it has finished, and returns the record and the checked tips a
// run hands MoveLayers.
func rebasedLine(t *testing.T, s *gittest.StackLine, epoch int64) (*StackRecord, []LayerTip) {
	t.Helper()
	line, res := stackRebase(t, s, epoch)
	if err := WriteResult(s.Dir, gittest.StackTmp, res.NewTip, epoch); err != nil {
		t.Fatal(err)
	}
	tips, err := CheckLayers(s.Dir, res.NewTip, "origin/main", res.Layers)
	if err != nil {
		t.Fatal(err)
	}
	rec := &StackRecord{Epoch: epoch, Work: gittest.StackTmp, Branch: gittest.StackTmp, Path: s.Paths[gittest.StackTmp],
		Onto: "origin/main", Line: line, Plans: res.Layers}
	return rec, tips
}

func moveOpts(t *testing.T) MoveOptions {
	t.Helper()
	scan, err := agents.ScanHolders(nil)
	return MoveOptions{Scan: scan, ScanErr: err, Now: time.Now()}
}

func movesOf(rec *StackRecord) map[string]LayerMove {
	out := map[string]LayerMove{}
	for _, m := range rec.Moves {
		out[m.Branch] = m
	}
	return out
}

func worktreesOf(t *testing.T, dir string) []repo.Worktree {
	t.Helper()
	wts, err := (&repo.Repo{MainRoot: dir}).Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	return wts
}

// Every layer nothing holds moves to its new tip, the clean worktree with
// it; the dirty worktree and the one a process works in are held, with the
// command that moves them; and undo puts back the line and every layer it
// moved while the held ones are still dirty and in use.
func TestMoveLayersMovesWhatNothingHoldsAndUndoPutsItBack(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	rec, tips := rebasedLine(t, s, 31)
	if err := MoveLayers(s.Dir, rec, tips, moveOpts(t)); err != nil {
		t.Fatal(err)
	}
	moves := movesOf(rec)
	for i, b := range s.Layers {
		m := moves[b]
		switch b {
		case "s1":
			if m.Result != MoveMoved || m.How != MoveByWorktree || gitIn(t, s.Paths["s1"], "rev-parse", "HEAD") != tips[i].New {
				t.Errorf("s1 %+v", m)
			}
		case "s2", "s4":
			if m.Result != MoveMoved || m.How != MoveByRef || gitIn(t, s.Dir, "rev-parse", b) != tips[i].New {
				t.Errorf("%s %+v", b, m)
			}
		case "s3", "s5":
			want := map[string]string{"s3": "dirty", "s5": "process"}[b]
			if m.Result != MoveHeld || !strings.Contains(m.Held, want) || !strings.Contains(m.Command, "reset --keep "+tips[i].New) {
				t.Errorf("%s %+v, want held as %s", b, m, want)
			}
			if gitIn(t, s.Dir, "rev-parse", b) != s.Tips[b] {
				t.Errorf("%s moved", b)
			}
			if refs := gitIn(t, s.Dir, "for-each-ref", "refs/wt-sync/"+b+"/"); refs != "" {
				t.Errorf("a held layer has a safety ref: %s", refs)
			}
		}
	}
	if on, ok, err := ReadStackRecord(s.Dir, 31); err != nil || !ok || movesOf(on)["s1"].Result != MoveMoved {
		t.Fatalf("the record on disk: %+v %v %v", on, ok, err)
	}
	restored, err := Undo(s.Dir, worktreesOf(t, s.Dir), nil, gittest.StackTmp, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 4 {
		t.Errorf("restored %+v, want the line and s1, s2, s4", restored)
	}
	for _, b := range append([]string{gittest.StackTmp}, s.Layers...) {
		if got := gitIn(t, s.Dir, "rev-parse", b); got != s.Tips[b] {
			t.Errorf("%s is at %s after the undo, want %s", b, got, s.Tips[b])
		}
	}
	if gitIn(t, s.Paths["s3"], "status", "--porcelain", "--untracked-files=no") == "" {
		t.Error("s3's changes are gone")
	}
}

// Moves stopped after the first one leave a record that says what moved,
// and undo puts back the line and the one layer that moved.
func TestMoveLayersStoppedPartWayIsRecordedAndUndone(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	rec, tips := rebasedLine(t, s, 32)
	opts := moveOpts(t)
	opts.AfterMove = func(LayerMove) error { return errors.New("interrupted") }
	if err := MoveLayers(s.Dir, rec, tips, opts); err == nil {
		t.Fatal("the moves went on")
	}
	on, ok, err := ReadStackRecord(s.Dir, 32)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	moves := movesOf(on)
	if moves["s1"].Result != MoveMoved || moves["s2"].Result != MovePlanned || moves["s4"].Result != MovePlanned {
		t.Fatalf("record %+v", on.Moves)
	}
	if _, err := Undo(s.Dir, worktreesOf(t, s.Dir), nil, gittest.StackTmp, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{gittest.StackTmp, "s1", "s2"} {
		if gitIn(t, s.Dir, "rev-parse", b) != s.Tips[b] {
			t.Errorf("%s not put back", b)
		}
	}
}

// A layer that gained a commit during the run is held, not reset; --force
// moves the one only a process holds.
func TestMoveLayersHoldsALayerThatMovedAndForcePassesAProcess(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	rec, tips := rebasedLine(t, s, 33)
	gittest.WriteFile(t, filepath.Join(s.Paths["s1"], "late.txt"), "late\n")
	gitIn(t, s.Paths["s1"], "add", "-A")
	gitIn(t, s.Paths["s1"], "commit", "-q", "-m", "late")
	late := gitIn(t, s.Paths["s1"], "rev-parse", "HEAD")
	opts := moveOpts(t)
	opts.Force = true
	if err := MoveLayers(s.Dir, rec, tips, opts); err != nil {
		t.Fatal(err)
	}
	moves := movesOf(rec)
	if m := moves["s1"]; m.Result != MoveHeld || !strings.Contains(m.Held, "moved during the run") {
		t.Errorf("s1 %+v", m)
	}
	if gitIn(t, s.Paths["s1"], "rev-parse", "HEAD") != late {
		t.Error("s1 was reset over its new commit")
	}
	if m := moves["s5"]; m.Result != MoveMoved {
		t.Errorf("s5 %+v, want moved past the process", m)
	}
	if m := moves["s3"]; m.Result != MoveHeld {
		t.Errorf("s3 %+v, want held: --force never moves a dirty worktree", m)
	}
}

// A commit made in a checked-out layer after its tip was checked is not
// rewound: the move is guarded by the old tip, and the layer is held with
// no pins.
func TestMoveLayersGuardsACheckedOutLayerOnItsOldTip(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	rec, tips := rebasedLine(t, s, 34)
	opts := moveOpts(t)
	s1 := s.Paths["s1"]
	opts.Between = func(stage string, m LayerMove) error {
		if stage == StageBeforeMove && m.Branch == "s1" {
			gittest.WriteFile(t, filepath.Join(s1, "late.txt"), "late\n")
			gitIn(t, s1, "add", "-A")
			gitIn(t, s1, "commit", "-q", "-m", "late")
		}
		return nil
	}
	if err := MoveLayers(s.Dir, rec, tips, opts); err != nil {
		t.Fatal(err)
	}
	if m := movesOf(rec)["s1"]; m.Result != MoveHeld || m.Command == "" {
		t.Errorf("s1 %+v, want held with its command", m)
	}
	if got := gitIn(t, s1, "log", "-1", "--format=%s"); got != "late" {
		t.Errorf("s1's commit was rewound: HEAD is %q", got)
	}
	if refs := gitIn(t, s.Dir, "for-each-ref", "refs/wt-sync/s1/", "refs/wt-sync-result/s1/"); refs != "" {
		t.Errorf("a held layer has pins: %s", refs)
	}
}

// A branch checked out elsewhere while its ref was being moved is put back
// and held, and keeps no pins.
func TestMoveLayersPutsBackALayerCheckedOutDuringItsMove(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	rec, tips := rebasedLine(t, s, 35)
	opts := moveOpts(t)
	late := s.Dir + "-late-s2"
	opts.Between = func(stage string, m LayerMove) error {
		if stage == StageBeforeMove && m.Branch == "s2" {
			gitIn(t, s.Dir, "worktree", "add", "-q", late, "s2")
		}
		return nil
	}
	if err := MoveLayers(s.Dir, rec, tips, opts); err != nil {
		t.Fatal(err)
	}
	if m := movesOf(rec)["s2"]; m.Result != MoveHeld || !strings.Contains(m.Held, "checked out") {
		t.Errorf("s2 %+v, want held for the checkout", m)
	}
	if got := gitIn(t, s.Dir, "rev-parse", "s2"); got != s.Tips["s2"] {
		t.Errorf("s2 is at %s, want put back at %s", got, s.Tips["s2"])
	}
	if st := gitIn(t, late, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Errorf("the worktree that checked s2 out is not clean: %s", st)
	}
	if refs := gitIn(t, s.Dir, "for-each-ref", "refs/wt-sync/s2/", "refs/wt-sync-result/s2/"); refs != "" {
		t.Errorf("a held layer has pins: %s", refs)
	}
}

// A branch a bisect elsewhere started from is held, in the look and in the
// moves.
func TestMoveLayersHoldsALayerABisectHolds(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	rec, tips := rebasedLine(t, s, 36)
	b := s.Dir + "-bisect"
	gitIn(t, s.Dir, "worktree", "add", "-q", b, "s2")
	gitIn(t, b, "bisect", "start")
	gitIn(t, b, "bisect", "bad")
	gitIn(t, b, "bisect", "good", s.Base)
	if h := ReadHolder("s2", worktreesOf(t, s.Dir), nil, nil, nil); h.State != HolderBisecting {
		t.Errorf("look: s2 %+v, want %s", h, HolderBisecting)
	}
	if err := MoveLayers(s.Dir, rec, tips, moveOpts(t)); err != nil {
		t.Fatal(err)
	}
	if m := movesOf(rec)["s2"]; m.Result != MoveHeld || !strings.Contains(m.Held, "bisect") {
		t.Errorf("s2 %+v, want held by the bisect", m)
	}
	if gitIn(t, s.Dir, "rev-parse", "s2") != s.Tips["s2"] {
		t.Error("s2 moved under a bisect")
	}
}

// A branch checked out elsewhere after its ref moved shows the new tip
// there: it is left at the new tip, pinned, and reported moved with a note.
func TestMoveLayersKeepsALayerCheckedOutAtItsNewTipDuringItsMove(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	rec, tips := rebasedLine(t, s, 37)
	opts := moveOpts(t)
	late := s.Dir + "-late-s2"
	opts.Between = func(stage string, m LayerMove) error {
		if stage == StageAfterRef && m.Branch == "s2" {
			gitIn(t, s.Dir, "worktree", "add", "-q", late, "s2")
		}
		return nil
	}
	if err := MoveLayers(s.Dir, rec, tips, opts); err != nil {
		t.Fatal(err)
	}
	m := movesOf(rec)["s2"]
	if m.Result != MoveMoved || !strings.Contains(m.Note, "checked out") {
		t.Errorf("s2 %+v, want moved with a note on the checkout", m)
	}
	if got := gitIn(t, s.Dir, "rev-parse", "s2"); got != m.New {
		t.Errorf("s2 is at %s, want its new tip %s", got, m.New)
	}
	if st := gitIn(t, late, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Errorf("the worktree that checked s2 out is not clean: %s", st)
	}
	if refs := gitIn(t, s.Dir, "for-each-ref", "refs/wt-sync/s2/"); refs == "" {
		t.Error("the moved layer lost its pin")
	}
}

// A checkout during the move whose index is neither tip's tree is left as
// it is: the branch stays at its new tip, pinned, and the layer needs
// recovery.
func TestMoveLayersLeavesForRecoveryALayerCheckedOutWithAnotherIndex(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	rec, tips := rebasedLine(t, s, 38)
	opts := moveOpts(t)
	late := s.Dir + "-late-s2"
	opts.Between = func(stage string, m LayerMove) error {
		if stage == StageAfterRef && m.Branch == "s2" {
			gitIn(t, s.Dir, "worktree", "add", "-q", late, "s2")
			gittest.WriteFile(t, filepath.Join(late, "b1.txt"), "staged by hand\n")
			gitIn(t, late, "add", "b1.txt")
		}
		return nil
	}
	err := MoveLayers(s.Dir, rec, tips, opts)
	m := movesOf(rec)["s2"]
	if err == nil || m.Result != MoveRecovery || !strings.Contains(m.Held, late) {
		t.Fatalf("err %v, s2 %+v: want it left for recovery, naming %s", err, m, late)
	}
	if got := gitIn(t, s.Dir, "rev-parse", "s2"); got != m.New {
		t.Errorf("s2 is at %s, want left at its new tip", got)
	}
	if refs := gitIn(t, s.Dir, "for-each-ref", "refs/wt-sync/s2/"); refs == "" {
		t.Error("the layer lost its pin")
	}
	if on, ok, _ := ReadStackRecord(s.Dir, 38); !ok || movesOf(on)["s2"].Result != MoveRecovery {
		t.Errorf("the record does not say s2 needs recovery: %+v", on)
	}
}

// A move interrupted between the ref and the files leaves the worktree on
// the new tip with the old index: undo puts the ref back and nothing else.
func TestUndoPutsBackALayerInterruptedBetweenItsRefAndItsFiles(t *testing.T) {
	t.Parallel()
	s := stackLine(t, stackYAML)
	rec, tips := rebasedLine(t, s, 39)
	opts := moveOpts(t)
	opts.Between = func(stage string, m LayerMove) error {
		if stage == StageAfterRef && m.Branch == "s1" {
			return errors.New("interrupted")
		}
		return nil
	}
	if err := MoveLayers(s.Dir, rec, tips, opts); err == nil {
		t.Fatal("the moves went on")
	}
	on, ok, err := ReadStackRecord(s.Dir, 39)
	if err != nil || !ok || movesOf(on)["s1"].Result != MoveMoving {
		t.Fatalf("record %+v %v %v, want s1 moving", on, ok, err)
	}
	if _, err := Undo(s.Dir, worktreesOf(t, s.Dir), nil, gittest.StackTmp, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{gittest.StackTmp, "s1"} {
		if gitIn(t, s.Dir, "rev-parse", b) != s.Tips[b] {
			t.Errorf("%s not put back", b)
		}
	}
	if st := gitIn(t, s.Paths["s1"], "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Errorf("s1's worktree is not clean after the undo: %s", st)
	}
}

// interruptedAtS1 is a run whose move of s1 was cut between its ref and
// its files: s1 at its new tip over its old index, recorded as moving.
func interruptedAtS1(t *testing.T, epoch int64) (*gittest.StackLine, LayerMove) {
	t.Helper()
	s := stackLine(t, stackYAML)
	rec, tips := rebasedLine(t, s, epoch)
	opts := moveOpts(t)
	opts.Between = func(stage string, m LayerMove) error {
		if stage == StageAfterRef && m.Branch == "s1" {
			return errors.New("interrupted")
		}
		return nil
	}
	if err := MoveLayers(s.Dir, rec, tips, opts); err == nil {
		t.Fatal("the moves went on")
	}
	return s, movesOf(rec)["s1"]
}

// An undo refused by another participant changes nothing: the interrupted
// layer's ref is still where the interrupt left it.
func TestUndoRefusedChangesNoRefOfAnInterruptedMove(t *testing.T) {
	t.Parallel()
	s, m := interruptedAtS1(t, 51)
	gittest.WriteFile(t, filepath.Join(s.Paths[gittest.StackTmp], "t.txt"), "dirty\n")
	if _, err := Undo(s.Dir, worktreesOf(t, s.Dir), nil, gittest.StackTmp, time.Now(), false); err == nil {
		t.Fatal("undo went over a dirty worktree")
	}
	if got := gitIn(t, s.Dir, "rev-parse", "s1"); got != m.New {
		t.Errorf("s1 is at %s after a refused undo, want it left at %s", got, m.New)
	}
}

// An interrupted layer checked out somewhere other than the worktree the
// record names is refused, with its pins kept and that worktree named.
func TestUndoRefusesAnInterruptedMoveCheckedOutElsewhere(t *testing.T) {
	t.Parallel()
	s, m := interruptedAtS1(t, 52)
	gitIn(t, s.Paths["s1"], "checkout", "-q", "--detach")
	other := s.Dir + "-other-s1"
	gitIn(t, s.Dir, "worktree", "add", "-q", other, "s1")
	_, err := Undo(s.Dir, worktreesOf(t, s.Dir), nil, gittest.StackTmp, time.Now(), false)
	if err == nil || !strings.Contains(err.Error(), other) {
		t.Fatalf("err %v, want a refusal naming %s", err, other)
	}
	if got := gitIn(t, s.Dir, "rev-parse", "s1"); got != m.New {
		t.Errorf("s1 is at %s, want left at %s", got, m.New)
	}
	if refs := gitIn(t, s.Dir, "for-each-ref", "refs/wt-sync/s1/"); refs == "" {
		t.Error("s1 lost its pin")
	}
	if st := gitIn(t, other, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Errorf("the other worktree was touched: %s", st)
	}
}
