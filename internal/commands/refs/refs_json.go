package refs

import (
	"io"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
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

// PlanOutput is the one object wt refs sweep --dry-run --json prints.
type PlanOutput struct {
	Schema        int               `json:"schema"`
	SchemaVersion string            `json:"schemaVersion"`
	Command       string            `json:"command"`
	Repo          *string           `json:"repo"`
	Trunk         *string           `json:"trunk"`
	TrunkSource   *string           `json:"trunkSource"`
	Fetched       bool              `json:"fetched"`
	Remote        bool              `json:"remote"`
	Patterns      []string          `json:"patterns"`
	Age           *string           `json:"age"`
	Now           int64             `json:"now"`
	Endpoint      *refpins.Endpoint `json:"endpoint"`
	RunID         *string           `json:"runId"`
	Token         *string           `json:"token"`
	Error         *string           `json:"error"`
	Problems      []RefProblem      `json:"problems"`
	Items         []RefItem         `json:"items"`
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

// Result is the one object wt refs sweep --yes --json prints.
type Result struct {
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
		Selected: r.Selected, Tip: kit.Strp(r.Tip), Object: r.Object, Annotated: r.Annotated,
		ContainedIn: kit.Strp(r.ContainedIn), UniqueCommits: r.Unique, DateSource: kit.Strp(r.DateSource),
		Kept: append([]string{}, r.Kept...), Subject: kit.Strp(r.Subject)}
	if r.DateSource != "" {
		d := r.Date
		it.Date = &d
	}
	return it
}

func newRefsPlanOutput() PlanOutput {
	return PlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("refs-sweep-plan"), Command: refsSweepCommand,
		Patterns: []string{}, Problems: []RefProblem{}, Items: []RefItem{}}
}

// SweepPlanJSON writes wt refs sweep --dry-run --json: every backup the
// patterns match, what it is and whether it would go, and the token that
// holds a later sweep to this plan. It fetches unless told not to, and moves
// nothing. A plan that cannot be made is still one object, with error set,
// and the error is returned too.
func SweepPlanJSON(ctx *kit.Context, opts Options, out, progress io.Writer) error {
	o := newRefsPlanOutput()
	if ctx == nil {
		return writeRefsPlan(out, o, kit.ErrNotInRepo)
	}
	plan, err := planRefs(ctx, opts, progress)
	o.Repo, o.Trunk, o.TrunkSource = kit.Strp(plan.Repo), kit.Strp(plan.Trunk), plan.TrunkSource
	o.Fetched, o.Remote, o.Age, o.Now = plan.Fetched, plan.Remote, kit.Strp(plan.Age), plan.Now
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

// SweepPlanFailed writes the plan object for a sweep that could not open
// the repository.
func SweepPlanFailed(out io.Writer, err error) {
	_ = writeRefsPlan(out, newRefsPlanOutput(), err)
}

func writeRefsPlan(w io.Writer, o PlanOutput, err error) error {
	if err != nil {
		o.Error = kit.Strp(err.Error())
	}
	if werr := kit.WriteJSON(w, o); werr != nil && err == nil {
		return werr
	}
	return err
}

// Journal collects what a ref sweep did, row by row, so that one object
// is written at the end: by the sweep when it returns, or by the signal
// handler when it does not. A nil journal records nothing.
type Journal struct {
	*kit.Journal[Result]
	root string
	url  string
	rows map[string]RefRow
	byID map[string]*RefResultItem
}

// NewRefsJournal is a journal that writes its object to out.
func NewRefsJournal(out io.Writer) *Journal {
	j := &Journal{rows: map[string]RefRow{}, byID: map[string]*RefResultItem{}}
	j.Journal = kit.NewJournal(out, Result{Schema: 1, SchemaVersion: schema.VersionOf("refs-sweep"),
		Command: refsSweepCommand, Problems: []RefProblem{}, Items: []*RefResultItem{}},
		kit.JournalRules[Result]{Error: func(r *Result, err error) { r.Error = kit.Strp(err.Error()) },
			Caught: j.caught, Close: closeRefs})
	return j
}

// Fail records an error that stopped the run before it began, and writes
// the object.
func (j *Journal) Fail(err error) {
	if j != nil {
		j.Journal.Fail(err)
	}
}

// Finish writes the object for a run that returned.
func (j *Journal) Finish() {
	if j != nil {
		j.Journal.Finish()
	}
}

// Interrupted writes the object for a run a signal ended.
func (j *Journal) Interrupted(inFlight, recovery string) {
	if j != nil {
		j.Journal.Interrupted(inFlight, recovery)
	}
}

// planned records the plan: every selected row not run yet, every row the
// plan keeps kept with its codes, the rest not selected.
func (j *Journal) planned(p Plan, token *string) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	j.root = p.Repo
	if p.Endpoint != nil {
		j.url = p.Endpoint.Raw
	}
	j.Res.Repo, j.Res.Trunk, j.Res.Fetched, j.Res.Remote, j.Res.Token =
		kit.Strp(p.Repo), kit.Strp(p.Trunk), p.Fetched, p.Remote, token
	j.Res.Items = []*RefResultItem{}
	j.Res.Problems = append([]RefProblem{}, p.Problems...)
	for _, r := range p.Rows {
		it := &RefResultItem{ID: r.ID, Kind: r.Kind, Name: r.Name, Category: r.Category, Selected: r.Selected,
			Tip: kit.Strp(r.Tip), Object: r.Object, Result: RefNotRun}
		switch {
		case r.Category == RefKept:
			it.Result, it.Reason = RefResultKept, kit.Strp(strings.Join(r.Kept, ", "))
		case !r.Selected:
			it.Result = RefNotSelected
		}
		j.Res.Items = append(j.Res.Items, it)
		j.byID[r.ID], j.rows[r.ID] = it, r
	}
}

// begin records the run about to move the selected rows, and each one's pin.
func (j *Journal) begin(runID string) {
	if j == nil {
		return
	}
	j.Begin(func(r *Result) {
		r.RunID = kit.Strp(runID)
		for _, it := range r.Items {
			if it.Selected {
				it.Pin = kit.Strp(refpins.PinOf(runID, it.Kind, it.Name))
			}
		}
	})
}

// abandon takes back a run whose meta could not be written: nothing of it
// exists.
func (j *Journal) abandon() {
	if j == nil {
		return
	}
	j.Abandon(func(r *Result) {
		r.RunID = nil
		for _, it := range r.Items {
			it.Pin = nil
		}
	})
}

// problem records why the run refused.
func (j *Journal) problem(code string, err error) {
	if j == nil {
		return
	}
	j.Edit(func(r *Result) { r.Problems = append(r.Problems, RefProblem{Code: code, Text: err.Error()}) })
}

// start marks the row about to be moved: the one a signal would catch.
func (j *Journal) start(id string) {
	if j == nil {
		return
	}
	j.Start(id)
}

// settle records what one row came to.
func (j *Journal) settle(id string, st refState) {
	if j == nil {
		return
	}
	j.Settle(id, func(r *Result) {
		if it := j.byID[id]; it != nil {
			settleRefItem(it, kit.Deref(r.RunID), st)
		}
	})
}

func settleRefItem(it *RefResultItem, runID string, st refState) {
	it.Pinned, it.Deleted = st.pinned, st.deleted
	switch {
	case st.kept != "":
		it.Result, it.Reason = RefResultKept, kit.Strp(st.kept)
	case st.err == nil && st.pinned && st.deleted:
		it.Result, it.Reason = RefSwept, nil
	default:
		it.Result = RefFailed
		why := "not finished"
		if st.err != nil {
			why = kit.OneLine(st.err.Error())
		}
		it.Reason = kit.Strp(why)
	}
	if it.Pinned && it.Deleted {
		it.RestoreCommand = []string{"wt", "refs", "restore", runID, "--only", it.ID, "--yes"}
	}
}

// caught reads back the row a signal caught in flight and marks it
// interrupted.
func (j *Journal) caught(r *Result, inFlight, _ string) {
	it := j.byID[inFlight]
	if it == nil {
		return
	}
	row := j.rows[inFlight]
	pin := kit.Deref(it.Pin)
	var st refState
	if refpins.RemoteKind(row.Kind) {
		st = readRemoteState(j.root, j.url, row, pin)
	} else {
		st = readLocalState(j.root, row, pin)
	}
	settleRefItem(it, kit.Deref(r.RunID), st)
	it.Result, it.Reason = RefInterrupted, nil
}

func closeRefs(r *Result, signalled bool, recovery string) any {
	if signalled {
		r.Outcome, r.Recovery = kit.OutcomeInterrupted, kit.Strp(recovery)
	} else {
		r.Outcome = refsOutcome(r.Items, r.Error != nil)
	}
	return r
}

// readRemoteState reads back a remote row for an interrupted run: its pin
// here, and origin briefly.
func readRemoteState(root, url string, r RefRow, pin string) refState {
	var st refState
	if v, ok, err := refpins.RefValue(root, pin); err == nil && ok && v == r.Object {
		st.pinned = true
	}
	if _, there, err := refpins.RemoteValue(root, url, r.Ref, 10*time.Second); err == nil && !there {
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
		return kit.OutcomeDone
	case !changed:
		return kit.OutcomeRefused
	}
	return kit.OutcomePartial
}
