package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
}

// UpResult is the one object wt up --json prints.
type UpResult struct {
	Schema    int              `json:"schema"`
	Command   string           `json:"command"`
	Trunk     *string          `json:"trunk"`
	TrunkRef  *string          `json:"trunkRef"`
	Onto      *string          `json:"onto"`
	Fetched   bool             `json:"fetched"`
	Outcome   string           `json:"outcome"`
	Error     *string          `json:"error"`
	Worktrees []*UpParticipant `json:"worktrees"`
	Recovery  *string          `json:"recovery"`
}

// RunJournal collects what a run did, participant by participant, as it
// does it, so that one object can be written at the end — by the run when it
// returns, or by the signal handler when it does not. Every method is safe
// from both, and the object is written once.
type RunJournal struct {
	mu   sync.Mutex
	once sync.Once
	out  io.Writer
	res  UpResult
	byBr map[string]*UpParticipant
}

// NewRunJournal is a journal that writes its object to out.
func NewRunJournal(out io.Writer) *RunJournal {
	return &RunJournal{out: out, res: UpResult{Schema: 1, Command: "up", Worktrees: []*UpParticipant{}},
		byBr: map[string]*UpParticipant{}}
}

func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (j *RunJournal) trunk(name, ref, onto string, fetched bool) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.Trunk, j.res.TrunkRef, j.res.Onto, j.res.Fetched = strp(name), strp(ref), strp(onto), fetched
}

// join records the participants, parents first, before any is touched: each
// starts as not run, which is what a run that stops before reaching it
// leaves it as.
func (j *RunJournal) join(work, branch, path, before string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	p := &UpParticipant{Work: work, Branch: branch, Path: path, Result: ResultNotRun,
		Before: strp(before), After: strp(before), FailedSteps: []string{}}
	j.res.Worktrees = append(j.res.Worktrees, p)
	j.byBr[branch] = p
}

// set records what one participant came to.
func (j *RunJournal) set(branch string, edit func(p *UpParticipant)) {
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
				if p.Branch == inFlight || p.Work == inFlight {
					p.Result = ResultInterrupted
					p.Recovery = strp(strings.Join(recovery, ""))
				}
			}
			j.res.Outcome = OutcomeInterrupted
			j.res.Recovery = strp(strings.Join(recovery, ""))
		} else {
			j.res.Outcome = outcomeOf(j.res.Worktrees, j.res.Error != nil)
		}
		enc := json.NewEncoder(j.out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(j.res)
	})
}

// outcomeOf is the rule: done when every participant is rebased or skipped;
// refused when nothing changed (every one skipped, refused, not run or
// restored, or none chosen); partial otherwise.
func outcomeOf(ps []*UpParticipant, failedEarly bool) string {
	if len(ps) == 0 {
		if failedEarly {
			return OutcomeRefused
		}
		return OutcomeDone
	}
	finished, changed := true, false
	for _, p := range ps {
		switch p.Result {
		case ResultRebased:
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
// under way: watchSignals hands it the recovery it printed.
var (
	interruptMu      sync.Mutex
	interruptJournal *RunJournal
)

func setInterruptJournal(j *RunJournal) func() {
	interruptMu.Lock()
	interruptJournal = j
	interruptMu.Unlock()
	return func() {
		interruptMu.Lock()
		interruptJournal = nil
		interruptMu.Unlock()
	}
}

func currentInterruptJournal() *RunJournal {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	return interruptJournal
}

// planToken names the inputs a plan was computed from: the trunk wt up
// resolves, the configuration file it reads, and the stack's branches in
// order. A newer trunk commit is not in it; a different trunk name,
// configuration or stack is.
func planToken(ctx *Context, trunk string, stack []string) string {
	h := sha256.New()
	h.Write([]byte("wt-plan-1\x00" + trunk + "\x00"))
	h.Write(configFingerprint(ctx))
	h.Write([]byte("\x00" + strings.Join(stack, "\x00")))
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
