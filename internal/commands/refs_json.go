package commands

import (
	"io"
	"strings"
	"sync"
	"time"

	"github.com/anders-lindstrom/wt/schema"
)

const refsSweepCommand = "refs sweep"

// RefItem is one row of wt refs sweep's plan, for --json.
type RefItem struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Pattern       string   `json:"pattern"`
	Category      string   `json:"category"`
	Selected      bool     `json:"selected"`
	Tip           *string  `json:"tip"`
	Object        string   `json:"object"`
	Annotated     *bool    `json:"annotated"`
	ContainedIn   *string  `json:"containedIn"`
	UniqueCommits *int     `json:"uniqueCommits"`
	Date          *int64   `json:"date"`
	DateSource    *string  `json:"dateSource"`
	Kept          []string `json:"kept"`
	Subject       *string  `json:"subject"`
}

// RefsPlanOutput is the one object wt refs sweep --dry-run --json prints.
type RefsPlanOutput struct {
	Schema        int          `json:"schema"`
	SchemaVersion string       `json:"schemaVersion"`
	Command       string       `json:"command"`
	Repo          *string      `json:"repo"`
	Trunk         *string      `json:"trunk"`
	TrunkSource   *string      `json:"trunkSource"`
	Fetched       bool         `json:"fetched"`
	Remote        bool         `json:"remote"`
	Patterns      []string     `json:"patterns"`
	Age           *string      `json:"age"`
	Now           int64        `json:"now"`
	Endpoint      *Endpoint    `json:"endpoint"`
	RunID         *string      `json:"runId"`
	Token         *string      `json:"token"`
	Error         *string      `json:"error"`
	Problems      []RefProblem `json:"problems"`
	Items         []RefItem    `json:"items"`
}

// RefResultItem is what became of one row, for --json.
type RefResultItem struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	Selected       bool     `json:"selected"`
	Tip            *string  `json:"tip"`
	Object         string   `json:"object"`
	Result         string   `json:"result"`
	Reason         *string  `json:"reason"`
	Pin            *string  `json:"pin"`
	Pinned         bool     `json:"pinned"`
	Deleted        bool     `json:"deleted"`
	RestoreCommand []string `json:"restoreCommand"`
}

// RefsResult is the one object wt refs sweep --yes --json prints.
type RefsResult struct {
	Schema        int              `json:"schema"`
	SchemaVersion string           `json:"schemaVersion"`
	Command       string           `json:"command"`
	Repo          *string          `json:"repo"`
	Trunk         *string          `json:"trunk"`
	Fetched       bool             `json:"fetched"`
	Remote        bool             `json:"remote"`
	Token         *string          `json:"token"`
	RunID         *string          `json:"runId"`
	Outcome       string           `json:"outcome"`
	Error         *string          `json:"error"`
	Problems      []RefProblem     `json:"problems"`
	Recovery      *string          `json:"recovery"`
	Items         []*RefResultItem `json:"items"`
}

func refItem(r RefRow) RefItem {
	it := RefItem{ID: r.ID, Kind: r.Kind, Name: r.Name, Pattern: r.Pattern, Category: r.Category,
		Selected: r.Selected, Tip: strp(r.Tip), Object: r.Object, Annotated: r.Annotated,
		ContainedIn: strp(r.ContainedIn), UniqueCommits: r.Unique, DateSource: strp(r.DateSource),
		Kept: append([]string{}, r.Kept...), Subject: strp(r.Subject)}
	if r.DateSource != "" {
		d := r.Date
		it.Date = &d
	}
	return it
}

func newRefsPlanOutput() RefsPlanOutput {
	return RefsPlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("refs-sweep-plan"), Command: refsSweepCommand,
		Patterns: []string{}, Problems: []RefProblem{}, Items: []RefItem{}}
}

// RefsSweepPlanJSON writes wt refs sweep --dry-run --json: every backup the
// patterns match, what it is and whether it would go, and the token that
// holds a later sweep to this plan. It fetches unless told not to, and moves
// nothing. A plan that cannot be made is still one object, with error set,
// and the error is returned too.
func RefsSweepPlanJSON(ctx *Context, opts RefsOptions, out, progress io.Writer) error {
	o := newRefsPlanOutput()
	if ctx == nil {
		return writeRefsPlan(out, o, ErrNotInRepo)
	}
	plan, err := planRefs(ctx, opts, progress)
	o.Repo, o.Trunk, o.TrunkSource = strp(plan.Repo), strp(plan.Trunk), plan.TrunkSource
	o.Fetched, o.Remote, o.Age, o.Now = plan.Fetched, plan.Remote, strp(plan.Age), plan.Now
	o.Patterns = append(o.Patterns, plan.Patterns...)
	o.Endpoint, o.Problems = plan.Endpoint, append(o.Problems, plan.Problems...)
	if err == nil {
		o.Token = refsToken(plan)
		if err = plan.applyOnly(opts.Only); err != nil {
			o.Token = nil
		}
		plan.Render(progress)
	}
	for _, r := range plan.Rows {
		o.Items = append(o.Items, refItem(r))
	}
	return writeRefsPlan(out, o, err)
}

// RefsSweepPlanFailed writes the plan object for a sweep that could not open
// the repository.
func RefsSweepPlanFailed(out io.Writer, err error) {
	_ = writeRefsPlan(out, newRefsPlanOutput(), err)
}

func writeRefsPlan(w io.Writer, o RefsPlanOutput, err error) error {
	if err != nil {
		o.Error = strp(err.Error())
	}
	if werr := writeJSON(w, o); werr != nil && err == nil {
		return werr
	}
	return err
}

// RefsJournal collects what a ref sweep did, row by row, so that one object
// is written at the end: by the sweep when it returns, or by the signal
// handler when it does not. Every method is safe from both, and the object
// is written once. A nil journal records nothing.
type RefsJournal struct {
	mu       sync.Mutex
	once     sync.Once
	out      io.Writer
	root     string
	url      string
	res      RefsResult
	rows     map[string]RefRow
	byID     map[string]*RefResultItem
	inFlight string
	applying bool
}

// NewRefsJournal is a journal that writes its object to out.
func NewRefsJournal(out io.Writer) *RefsJournal {
	return &RefsJournal{out: out, rows: map[string]RefRow{}, byID: map[string]*RefResultItem{},
		res: RefsResult{Schema: 1, SchemaVersion: schema.VersionOf("refs-sweep"), Command: refsSweepCommand,
			Problems: []RefProblem{}, Items: []*RefResultItem{}}}
}

// planned records the plan: every selected row not run yet, every row the
// plan keeps kept with its codes, the rest not selected.
func (j *RefsJournal) planned(p RefsPlan, token *string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.root = p.Repo
	if p.Endpoint != nil {
		j.url = p.Endpoint.raw
	}
	j.res.Repo, j.res.Trunk, j.res.Fetched, j.res.Remote, j.res.Token =
		strp(p.Repo), strp(p.Trunk), p.Fetched, p.Remote, token
	j.res.Items = []*RefResultItem{}
	j.res.Problems = append([]RefProblem{}, p.Problems...)
	for _, r := range p.Rows {
		it := &RefResultItem{ID: r.ID, Kind: r.Kind, Name: r.Name, Category: r.Category, Selected: r.Selected,
			Tip: strp(r.Tip), Object: r.Object, Result: RefNotRun}
		switch {
		case r.Category == RefKept:
			it.Result, it.Reason = RefResultKept, strp(strings.Join(r.Kept, ", "))
		case !r.Selected:
			it.Result = RefNotSelected
		}
		j.res.Items = append(j.res.Items, it)
		j.byID[r.ID], j.rows[r.ID] = it, r
	}
}

// begin records the run about to move the selected rows, and each one's pin.
func (j *RefsJournal) begin(runID string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.RunID, j.applying = strp(runID), true
	for _, it := range j.res.Items {
		if it.Selected {
			it.Pin = strp(pinOf(runID, it.Kind, it.Name))
		}
	}
}

// abandon takes back a run whose meta could not be written: nothing of it
// exists.
func (j *RefsJournal) abandon() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.RunID, j.applying = nil, false
	for _, it := range j.res.Items {
		it.Pin = nil
	}
}

// problem records why the run refused.
func (j *RefsJournal) problem(code string, err error) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.Problems = append(j.res.Problems, RefProblem{Code: code, Text: err.Error()})
}

// start marks the row about to be moved: the one a signal would catch.
func (j *RefsJournal) start(id string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inFlight = id
}

// settle records what one row came to.
func (j *RefsJournal) settle(id string, st refState) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inFlight = ""
	if it := j.byID[id]; it != nil {
		settleRefItem(it, deref(j.res.RunID), st)
	}
}

func settleRefItem(it *RefResultItem, runID string, st refState) {
	it.Pinned, it.Deleted = st.pinned, st.deleted
	switch {
	case st.kept != "":
		it.Result, it.Reason = RefResultKept, strp(st.kept)
	case st.err == nil && st.pinned && st.deleted:
		it.Result, it.Reason = RefSwept, nil
	default:
		it.Result = RefFailed
		why := "not finished"
		if st.err != nil {
			why = oneLine(st.err.Error())
		}
		it.Reason = strp(why)
	}
	if it.Pinned && it.Deleted {
		it.RestoreCommand = []string{"wt", "refs", "restore", runID, "--only", it.ID, "--yes"}
	}
}

// fail records an error that ended the run before anything was moved.
func (j *RefsJournal) fail(err error) {
	if j == nil || err == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.applying {
		j.res.Error = strp(err.Error())
	}
}

// Fail records an error that stopped the run before it began, and writes
// the object.
func (j *RefsJournal) Fail(err error) {
	j.fail(err)
	j.Finish()
}

// Finish writes the object for a run that returned.
func (j *RefsJournal) Finish() { j.write(false, "") }

// interrupted writes the object for a run a signal ended: the row in flight
// is read back and marked interrupted.
func (j *RefsJournal) interrupted(_ string, recovery string) { j.write(true, recovery) }

func (j *RefsJournal) write(signalled bool, recovery string) {
	if j == nil {
		return
	}
	j.once.Do(func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if signalled {
			// The handler's own git is not parked, and nothing else holds the
			// lock while running one.
			if it := j.byID[j.inFlight]; it != nil {
				r := j.rows[j.inFlight]
				pin := deref(it.Pin)
				var st refState
				if remoteKind(r.Kind) {
					st = readRemoteState(j.root, j.url, r, pin)
				} else {
					st = readLocalState(j.root, r, pin)
				}
				settleRefItem(it, deref(j.res.RunID), st)
				it.Result, it.Reason = RefInterrupted, nil
			}
			j.res.Outcome, j.res.Recovery = OutcomeInterrupted, strp(recovery)
		} else {
			j.res.Outcome = refsOutcome(j.res.Items, j.res.Error != nil)
		}
		_ = writeJSON(j.out, j.res)
	})
}

// readRemoteState reads back a remote row for an interrupted run: its pin
// here, and origin briefly.
func readRemoteState(root, url string, r RefRow, pin string) refState {
	var st refState
	if v, ok, err := refValue(root, pin); err == nil && ok && v == r.Object {
		st.pinned = true
	}
	if _, there, err := remoteValue(root, url, r.Ref, 10*time.Second); err == nil && !there {
		st.deleted = true
	}
	return st
}

// refsOutcome is the rule: done when every selected row was swept and
// nothing stopped the run first; refused when nothing was pinned or
// deleted; partial otherwise.
func refsOutcome(items []*RefResultItem, failedEarly bool) string {
	finished, changed := true, false
	for _, it := range items {
		if it.Pinned || it.Deleted {
			changed = true
		}
		if it.Selected && it.Result != RefSwept {
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
