package syncrun

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/jsonout"
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
	// NoPushReason is why a rebased branch has no push command, and
	// FixCommand what settles it, when a command does.
	NoPushReason *string  `json:"noPushReason"`
	FixCommand   []string `json:"fixCommand"`
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

// RunResult is the one object wt sync rebase, resume and undo print with
// --json.
type RunResult struct {
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
	*jsonout.Journal[RunResult]
	// up writes wt up's object; otherwise the journal writes wt sync's,
	// which reports more of each participant.
	up   bool
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
	j := &RunJournal{byBr: map[string]*SyncParticipant{}}
	j.Journal = jsonout.NewJournal(out, RunResult{Schema: 1, SchemaVersion: schema.VersionOf(schemaName), Command: command,
		Worktrees: []*SyncParticipant{}},
		jsonout.JournalRules[RunResult]{Error: func(r *RunResult, err error) { r.Error = jsonout.Strp(err.Error()) },
			Caught: j.caught, Close: j.close})
	return j
}

// Fail records an error that stopped the run before it began, and writes
// the object.
func (j *RunJournal) Fail(err error) {
	if j != nil {
		j.Journal.Fail(err)
	}
}

// Finish writes the object for a run that returned.
func (j *RunJournal) Finish() {
	if j != nil {
		j.Journal.Finish()
	}
}

// Interrupted writes the object for a run a signal ended.
func (j *RunJournal) Interrupted(inFlight, recovery string) {
	if j != nil {
		j.Journal.Interrupted(inFlight, recovery)
	}
}

// Repo records the repository the run is in: its main checkout.
func (j *RunJournal) Repo(name string) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	j.Res.Repo = jsonout.Strp(name)
}

// Trunk records the run's trunk, and how it was found, under one lock: an
// interrupt between the two would print a trunk without its source.
func (j *RunJournal) Trunk(name string, source *string, ref, onto string, fetched bool) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	j.Res.Trunk, j.Res.TrunkSource, j.Res.TrunkRef = jsonout.Strp(name), source, jsonout.Strp(ref)
	j.Res.Onto, j.Res.Fetched = jsonout.Strp(onto), fetched
}

// TrunkSync records what the run did to local trunk after its fetch.
func (j *RunJournal) TrunkSync(ts *TrunkSync) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	j.Res.TrunkSync = ts
}

// Join records the participants, parents first, before any is touched: each
// starts as not run, which is what a run that stops before reaching it
// leaves it as.
func (j *RunJournal) Join(work, branch, path, before string, own *OwnRemoteSync) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	p := &SyncParticipant{UpParticipant: UpParticipant{Work: work, Branch: branch, Path: path, Result: ResultNotRun,
		Before: jsonout.Strp(before), After: jsonout.Strp(before), FailedSteps: []string{}, OwnRemoteSync: own}, Deferred: []DeferredStep{}}
	j.Res.Worktrees = append(j.Res.Worktrees, p)
	j.byBr[branch] = p
}

// OnSignal has a signal ask changed which participants still recorded as
// not run it caught changed; those are interrupted.
func (j *RunJournal) OnSignal(changed func(p *SyncParticipant) bool) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	j.changed = changed
}

// Has reports whether branch has joined the run.
func (j *RunJournal) Has(branch string) bool {
	j.Lock()
	defer j.Unlock()
	return j.byBr[branch] != nil
}

// Set records what one participant came to.
func (j *RunJournal) Set(branch string, edit func(p *UpParticipant)) {
	j.SetSync(branch, func(p *SyncParticipant) { edit(&p.UpParticipant) })
}

// SetSync records what one participant came to, the parts only wt sync
// reports included.
func (j *RunJournal) SetSync(branch string, edit func(p *SyncParticipant)) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	if p := j.byBr[branch]; p != nil {
		edit(p)
	}
}

// FailWith records an error that ended the run before any participant was
// touched: the configuration, the fetch, an --expect that no longer holds.
func (j *RunJournal) FailWith(err error) {
	if j == nil {
		return
	}
	j.RunError(err)
}

// untouched reports that no participant got past not run: the run stopped
// before it touched any, or chose none.
func (j *RunJournal) untouched() bool {
	j.Lock()
	defer j.Unlock()
	for _, p := range j.Res.Worktrees {
		if p.Result != ResultNotRun {
			return false
		}
	}
	return true
}

// caught marks interrupted the participant in flight, the branch or the
// work the handler names, and any still recorded as not run that changed
// says the signal caught changed; the rest stand as recorded.
func (j *RunJournal) caught(r *RunResult, inFlight, recovery string) {
	for _, p := range r.Worktrees {
		caught := p.Branch == inFlight || p.Work == inFlight
		if !caught && p.Result == ResultNotRun && j.changed != nil {
			caught = j.changed(p)
		}
		if caught {
			p.Result = ResultInterrupted
			p.Recovery = jsonout.Strp(recovery)
			j.fastForwardSeen(p)
		}
	}
}

// close is the object: the outcome follows from the participants, by the
// rules in docs/json.md, or is interrupted.
func (j *RunJournal) close(r *RunResult, signalled bool, recovery string) any {
	if signalled {
		r.Outcome = jsonout.OutcomeInterrupted
		r.Recovery = jsonout.Strp(recovery)
	} else {
		r.Outcome = OutcomeOf(r.Worktrees, r.Error != nil)
	}
	if !j.up {
		return r
	}
	up := UpResult{Schema: r.Schema, SchemaVersion: r.SchemaVersion, Command: r.Command,
		Trunk: r.Trunk, TrunkSource: r.TrunkSource, TrunkRef: r.TrunkRef, Onto: r.Onto, Fetched: r.Fetched,
		TrunkSync: r.TrunkSync, Outcome: r.Outcome, Error: r.Error, Worktrees: []*UpParticipant{}, Recovery: r.Recovery}
	for _, p := range r.Worktrees {
		up.Worktrees = append(up.Worktrees, &p.UpParticipant)
	}
	return up
}

// fastForwardSeen reads where a participant a signal caught is, when the
// run was about to fast-forward it to its own remote: git moves the branch
// before the merge returns, so the signal can land with the branch at the
// remote's commit and the run not yet having said so. Called with the lock
// held.
func (j *RunJournal) fastForwardSeen(p *SyncParticipant) {
	o := p.OwnRemoteSync
	if o == nil || o.FastForwarded || o.Remote == nil || o.State != string(wtsync.OwnBehind) || j.Res.Repo == nil {
		return
	}
	if tip, err := git.Run(*j.Res.Repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+p.Branch); err == nil && tip == *o.Remote {
		o.FastForwarded, p.After = true, jsonout.Strp(tip)
	}
}

// Journaled runs a verb that reports to j, when there is one: j is the one
// a signal finishes while the verb runs, an error before any participant was
// touched is the run's own, and the object is written once the verb
// returns.
func Journaled(j *RunJournal, verb func() error) error {
	if j == nil {
		return verb()
	}
	defer kit.SetInterruptJournal(j)()
	err := verb()
	// An error after a participant was touched is in the participants
	// already.
	if err != nil && j.untouched() {
		j.FailWith(err)
	}
	j.Finish()
	return err
}

// OutcomeOf is the rule: done when every participant is rebased,
// fast-forwarded, undone or skipped; refused when nothing changed (every one skipped, refused, not run
// or restored, or none chosen); partial otherwise.
func OutcomeOf(ps []*SyncParticipant, failedEarly bool) string {
	if len(ps) == 0 {
		if failedEarly {
			return jsonout.OutcomeRefused
		}
		return jsonout.OutcomeDone
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
		return jsonout.OutcomeDone
	case !changed:
		return jsonout.OutcomeRefused
	}
	return jsonout.OutcomePartial
}

// PlanToken names the inputs a plan was computed from: the trunk wt up
// resolves, the configuration file it reads, and the stack's branches in
// order. A newer trunk commit is not in it; a different trunk name,
// configuration or stack is. Nor is the commit of a branch's own remote,
// except for a branch that has diverged from it: diverged maps each such
// branch to that commit, so that a caller who allows the divergence it was
// shown is refused over one that moved since. With nothing diverged the
// token is what it was before wt looked at the remotes, which docs/json.md
// promises: so it is built by hand, not with jsonout.Token.
func PlanToken(ctx *kit.Context, trunk string, stack []string, diverged map[string]string) string {
	h := sha256.New()
	h.Write([]byte("wt-plan-1\x00" + trunk + "\x00"))
	h.Write(kit.ConfigFingerprint(ctx))
	h.Write([]byte("\x00" + strings.Join(stack, "\x00")))
	for _, b := range stack {
		if commit, ok := diverged[b]; ok {
			h.Write([]byte("\x00diverged\x01" + b + "\x01" + commit))
		}
	}
	return "1:" + hex.EncodeToString(h.Sum(nil))[:32]
}
