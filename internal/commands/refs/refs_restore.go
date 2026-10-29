package refs

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/schema"
)

// What a restore does with one pin, decided from what is at its name now.
const (
	// RestoreCreate: the name is free; the ref is made at the pinned object.
	RestoreCreate = "create"
	// RestoreNone: the ref is there at the pinned object already; only the
	// pin goes.
	RestoreNone = "none"
	// RestoreOccupied: the name holds another object here.
	RestoreOccupied = "occupied"
	// RestoreRemoteOccupied: origin has something at the name.
	RestoreRemoteOccupied = "remoteOccupied"
	// RestorePinGone: the pin is not there any more.
	RestorePinGone = "pinGone"
	// RestoreEndpointChanged: a remote ref, and origin now resolves to other
	// URLs than the ones the run deleted it from.
	RestoreEndpointChanged = "endpointChanged"
	// RestoreEndpointUnknown: a remote ref, and the run has no meta saying
	// which origin it deleted it from.
	RestoreEndpointUnknown = "endpointUnknown"
)

// RefRestored is a row of a restore that ran whose ref is back and whose pin
// is gone.
const RefRestored = "restored"

// restoreRow is one pin of the run and what a restore makes of it.
type restoreRow struct {
	refpins.SweptRef
	Action   string
	Selected bool
}

// RestoreRefsPlan is what wt refs restore <runId> would put back.
type RestoreRefsPlan struct {
	Repo, RunID string
	Rows        []restoreRow
	// endpoint is origin as resolved now, when the run has remote pins and
	// origin resolves to one clean URL; every remote call goes to it.
	endpoint *refpins.Endpoint
}

// RestoreRefsOptions carries what wt refs restore was asked.
type RestoreRefsOptions struct {
	Only    []string
	DryRun  bool
	Yes     bool
	Confirm kit.Confirm[RestoreRefsPlan]
	Expect  string
	Journal *RestoreRefsJournal
}

// ErrNoRun is a run id no pins are under.
func ErrNoRun(runID string) error {
	return fmt.Errorf("no swept run %s here: wt refs swept lists the runs", runID)
}

// PlanRefsRestore reads the run's pins and, for each, what is at its name
// now: here, and on origin for the remote ones, asked once.
func PlanRefsRestore(ctx *kit.Context, runID string) (RestoreRefsPlan, error) {
	p := RestoreRefsPlan{Repo: ctx.Repo.MainRoot, RunID: runID}
	if !refpins.RunIDPattern.MatchString(runID) {
		return p, fmt.Errorf("%q is not a run id; they look like 20260930T091500Z-3f2a", runID)
	}
	runs, err := refpins.ReadSwept(p.Repo)
	if err != nil {
		return p, err
	}
	i := slices.IndexFunc(runs, func(r refpins.SweptRun) bool { return r.RunID == runID })
	if i < 0 {
		return p, ErrNoRun(runID)
	}
	run := runs[i]
	var origin *refpins.RemoteRefs
	var today *refpins.Endpoint
	var todayErr error
	resolved := false
	for _, s := range run.Refs {
		row := restoreRow{SweptRef: s}
		if refpins.RemoteKind(s.Kind) && !resolved {
			today, todayErr = refpins.ResolveEndpoint(p.Repo)
			resolved = true
			if todayErr == nil {
				p.endpoint = today
			}
		}
		switch {
		case !refpins.RemoteKind(s.Kind):
		case run.Meta == nil || run.Meta.Endpoint == nil:
			row.Action = RestoreEndpointUnknown
		case todayErr != nil || today.Digest != run.Meta.Endpoint.Digest:
			row.Action = RestoreEndpointChanged
		}
		if row.Action != "" {
			p.Rows = append(p.Rows, row)
			continue
		}
		if refpins.RemoteKind(s.Kind) {
			if origin == nil {
				rr, err := refpins.ReadRemoteRefs(p.Repo, today.Raw)
				if err != nil {
					return p, err
				}
				origin = &rr
			}
			switch now, ok := origin.Values[s.Ref()]; {
			case !ok:
				row.Action = RestoreCreate
			case now == s.Object:
				row.Action = RestoreNone
			default:
				row.Action = RestoreRemoteOccupied
			}
		} else {
			switch now, ok, err := refpins.RefValue(p.Repo, s.Ref()); {
			case err != nil:
				return p, err
			case !ok:
				row.Action = RestoreCreate
			case now == s.Object:
				row.Action = RestoreNone
			default:
				row.Action = RestoreOccupied
			}
		}
		row.Selected = row.restorable()
		p.Rows = append(p.Rows, row)
	}
	return p, nil
}

func (r restoreRow) restorable() bool { return r.Action == RestoreCreate || r.Action == RestoreNone }

func (r restoreRow) remoteRestorable() bool { return refpins.RemoteKind(r.Kind) && r.restorable() }

// applyOnly replaces the default selection. An id the run does not have,
// or one whose name is taken, refuses the lot.
func (p *RestoreRefsPlan) applyOnly(only []string) error {
	if only == nil {
		return nil
	}
	want := map[string]bool{}
	for _, id := range only {
		want[id] = true
	}
	var unknown, taken []string
	for id := range want {
		i := slices.IndexFunc(p.Rows, func(r restoreRow) bool { return r.ID() == id })
		switch {
		case i < 0:
			unknown = append(unknown, id)
		case !p.Rows[i].restorable():
			taken = append(taken, id+" ("+p.Rows[i].Action+")")
		}
	}
	sort.Strings(unknown)
	sort.Strings(taken)
	switch {
	case len(unknown) > 0:
		return fmt.Errorf("nothing was restored: --only names what run %s does not have: %s",
			p.RunID, strings.Join(unknown, ", "))
	case len(taken) > 0:
		return fmt.Errorf("nothing was restored: --only names what cannot go back: %s", strings.Join(taken, ", "))
	}
	for i := range p.Rows {
		p.Rows[i].Selected = want[p.Rows[i].ID()]
	}
	return nil
}

// restoreToken names a restore plan: the repository, the run, origin as
// resolved now when a remote ref can go back to it, and every pin with its
// object and what would be done with it. Nil when nothing can go back.
func restoreToken(p RestoreRefsPlan) *string { return restoreTokenOf(p).Strp() }

func restoreTokenOf(p RestoreRefsPlan) *kit.Token {
	if !slices.ContainsFunc(p.Rows, restoreRow.restorable) {
		return nil
	}
	t := kit.NewToken("rr1-", "wt-refs-restore").Str("Repo", p.Repo).Str("RunID", p.RunID)
	if p.endpoint != nil && slices.ContainsFunc(p.Rows, restoreRow.remoteRestorable) {
		t.Str("endpoint", p.endpoint.Digest)
	}
	for _, r := range p.Rows {
		t.Str("Rows.Kind", r.Kind).Str("Rows.Name", r.Name).Str("Rows.Pin", r.Pin).Str("Rows.Object", r.Object).
			Str("Rows.Action", r.Action)
	}
	return t
}

var errRestorePlanChanged = errors.New("the restore plan changed since it was read (a pin went, or a name " +
	"was taken or freed); nothing was restored: read the plan again")

// RestoreRefs puts back what a ref sweep moved: each ref is made again at
// its pinned object, only where its name is free, and the pin goes with it.
func RestoreRefs(ctx *kit.Context, runID string, opts RestoreRefsOptions, w io.Writer) (err error) {
	j := opts.Journal
	if j != nil {
		defer kit.SetInterruptJournal(j)()
		defer kit.WatchSignals(w, nil)()
		defer func() { j.Fail(err) }()
	}
	plan, err := PlanRefsRestore(ctx, runID)
	j.planned(plan, nil)
	if err != nil {
		return err
	}
	token := restoreToken(plan)
	j.planned(plan, token)
	if opts.Expect != "" && kit.Deref(token) != opts.Expect {
		return errRestorePlanChanged
	}
	if err := plan.applyOnly(opts.Only); err != nil {
		return err
	}
	j.planned(plan, token)
	plan.Render(w)
	n := 0
	for _, r := range plan.Rows {
		if r.Selected {
			n++
		}
	}
	switch {
	case n == 0:
		fmt.Fprintln(w, "Nothing to restore.")
		return nil
	case opts.DryRun:
		fmt.Fprintln(w, "Nothing was restored: --dry-run.")
		return nil
	case opts.Yes:
	case opts.Confirm != nil:
		if !opts.Confirm(plan) {
			fmt.Fprintln(w, "Nothing was restored.")
			return nil
		}
		fresh, err := PlanRefsRestore(ctx, runID)
		if err != nil {
			return err
		}
		if kit.Deref(restoreToken(fresh)) != kit.Deref(token) {
			return errRestorePlanChanged
		}
	default:
		fmt.Fprintf(w, "Nothing was restored: there is no terminal to ask. Pass --yes to restore %s.\n", kit.Count(n, "ref"))
		return nil
	}
	// Origin is read again right before anything goes to it.
	if plan.endpoint != nil {
		now, err := refpins.ResolveEndpoint(ctx.Repo.MainRoot)
		if err != nil || now.Digest != plan.endpoint.Digest {
			return errors.New("nothing was restored: origin's URL changed since the plan was made; read the plan again")
		}
	}
	j.begin(plan.url())
	root := ctx.Repo.MainRoot
	failed := 0
	for _, r := range plan.Rows {
		if !r.Selected {
			continue
		}
		j.start(r.ID())
		st := RestoreRef(root, plan.url(), r)
		j.settle(r.ID(), st)
		switch {
		case st.Kept != "":
			fmt.Fprintf(w, "- kept %s: %s\n", r.ID(), st.Kept)
			failed++
		case st.err != nil || !st.restored || !st.PinDeleted:
			why := "not finished"
			if st.err != nil {
				why = kit.OneLine(st.err.Error())
			}
			fmt.Fprintf(w, "! %s: %s\n", r.ID(), why)
			failed++
		default:
			fmt.Fprintf(w, "✓ restored %s\n", r.ID())
		}
	}
	refpins.DropEmptyRun(root, runID, w)
	if failed > 0 {
		return fmt.Errorf("%d of %s kept or not finished", failed, kit.Count(n, "ref"))
	}
	return nil
}

// RestoreState is what one row came to, read back.
type RestoreState struct {
	Kept                 string
	err                  error
	restored, PinDeleted bool
}

// RestoreRef puts one ref back. Here that is one transaction: the ref made
// only where it is not, and the pin deleted only at its object. On origin
// it is a push leased on the ref being absent, then the pin.
func RestoreRef(root, url string, r restoreRow) RestoreState {
	now, ok, err := refpins.RefValue(root, r.Pin)
	switch {
	case err != nil:
		return RestoreState{err: err}
	case !ok || now != r.Object:
		return RestoreState{Kept: RestorePinGone + ": the pin moved or went after the plan was made"}
	}
	var opErr error
	switch {
	case r.Action == RestoreNone && refpins.RemoteKind(r.Kind):
		// Only while origin is read back holding it does the pin go.
		switch v, there, verr := refpins.RemoteValue(root, url, r.Ref(), kit.NetworkTimeout); {
		case verr != nil:
			return RestoreState{err: fmt.Errorf("origin could not be read, so the pin stays: %w", verr)}
		case !there || v != r.Object:
			return RestoreState{Kept: "origin no longer holds it at the pinned object, so the pin stays"}
		}
		opErr = refpins.DeleteRefAt(root, r.Pin, now)
	case r.Action == RestoreNone:
		// The pin goes only in the same transaction that sees the ref at it.
		opErr = refpins.UpdateRefs(root, "wt refs restore", "verify "+r.Ref()+" "+r.Object, "delete "+r.Pin+" "+r.Object)
		if opErr != nil {
			if v, there, _ := refpins.RefValue(root, r.Ref()); !there || v != r.Object {
				return RestoreState{Kept: "it moved or went after the plan was made, so the pin stays"}
			}
		}
	case refpins.RemoteKind(r.Kind):
		// The pin goes only once origin is read back holding the object: a
		// push that was rejected, timed out or cannot be confirmed keeps it,
		// and a later restore finds the ref there and only drops the pin.
		_, pushErr := git.RunTimeout(root, kit.NetworkTimeout, "push", "--quiet", "--no-verify",
			"--force-with-lease="+r.Ref()+":", url, r.Pin+":"+r.Ref())
		v, there, verr := refpins.RemoteValue(root, url, r.Ref(), kit.NetworkTimeout)
		switch {
		case pushErr != nil:
			return RestoreState{restored: verr == nil && there && v == r.Object,
				err: fmt.Errorf("the push failed, so the pin stays: %s", git.Reason(pushErr))}
		case verr != nil:
			return RestoreState{err: fmt.Errorf("pushed, but origin could not be read back, so the pin stays: %w", verr)}
		case !there || v != r.Object:
			return RestoreState{err: errors.New("pushed, but origin does not hold it, so the pin stays")}
		}
		opErr = refpins.DeleteRefAt(root, r.Pin, now)
	default:
		opErr = refpins.UpdateRefs(root, "wt refs restore", "create "+r.Ref()+" "+r.Object, "delete "+r.Pin+" "+r.Object)
		if opErr != nil {
			if v, there, _ := refpins.RefValue(root, r.Ref()); there && v != r.Object {
				return RestoreState{Kept: "its name was taken after the plan was made"}
			}
		}
	}
	st := readRestoreState(root, url, r, kit.NetworkTimeout)
	st.err = opErr
	return st
}

// url is origin's URL for the remote rows; "" when the run has none.
func (p RestoreRefsPlan) url() string {
	if p.endpoint == nil {
		return ""
	}
	return p.endpoint.Raw
}

// readRestoreState reads back one row: the ref at its object, and the pin.
func readRestoreState(root, url string, r restoreRow, timeout time.Duration) RestoreState {
	var st RestoreState
	if refpins.RemoteKind(r.Kind) {
		if v, ok, err := refpins.RemoteValue(root, url, r.Ref(), timeout); err == nil && ok && v == r.Object {
			st.restored = true
		}
	} else if v, ok, err := refpins.RefValue(root, r.Ref()); err == nil && ok && v == r.Object {
		st.restored = true
	}
	if _, ok, err := refpins.RefValue(root, r.Pin); err == nil && !ok {
		st.PinDeleted = true
	}
	return st
}

// Render writes what the restore would do.
func (p RestoreRefsPlan) Render(w io.Writer) {
	var rows [][]string
	for _, r := range p.Rows {
		what := map[string]string{RestoreCreate: "goes back", RestoreNone: "is back already; the pin goes",
			RestoreOccupied:        "kept: its name holds another commit here",
			RestoreRemoteOccupied:  "kept: origin has something at its name",
			RestoreEndpointChanged: "kept: origin is not the one it was swept from",
			RestoreEndpointUnknown: "kept: the run does not say which origin it came from"}[r.Action]
		if !r.Selected && r.restorable() {
			what = "not selected"
		}
		rows = append(rows, []string{"  " + r.ID(), git.ShortID(r.Object, 12), what})
	}
	fmt.Fprintf(w, "Run %s:\n", p.RunID)
	_ = kit.PrintTable(w, rows)
	fmt.Fprintln(w)
}

// RefRestorePlanItem is one pin of a restore plan, for --json.
type RefRestorePlanItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Pin      string `json:"pin"`
	Object   string `json:"object"`
	Action   string `json:"action"`
	Selected bool   `json:"selected"`
}

// RestoreRefsPlanOutput is the one object wt refs restore --dry-run --json
// prints.
type RestoreRefsPlanOutput struct {
	Schema        int                  `json:"schema"`
	SchemaVersion string               `json:"schemaVersion"`
	Command       string               `json:"command"`
	Repo          *string              `json:"repo"`
	RunID         string               `json:"runId"`
	Token         *string              `json:"token"`
	Error         *string              `json:"error"`
	Items         []RefRestorePlanItem `json:"items"`
}

// RefRestoreItem is what became of one pin, for --json.
type RefRestoreItem struct {
	RefRestorePlanItem
	Result     string  `json:"result"`
	Reason     *string `json:"reason"`
	Restored   bool    `json:"restored"`
	PinDeleted bool    `json:"pinDeleted"`
}

// RestoreRefsResult is the one object wt refs restore --yes --json prints.
type RestoreRefsResult struct {
	Schema        int               `json:"schema"`
	SchemaVersion string            `json:"schemaVersion"`
	Command       string            `json:"command"`
	Repo          *string           `json:"repo"`
	RunID         string            `json:"runId"`
	Token         *string           `json:"token"`
	Outcome       string            `json:"outcome"`
	Error         *string           `json:"error"`
	Recovery      *string           `json:"recovery"`
	Items         []*RefRestoreItem `json:"items"`
}

const refsRestoreCommand = "refs restore"

func restorePlanItem(r restoreRow) RefRestorePlanItem {
	return RefRestorePlanItem{ID: r.ID(), Kind: r.Kind, Name: r.Name, Pin: r.Pin, Object: r.Object,
		Action: r.Action, Selected: r.Selected}
}

// RestoreRefsPlanJSON writes wt refs restore <runId> --dry-run --json. It
// changes nothing; a plan that cannot be made is one object too.
func RestoreRefsPlanJSON(ctx *kit.Context, runID string, only []string, out, progress io.Writer) error {
	o := RestoreRefsPlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("refs-restore-plan"),
		Command: refsRestoreCommand, RunID: runID, Items: []RefRestorePlanItem{}}
	var err error
	if ctx == nil {
		err = kit.ErrNotInRepo
	} else {
		var plan RestoreRefsPlan
		plan, err = PlanRefsRestore(ctx, runID)
		o.Repo = kit.Strp(plan.Repo)
		if err == nil {
			o.Token = restoreToken(plan)
			if err = plan.applyOnly(only); err != nil {
				o.Token = nil
			}
			plan.Render(progress)
		}
		for _, r := range plan.Rows {
			o.Items = append(o.Items, restorePlanItem(r))
		}
	}
	if err != nil {
		o.Error = kit.Strp(err.Error())
	}
	if werr := kit.WriteJSON(out, o); werr != nil && err == nil {
		return werr
	}
	return err
}

// RestoreRefsJournal writes wt refs restore --yes --json's one object once:
// when the restore returns, or when a signal ends it.
type RestoreRefsJournal struct {
	*kit.Journal[RestoreRefsResult]
	root string
	url  string
	rows map[string]restoreRow
	byID map[string]*RefRestoreItem
}

// NewRefsRestoreJournal is a journal that writes its object to out.
func NewRefsRestoreJournal(out io.Writer, runID string) *RestoreRefsJournal {
	j := &RestoreRefsJournal{rows: map[string]restoreRow{}, byID: map[string]*RefRestoreItem{}}
	j.Journal = kit.NewJournal(out, RestoreRefsResult{Schema: 1, SchemaVersion: schema.VersionOf("refs-restore"),
		Command: refsRestoreCommand, RunID: runID, Items: []*RefRestoreItem{}},
		kit.JournalRules[RestoreRefsResult]{Error: func(r *RestoreRefsResult, err error) { r.Error = kit.Strp(err.Error()) },
			Caught: j.caught, Close: closeRestoreRefs})
	return j
}

// Fail records an error that stopped the run before it began, and writes
// the object.
func (j *RestoreRefsJournal) Fail(err error) {
	if j != nil {
		j.Journal.Fail(err)
	}
}

// Finish writes the object for a run that returned.
func (j *RestoreRefsJournal) Finish() {
	if j != nil {
		j.Journal.Finish()
	}
}

// Interrupted writes the object for a run a signal ended.
func (j *RestoreRefsJournal) Interrupted(inFlight, recovery string) {
	if j != nil {
		j.Journal.Interrupted(inFlight, recovery)
	}
}

func (j *RestoreRefsJournal) planned(p RestoreRefsPlan, token *string) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	j.root, j.Res.Repo, j.Res.Token = p.Repo, kit.Strp(p.Repo), token
	j.Res.Items = []*RefRestoreItem{}
	for _, r := range p.Rows {
		it := &RefRestoreItem{RefRestorePlanItem: restorePlanItem(r), Result: RefNotRun}
		switch {
		case !r.restorable():
			it.Result, it.Reason = RefResultKept, kit.Strp(r.Action)
		case !r.Selected:
			it.Result = RefNotSelected
		}
		j.Res.Items = append(j.Res.Items, it)
		j.byID[r.ID()], j.rows[r.ID()] = it, r
	}
}

func (j *RestoreRefsJournal) begin(url string) {
	if j == nil {
		return
	}
	j.Begin(func(*RestoreRefsResult) { j.url = url })
}

func (j *RestoreRefsJournal) start(id string) {
	if j == nil {
		return
	}
	j.Start(id)
}

func (j *RestoreRefsJournal) settle(id string, st RestoreState) {
	if j == nil {
		return
	}
	j.Settle(id, func(*RestoreRefsResult) {
		if it := j.byID[id]; it != nil {
			settleRestoreItem(it, st)
		}
	})
}

func settleRestoreItem(it *RefRestoreItem, st RestoreState) {
	it.Restored, it.PinDeleted = st.restored, st.PinDeleted
	switch {
	case st.Kept != "":
		it.Result, it.Reason = RefResultKept, kit.Strp(st.Kept)
	case st.err == nil && st.restored && st.PinDeleted:
		it.Result, it.Reason = RefRestored, nil
	default:
		it.Result = RefFailed
		why := "not finished"
		if st.err != nil {
			why = kit.OneLine(st.err.Error())
		}
		it.Reason = kit.Strp(why)
	}
}

// caught reads back the row a signal caught in flight and marks it
// interrupted.
func (j *RestoreRefsJournal) caught(_ *RestoreRefsResult, inFlight, _ string) {
	if it := j.byID[inFlight]; it != nil {
		settleRestoreItem(it, readRestoreState(j.root, j.url, j.rows[inFlight], 10*time.Second))
		it.Result, it.Reason = RefInterrupted, nil
	}
}

func closeRestoreRefs(r *RestoreRefsResult, signalled bool, recovery string) any {
	if signalled {
		r.Outcome, r.Recovery = kit.OutcomeInterrupted, kit.Strp(recovery)
		return r
	}
	finished, changed := true, false
	for _, it := range r.Items {
		if it.PinDeleted || it.Result == RefRestored {
			changed = true
		}
		if it.Selected && it.Result != RefRestored {
			finished = false
		}
	}
	switch {
	case finished && r.Error == nil:
		r.Outcome = kit.OutcomeDone
	case !changed:
		r.Outcome = kit.OutcomeRefused
	default:
		r.Outcome = kit.OutcomePartial
	}
	return r
}
