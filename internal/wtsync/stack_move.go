package wtsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// What holds a layer's worktree, as LayerHolder.State says it.
const (
	HolderNone     = "none"     // the branch is in no worktree
	HolderClean    = "clean"    // in a worktree nothing holds
	HolderDirty    = "dirty"    // tracked changes
	HolderRebasing = "rebasing" // a rebase in progress
	HolderBusy     = "busy"     // an agent session is busy in it
	HolderIdle     = "idle"     // an agent session is idle in it
	HolderProcess  = "process"  // a process is working in it
	HolderUnknown  = "unknown"  // where it stands could not be read
	// HolderBisecting is a branch a bisect in another worktree started from.
	HolderBisecting = "bisecting"
)

// LayerHolder is where a layer branch is checked out and what holds it.
type LayerHolder struct {
	Branch, Path string
	State        string
	Detail       string // who or what holds it, or why it could not be read
}

// Forcible is whether --force moves a layer held this way: an idle session
// or a process, never tracked changes, a rebase or a busy session.
func (h LayerHolder) Forcible() bool { return h.State == HolderIdle || h.State == HolderProcess }

// Holds is whether a move must leave the layer where it is, unless forced.
func (h LayerHolder) Holds() bool { return h.State != HolderNone && h.State != HolderClean }

// ReadHolder reads where branch is checked out among worktrees and what
// holds it: a rebase, tracked changes, a busy session, a process working
// in it (scan; scanErr is why there is none), an idle session, in that
// order. What cannot be read holds it.
func ReadHolder(branch string, worktrees []repo.Worktree, listed []agents.Agent, scan *agents.ProcessScan, scanErr error) LayerHolder {
	h := LayerHolder{Branch: branch, State: HolderNone}
	for _, wt := range worktrees {
		if wt.Branch == branch && !wt.Detached {
			h.Path = wt.Path
			break
		}
	}
	if h.Path == "" {
		return h
	}
	gitDir, err := GitDir(h.Path)
	if err != nil {
		h.State, h.Detail = HolderUnknown, err.Error()
		return h
	}
	return holderAt(h, gitDir, listed, scan, scanErr)
}

func holderAt(h LayerHolder, gitDir string, listed []agents.Agent, scan *agents.ProcessScan, scanErr error) LayerHolder {
	st := RecheckGit(h.Path, gitDir)
	sessions := agents.SessionsAt(listed, h.Path)
	switch {
	case st.Err != nil:
		h.State, h.Detail = HolderUnknown, st.Err.Error()
	case st.Rebasing:
		h.State = HolderRebasing
	case st.Dirty:
		h.State = HolderDirty
	case len(sessions.Busy()) > 0:
		h.State, h.Detail = HolderBusy, sessions.Busy().Label(agentLabel)
	case scanErr != nil:
		h.State, h.Detail = HolderUnknown, "the processes working in it could not be read: "+scanErr.Error()
	case len(scan.In(h.Path)) > 0:
		procs := scan.In(h.Path)
		h.State, h.Detail = HolderProcess, fmt.Sprintf("pid %d %s", procs[0].PID, procs[0].Short(40))
		if len(procs) > 1 {
			h.Detail += fmt.Sprintf(" +%d", len(procs)-1)
		}
	case len(sessions) > 0:
		h.State, h.Detail = HolderIdle, sessions.Label(agentLabel)
	default:
		h.State = HolderClean
	}
	return h
}

// The ways a layer move can end, as LayerMove.Result says it.
const (
	MovePlanned   = "planned"   // recorded, not moved yet
	MoveMoved     = "moved"     // the branch is at its new tip
	MoveHeld      = "held"      // left where it was; Held says why
	MoveUnchanged = "unchanged" // its new tip is its old one
	// MoveRecovery is a move that went part way and could not be put back:
	// Held says what to look at, and wt sync undo puts it back.
	MoveRecovery = "needsRecovery"
	// MoveMoving is a move under way: recorded before the ref moves, and
	// left so by an interrupt before the worktree followed it.
	MoveMoving = "moving"
)

// The ways a layer moves.
const (
	MoveByRef      = "ref"      // a branch in no worktree: update-ref with the old tip as guard
	MoveByWorktree = "worktree" // reset --keep in the worktree that holds it
)

// LayerMove is one layer's move: where it was, where it goes, how, and what
// came of it.
type LayerMove struct {
	Branch, Old, New string
	How              string
	Path             string
	Result           string
	Held             string // why it was held
	Command          string // what moves it by hand, for a held layer
	Note             string // what else happened, for a moved layer
}

// StackRecord is what a run of a stack held on one line keeps on disk for
// its epoch, so that a resume or an undo knows the truth however the run
// ended: the line and its layers as read, the plan read from the rebase's
// list, and each layer move, recorded before it is made and again once it
// is.
type StackRecord struct {
	Epoch      int64       `json:"epoch"`
	Work       string      `json:"work"`
	Branch     string      `json:"branch"`
	Path       string      `json:"path"`
	Onto       string      `json:"onto"`
	Autosquash bool        `json:"autosquash"`
	Force      bool        `json:"force"`
	Line       Line        `json:"line"`
	Plans      []LayerPlan `json:"plans"`
	Moves      []LayerMove `json:"moves"`
}

func stackRecordPath(mainRoot string, epoch int64) (string, error) {
	common, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(common, "wt-sync-stack", strconv.FormatInt(epoch, 10)+".json"), nil
}

// WriteStackRecord writes rec in full, atomically.
func WriteStackRecord(mainRoot string, rec *StackRecord) error {
	path, err := stackRecordPath(mainRoot, rec.Epoch)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadStackRecord reads the record of the run of epoch; ok is false when
// that run kept none.
func ReadStackRecord(mainRoot string, epoch int64) (*StackRecord, bool, error) {
	path, err := stackRecordPath(mainRoot, epoch)
	if err != nil {
		return nil, false, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var rec StackRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return &rec, true, nil
}

// ForgetStack deletes what a run of a stack left for its epoch: the
// markers it planned, each only while at the commit it is read at, and its
// record. A run that kept no record leaves nothing to forget.
func ForgetStack(mainRoot string, epoch int64) error {
	rec, ok, err := ReadStackRecord(mainRoot, epoch)
	if err != nil || !ok {
		return err
	}
	if err := DropStackMap(mainRoot, rec.Plans); err != nil {
		return err
	}
	path, err := stackRecordPath(mainRoot, epoch)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// MoveOptions is what MoveLayers needs besides the tips.
type MoveOptions struct {
	Listed  []agents.Agent
	Scan    *agents.ProcessScan
	ScanErr error
	Force   bool
	Now     time.Time
	// AfterMove runs after each layer move is recorded, for a test to stop
	// the moves there as an interrupt would; an error stops them.
	AfterMove func(LayerMove) error
	// Between runs at each stage of one move, for a test to stand another
	// git operation there; an error stops the moves on the spot, as an
	// interrupt would, with nothing recorded after it. Nil in a real run.
	Between func(stage string, m LayerMove) error
}

// The stages of one move Between is called at.
const (
	// StageBeforeMove is after every check under the lock, before the ref
	// is moved.
	StageBeforeMove = "beforeMove"
	// StageAfterRef is after the branch's ref has moved, before anything
	// else of the move.
	StageAfterRef = "afterRef"
)

// MoveLayers moves every layer of rec to its checked new tip, bottom first,
// and records each move in rec before it is made and once it is. A moved
// layer gets the run's safety and result refs under rec's epoch, written
// before the move, so wt sync undo puts it back with the line; a held layer
// gets none and is no member of that undo. A layer in no worktree moves with
// update-ref, guarded by its old tip, unless it was checked out meanwhile.
// A layer in a worktree moves under that worktree's lock, with reset --keep,
// only while the worktree is on the branch at its old tip, clean, not
// mid-rebase, and held by no session or process (an idle session or a
// process are passed with Force). Anything else holds it, with the command
// that moves it by hand. An error is the record that could not be written,
// or the moves stopped by AfterMove; rec says what moved.
func MoveLayers(mainRoot string, rec *StackRecord, tips []LayerTip, opts MoveOptions) error {
	return errors.New("not yet")
}

// moveLayer makes one move and fills in what came of it.
func moveLayer(mainRoot string, epoch int64, m *LayerMove, opts MoveOptions) {
	worktrees, err := (&repo.Repo{MainRoot: mainRoot}).Worktrees()
	if err != nil {
		hold(m, "the worktrees could not be listed: "+err.Error())
		return
	}
	h := LayerHolder{Branch: m.Branch, State: HolderNone}
	for _, wt := range worktrees {
		if wt.Branch == m.Branch && !wt.Detached {
			h.Path = wt.Path
		}
	}
	if h.Path == "" {
		m.How = MoveByRef
		moveRef(mainRoot, epoch, m)
		return
	}
	m.How, m.Path = MoveByWorktree, h.Path
	gitDir, err := GitDir(h.Path)
	if err != nil {
		hold(m, err.Error())
		return
	}
	lock, err := Acquire(gitDir, opts.Now)
	if err != nil {
		hold(m, "locked: "+err.Error())
		return
	}
	defer func() { _ = lock.Release() }()
	// Read again under the lock: what the look saw may not hold any more.
	if ref, err := gitEnv(h.Path, nil, nil, "symbolic-ref", "--quiet", "HEAD"); err != nil || ref != "refs/heads/"+m.Branch {
		hold(m, "the worktree is no longer on "+m.Branch)
		return
	}
	if tip, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", "refs/heads/"+m.Branch); err != nil {
		hold(m, "its tip could not be read: "+err.Error())
		return
	} else if tip != m.Old {
		hold(m, fmt.Sprintf("it moved during the run, to %s (%s commits beyond its old tip)", git.ShortID(tip, 7), aheadCount(mainRoot, m.Old, tip)))
		return
	}
	h = holderAt(h, gitDir, opts.Listed, opts.Scan, opts.ScanErr)
	if h.Holds() && !(opts.Force && h.Forcible()) {
		why := h.State
		if h.Detail != "" {
			why += ": " + h.Detail
		}
		if h.Forcible() {
			why += " (--force moves it)"
		}
		hold(m, why)
		return
	}
	if err := WriteRun(mainRoot, epoch, []Pin{{Branch: m.Branch, Safety: m.Old, Result: m.New}}); err != nil {
		hold(m, err.Error())
		return
	}
	if _, err := gitEnv(h.Path, rebaseEnv, nil, "reset", "--quiet", "--keep", m.New); err != nil {
		unpin(mainRoot, epoch, m)
		hold(m, "reset --keep failed: "+err.Error())
		return
	}
	m.Result = MoveMoved
}

// moveRef moves a branch in no worktree, guarded by its old tip.
func moveRef(mainRoot string, epoch int64, m *LayerMove) {
	if err := WriteRun(mainRoot, epoch, []Pin{{Branch: m.Branch, Safety: m.Old, Result: m.New}}); err != nil {
		hold(m, err.Error())
		return
	}
	if _, err := gitEnv(mainRoot, nil, nil, "update-ref", "-m", "wt sync rebase --stack", "refs/heads/"+m.Branch, m.New, m.Old); err != nil {
		unpin(mainRoot, epoch, m)
		hold(m, "update-ref refused: "+strings.TrimSpace(err.Error()))
		return
	}
	m.Result = MoveMoved
}

// unpin drops the refs a move wrote before it failed: the branch never moved.
func unpin(mainRoot string, epoch int64, m *LayerMove) {
	_ = DeleteSafety(mainRoot, Safety{Branch: m.Branch, Epoch: epoch, Ref: SafetyRef(m.Branch, epoch), Tip: m.Old})
}

func hold(m *LayerMove, why string) {
	m.Result, m.Held = MoveHeld, why
	if m.How == MoveByWorktree {
		m.Command = fmt.Sprintf("git -C %s reset --keep %s", shellQuote(m.Path), m.New)
		return
	}
	m.Command = fmt.Sprintf("git update-ref refs/heads/%s %s %s", m.Branch, m.New, m.Old)
}
