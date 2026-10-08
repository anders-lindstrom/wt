package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/wtsync"
	"github.com/anders-lindstrom/wt/schema"
)

// The results a participant of a run can come to, for --json. The set is
// exhaustive: every participant ends as exactly one of these.
const (
	// ResultRebased is rebased onto trunk, every step after it done.
	ResultRebased = "rebased"
	// ResultRebasedStepFailed is rebased — the branch moved — but a step
	// after the rebase did not finish: a deferred step, the result ref, the
	// plan's cleanup. FailedSteps names them.
	ResultRebasedStepFailed = "rebasedStepFailed"
	// ResultFastForwarded is moved, not rebased: the branch was behind its
	// own remote, whose commit is on trunk already or has nothing of its own,
	// so the run fast-forwarded the branch there and had nothing to rebase.
	ResultFastForwarded = "fastForwarded"
	// ResultSkipped is nothing to do: on trunk already, or nothing of its own.
	ResultSkipped = "skipped"
	// ResultRefused is refused before anything was touched, for a reason of
	// its own: dirt, a session, a conflict that would be yours.
	ResultRefused = "refused"
	// ResultNotRun is not touched because of another member of its stack:
	// one refused, failed or waiting on a person.
	ResultNotRun = "notRun"
	// ResultRestored is a rebase that failed or stopped and was put back:
	// branch, index, HEAD and working tree as the run found them.
	ResultRestored = "restored"
	// ResultNeedsRecovery is a rebase that failed and could not be put back
	// by itself; Recovery says how.
	ResultNeedsRecovery = "needsRecovery"
	// ResultHandedOver is stopped at a conflict that is a person's, with a
	// plan file: wt sync resume finishes it, wt sync undo puts it back.
	ResultHandedOver = "handedOver"
	// ResultInterrupted is the participant a signal caught; Recovery says
	// how to put it back.
	ResultInterrupted = "interrupted"
	// ResultUndone is a branch wt sync undo put back at its safety ref, or
	// whose handed-over rebase it aborted. Only wt sync undo reports it.
	ResultUndone = "undone"
)

// What a deferred step came to, for wt sync --json.
const (
	// DeferredDone ran and changed nothing tracked.
	DeferredDone = "done"
	// DeferredCommitted ran and its output was committed.
	DeferredCommitted = "committed"
	// DeferredFailed ran and failed: owed, to run by hand.
	DeferredFailed = "failed"
	// DeferredSkipped did not run; Reason says why.
	DeferredSkipped = "skipped"
)

// The outcomes a whole run can come to.
const (
	// OutcomeDone is every participant rebased or skipped.
	OutcomeDone = "done"
	// OutcomeRefused is nothing changed: every participant skipped, refused,
	// not run or restored, or the run stopped before choosing any (Error).
	OutcomeRefused = "refused"
	// OutcomePartial is something changed and not everything finished.
	OutcomePartial = "partial"
	// OutcomeInterrupted is a signal ended the run.
	OutcomeInterrupted = "interrupted"
)

// UpParticipant is one worktree of a run, as --json reports it.
type UpParticipant struct {
	Work        string   `json:"work"`
	Branch      string   `json:"branch"`
	Path        string   `json:"path"`
	Result      string   `json:"result"`
	Reason      *string  `json:"reason"`
	Before      *string  `json:"before"`
	After       *string  `json:"after"`
	FailedSteps []string `json:"failedSteps"`
	Recovery    *string  `json:"recovery"`
	PushCommand []string `json:"pushCommand"`
	Pushed      bool     `json:"pushed"`
	// OwnRemoteSync is nil from wt sync resume and undo, which do not check.
	OwnRemoteSync *OwnRemoteSync `json:"ownRemoteSync"`
}

// DeferredStep is one deferred step of a finished rebase.
type DeferredStep struct {
	Step   string  `json:"step"`
	Result string  `json:"result"`
	Reason *string `json:"reason"`
	Commit *string `json:"commit"`
}

// SyncParticipant is one worktree of a wt sync rebase, resume or undo: what wt
// up reports of it, and the way back.
type SyncParticipant struct {
	UpParticipant
	SafetyRef   *string        `json:"safetyRef"`
	Deferred    []DeferredStep `json:"deferred"`
	UndoCommand []string       `json:"undoCommand"`
	PlanFile    *string        `json:"planFile"`
}

// SyncRunResult is the one object wt sync rebase, resume and undo print with
// --json.
type SyncRunResult struct {
	Schema        int                `json:"schema"`
	SchemaVersion string             `json:"schemaVersion"`
	Command       string             `json:"command"`
	Repo          *string            `json:"repo"`
	Trunk         *string            `json:"trunk"`
	TrunkRef      *string            `json:"trunkRef"`
	Onto          *string            `json:"onto"`
	Fetched       bool               `json:"fetched"`
	TrunkSync     *TrunkSync         `json:"trunkSync"`
	Outcome       string             `json:"outcome"`
	Error         *string            `json:"error"`
	Worktrees     []*SyncParticipant `json:"worktrees"`
	Recovery      *string            `json:"recovery"`
	// TrunkSource is printed by wt up alone, not by sync-run.
	TrunkSource *string `json:"-"`
}

// UpResult is the one object wt up --json prints.
type UpResult struct {
	Schema        int              `json:"schema"`
	SchemaVersion string           `json:"schemaVersion"`
	Command       string           `json:"command"`
	Trunk         *string          `json:"trunk"`
	TrunkSource   *string          `json:"trunkSource"`
	TrunkRef      *string          `json:"trunkRef"`
	Onto          *string          `json:"onto"`
	Fetched       bool             `json:"fetched"`
	TrunkSync     *TrunkSync       `json:"trunkSync"`
	Outcome       string           `json:"outcome"`
	Error         *string          `json:"error"`
	Worktrees     []*UpParticipant `json:"worktrees"`
	Recovery      *string          `json:"recovery"`
}

// RunJournal collects what a run did, participant by participant, as it
// does it, so that one object can be written at the end — by the run when it
// returns, or by the signal handler when it does not. Every method is safe
// from both, and the object is written once.
type RunJournal struct {
	mu   sync.Mutex
	once sync.Once
	out  io.Writer
	// up writes wt up's object; otherwise the journal writes wt sync's,
	// which reports more of each participant.
	up   bool
	res  SyncRunResult
	byBr map[string]*SyncParticipant
	// changed tells a signal which participants still recorded as not run
	// it caught changed, for a verb that moves several with nothing in
	// flight to name: undo.
	changed func(p *SyncParticipant) bool
}

// NewRunJournal is a journal that writes wt up's object to out.
func NewRunJournal(out io.Writer) *RunJournal {
	j := newJournal(out, "up", "up")
	j.up = true
	return j
}

// NewSyncRunJournal is a journal that writes the object of a wt sync verb
// to out: command is "sync rebase", "sync resume" or "sync undo".
func NewSyncRunJournal(out io.Writer, command string) *RunJournal {
	return newJournal(out, "sync-run", command)
}

func newJournal(out io.Writer, schemaName, command string) *RunJournal {
	return &RunJournal{out: out, res: SyncRunResult{Schema: 1, SchemaVersion: schema.VersionOf(schemaName), Command: command,
		Worktrees: []*SyncParticipant{}},
		byBr: map[string]*SyncParticipant{}}
}

func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// repo records the repository the run is in: its main checkout.
func (j *RunJournal) repo(name string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.Repo = strp(name)
}

// trunk records the run's trunk, and how it was found, under one lock: an
// interrupt between the two would print a trunk without its source.
func (j *RunJournal) trunk(name string, source *string, ref, onto string, fetched bool) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.Trunk, j.res.TrunkSource, j.res.TrunkRef = strp(name), source, strp(ref)
	j.res.Onto, j.res.Fetched = strp(onto), fetched
}

// trunkSync records what the run did to local trunk after its fetch.
func (j *RunJournal) trunkSync(ts *TrunkSync) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.TrunkSync = ts
}

// join records the participants, parents first, before any is touched: each
// starts as not run, which is what a run that stops before reaching it
// leaves it as.
func (j *RunJournal) join(work, branch, path, before string, own *OwnRemoteSync) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	p := &SyncParticipant{UpParticipant: UpParticipant{Work: work, Branch: branch, Path: path, Result: ResultNotRun,
		Before: strp(before), After: strp(before), FailedSteps: []string{}, OwnRemoteSync: own}, Deferred: []DeferredStep{}}
	j.res.Worktrees = append(j.res.Worktrees, p)
	j.byBr[branch] = p
}

// onSignal has a signal ask changed which participants still recorded as
// not run it caught changed; those are interrupted.
func (j *RunJournal) onSignal(changed func(p *SyncParticipant) bool) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.changed = changed
}

// has reports whether branch has joined the run.
func (j *RunJournal) has(branch string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.byBr[branch] != nil
}

// set records what one participant came to.
func (j *RunJournal) set(branch string, edit func(p *UpParticipant)) {
	j.setSync(branch, func(p *SyncParticipant) { edit(&p.UpParticipant) })
}

// setSync records what one participant came to, the parts only wt sync
// reports included.
func (j *RunJournal) setSync(branch string, edit func(p *SyncParticipant)) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if p := j.byBr[branch]; p != nil {
		edit(p)
	}
}

// fail records an error that ended the run before any participant was
// touched: the configuration, the fetch, an --expect that no longer holds.
func (j *RunJournal) fail(err error) {
	if j == nil || err == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.Error = strp(err.Error())
}

// untouched reports that no participant got past not run: the run stopped
// before it touched any, or chose none.
func (j *RunJournal) untouched() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, p := range j.res.Worktrees {
		if p.Result != ResultNotRun {
			return false
		}
	}
	return true
}

// Fail records an error that stopped the run before any participant, and
// writes the object: what the caller does when it cannot even start one.
func (j *RunJournal) Fail(err error) {
	j.fail(err)
	j.Finish()
}

// Finish writes the object for a run that returned: the outcome follows
// from the participants, by the rules in docs/json.md.
func (j *RunJournal) Finish() {
	j.write("", false)
}

// interrupted writes the object for a run a signal ended: the participant
// in flight is interrupted, the rest stand as recorded.
func (j *RunJournal) interrupted(inFlight, recovery string) {
	j.write(inFlight, true, recovery)
}

func (j *RunJournal) write(inFlight string, signalled bool, recovery ...string) {
	if j == nil {
		return
	}
	j.once.Do(func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if signalled {
			for _, p := range j.res.Worktrees {
				caught := p.Branch == inFlight || p.Work == inFlight
				if !caught && p.Result == ResultNotRun && j.changed != nil {
					caught = j.changed(p)
				}
				if caught {
					p.Result = ResultInterrupted
					p.Recovery = strp(strings.Join(recovery, ""))
					j.fastForwardSeen(p)
				}
			}
			j.res.Outcome = OutcomeInterrupted
			j.res.Recovery = strp(strings.Join(recovery, ""))
		} else {
			j.res.Outcome = outcomeOf(j.res.Worktrees, j.res.Error != nil)
		}
		if !j.up {
			_ = writeJSON(j.out, j.res)
			return
		}
		up := UpResult{Schema: j.res.Schema, SchemaVersion: j.res.SchemaVersion, Command: j.res.Command,
			Trunk: j.res.Trunk, TrunkSource: j.res.TrunkSource, TrunkRef: j.res.TrunkRef, Onto: j.res.Onto, Fetched: j.res.Fetched,
			TrunkSync: j.res.TrunkSync, Outcome: j.res.Outcome, Error: j.res.Error, Worktrees: []*UpParticipant{}, Recovery: j.res.Recovery}
		for _, p := range j.res.Worktrees {
			up.Worktrees = append(up.Worktrees, &p.UpParticipant)
		}
		_ = writeJSON(j.out, up)
	})
}

// fastForwardSeen reads where a participant a signal caught is, when the
// run was about to fast-forward it to its own remote: git moves the branch
// before the merge returns, so the signal can land with the branch at the
// remote's commit and the run not yet having said so. Called with the lock
// held.
func (j *RunJournal) fastForwardSeen(p *SyncParticipant) {
	o := p.OwnRemoteSync
	if o == nil || o.FastForwarded || o.Remote == nil || o.State != string(wtsync.OwnBehind) || j.res.Repo == nil {
		return
	}
	if tip, err := git.Run(*j.res.Repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+p.Branch); err == nil && tip == *o.Remote {
		o.FastForwarded, p.After = true, strp(tip)
	}
}

// journaled runs a verb that reports to j, when there is one: j is the one
// a signal finishes while the verb runs, an error before any participant was
// touched is the run's own, and the object is written once the verb
// returns.
func journaled(j *RunJournal, verb func() error) error {
	if j == nil {
		return verb()
	}
	defer setInterruptJournal(j)()
	err := verb()
	// An error after a participant was touched is in the participants
	// already.
	if err != nil && j.untouched() {
		j.fail(err)
	}
	j.Finish()
	return err
}

// outcomeOf is the rule: done when every participant is rebased,
// fast-forwarded, undone or skipped; refused when nothing changed (every one skipped, refused, not run
// or restored, or none chosen); partial otherwise.
func outcomeOf(ps []*SyncParticipant, failedEarly bool) string {
	if len(ps) == 0 {
		if failedEarly {
			return OutcomeRefused
		}
		return OutcomeDone
	}
	finished, changed := true, false
	for _, p := range ps {
		switch p.Result {
		case ResultRebased, ResultFastForwarded, ResultUndone:
			changed = true
		case ResultSkipped:
		case ResultRebasedStepFailed, ResultNeedsRecovery, ResultHandedOver, ResultInterrupted:
			changed, finished = true, false
		default:
			finished = false
		}
	}
	switch {
	case finished && !failedEarly:
		return OutcomeDone
	case !changed:
		return OutcomeRefused
	}
	return OutcomePartial
}

// interruptJournal is the journal a signal must finish, if a --json run is
// under way: watchSignals hands it the worktree in flight and the recovery it
// printed.
var (
	interruptMu      sync.Mutex
	interruptJournal interruptible
)

// interruptible is a journal a signal can finish: RunJournal, SweepJournal.
type interruptible interface {
	interrupted(inFlight, recovery string)
}

func setInterruptJournal(j interruptible) func() {
	interruptMu.Lock()
	interruptJournal = j
	interruptMu.Unlock()
	return func() {
		interruptMu.Lock()
		interruptJournal = nil
		interruptMu.Unlock()
	}
}

func currentInterruptJournal() interruptible {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	return interruptJournal
}

// planToken names the inputs a plan was computed from: the trunk wt up
// resolves, the configuration file it reads, and the stack's branches in
// order. A newer trunk commit is not in it; a different trunk name,
// configuration or stack is. Nor is the commit of a branch's own remote,
// except for a branch that has diverged from it: diverged maps each such
// branch to that commit, so that a caller who allows the divergence it was
// shown is refused over one that moved since. With nothing diverged the
// token is what it was before wt looked at the remotes.
func planToken(ctx *Context, trunk string, stack []string, diverged map[string]string) string {
	h := sha256.New()
	h.Write([]byte("wt-plan-1\x00" + trunk + "\x00"))
	h.Write(configFingerprint(ctx))
	h.Write([]byte("\x00" + strings.Join(stack, "\x00")))
	for _, b := range stack {
		if commit, ok := diverged[b]; ok {
			h.Write([]byte("\x00diverged\x01" + b + "\x01" + commit))
		}
	}
	return "1:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// configFingerprint is the bytes of the configuration file ctx read, the
// worktree's own or else the main checkout's, the way loadFor finds it.
func configFingerprint(ctx *Context) []byte {
	for _, root := range []string{ctx.Repo.Root, ctx.Repo.MainRoot} {
		for _, name := range []string{"worktree.toml", "worktree.conf"} {
			if data, err := os.ReadFile(filepath.Join(root, "bin", "worktree", name)); err == nil {
				sum := sha256.Sum256(data)
				return sum[:]
			}
		}
	}
	return nil
}
